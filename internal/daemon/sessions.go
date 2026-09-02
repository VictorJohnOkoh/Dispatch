package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/admission"
	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/harness"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"github.com/VictorJohnOkoh/Dispatch/internal/session"
	"github.com/VictorJohnOkoh/Dispatch/internal/vendors"
	"github.com/VictorJohnOkoh/Dispatch/internal/workspace"
)

// startRequest is the body of POST /v1/sessions. Dir is relative to the Workspace
// Root when it is relative, and the Root itself when it is empty.
type startRequest struct {
	Harness string `json:"harness"`
	Model   string `json:"model"`
	Dir     string `json:"dir"`

	// Policy is the user choosing the Approval Policy at the start. Without it the
	// Session gets the Host config's default, clipped by the Harness's Gates. With
	// it, a slot the Harness cannot gate fails the start rather than being quietly
	// turned into auto: it is the same rule and the same code as a change while the
	// Session runs.
	Policy *event.Policy `json:"policy"`
}

// startedBody is the one command answer with a body. The Seq tells the caller
// where its Session starts in the log.
type startedBody struct {
	Session event.SessionID `json:"session"`
	Seq     uint64          `json:"seq"`
}

// startSession runs the four refusals, writes SessionStarted and answers. The
// launch itself carries on after the answer has gone, because a cold Model load
// took 29.70s in the captures and the caller needs the id to watch it.
func (d *Daemon) startSession(w http.ResponseWriter, r *http.Request) {
	var req startRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		refuse(w, protocol.StatusUnprocessable, protocol.Refusal{
			Reason: protocol.ReasonMalformed, Detail: err.Error(),
		})
		return
	}

	h := d.harness(req.Harness)
	if h == nil {
		refuse(w, protocol.StatusUnprocessable, protocol.Refusal{
			Reason: protocol.ReasonUnknownHarness,
			Detail: fmt.Sprintf("this Host serves no Harness named %q", req.Harness),
		})
		return
	}

	dir, err := d.root.Contain(d.root.String(), req.Dir)
	if err != nil {
		if !errors.Is(err, workspace.ErrOutsideRoot) {
			// The path could not be resolved at all, which is this Host failing
			// rather than the request being wrong, so it is not a Refusal.
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		refuse(w, protocol.StatusUnprocessable, protocol.Refusal{
			Reason: protocol.ReasonWorkspace, Detail: err.Error(),
		})
		return
	}

	if req.Policy != nil {
		if refusal := ungated(h.Adapter.Capabilities(), *req.Policy); refusal != nil {
			refuse(w, protocol.StatusUnprocessable, *refusal)
			return
		}
	}

	vendor := d.vendors.serving(req.Model)
	if vendor == nil {
		refuse(w, protocol.StatusUnprocessable, protocol.Refusal{
			Reason: protocol.ReasonUnknownModel,
			Detail: fmt.Sprintf("no Vendor on this Host serves the Model %q", req.Model),
		})
		return
	}

	// Admission and the registry entry it decided on are one step, so two starts
	// arriving together cannot both read an empty Host and both be admitted.
	d.starting.Lock()
	defer d.starting.Unlock()

	// Admission is asked once, before the Session exists. A refusal writes no Event
	// because there is nothing to write one against, so it goes to the operational
	// log instead.
	if refusal := d.admit.Admit(r.Context(), admission.Request{
		Harness: req.Harness, Model: req.Model, Vendor: vendor, Dir: dir, Live: d.sessions.live(),
	}); refusal != nil {
		d.log.Info("a Session start was refused", "harness", req.Harness, "model", req.Model,
			"reason", refusal.Reason, "blocking", refusal.Blocking)
		refuse(w, protocol.StatusConflict, protocol.Refusal{
			Reason:   protocol.ReasonAdmission,
			Detail:   refusal.Reason,
			Blocking: blockingNames(refusal.Blocking),
		})
		return
	}

	ctx, cancel := context.WithCancel(d.base)
	s := &Session{
		id:      d.sessions.newID(),
		harness: req.Harness,
		caps:    h.Adapter.Capabilities(),
		model:   req.Model,
		vendor:  vendor.Endpoint().Base,
		dir:     dir,
		started: time.Now().UTC(),
		cancel:  cancel,
	}
	s.sink = &sink{d: d, s: s}
	// The Session joins the registry only once its first Event is in the log. A
	// Session with no Event folds to Starting, so one added ahead of a write that
	// failed would hold the Host's one slot for as long as the Daemon runs.
	started, err := d.write(s, event.KindSessionStarted, &event.SessionStarted{
		Harness: s.harness, Model: s.model, Vendor: s.vendor, Cwd: s.dir,
	})
	if err != nil {
		logRefused(w)
		return
	}
	d.sessions.add(s)

	// The Approval Policy is chosen when the Session starts, and it is in the log
	// before the Harness exists, so nothing can be asked before the answer is
	// decidable. A Harness that runs no tools has none at all.
	if policy, setBy, has := d.chosenPolicy(s.caps, req.Policy); has {
		d.write(s, event.KindApprovalPolicySet, &event.ApprovalPolicySet{
			Policy: policy, SetBy: setBy,
		})
	}

	go d.launch(ctx, s, h, vendor)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(protocol.StatusStarted)
	json.NewEncoder(w).Encode(startedBody{Session: s.id, Seq: started.Seq})
}

// launch is the rest of the start, and it runs while the Session is Starting.
//
// Load is called here, before SessionReady, so the wait for a cold Model is inside
// a state the user can see and cancel. It also disables the Vendor's own evictor
// for the Session's life, which is what makes Idle mean idle rather than a Prompt
// that costs twenty seconds. Nothing calls Unload: that is reserved for the VRAM
// policy which is out of v1.
func (d *Daemon) launch(ctx context.Context, s *Session, h *Harness, vendor vendors.Adapter) {
	if err := vendor.Load(ctx, s.model); err != nil {
		d.endFailed(s, event.ErrVendor, fmt.Sprintf("the Model %s would not load: %v", s.model, err))
		return
	}

	run, err := h.Adapter.Start(ctx, harness.SessionSpec{
		Session: s.id,
		Model:   s.model,
		Vendor:  vendor.Endpoint(),
		Dir:     s.dir,
		Spawn:   d.spawner(s, h),
		Files:   files{root: d.root, dir: s.dir},
	}, s.sink)
	if err != nil {
		d.endFailed(s, event.ErrHarnessFailed, err.Error())
		return
	}
	d.sessions.setRun(s, run)

	// A Harness that died during its own launch has ended the Session already, and
	// a Session that is Ready after it Ended reads as a Session that came back.
	if d.sessions.ending(s) {
		return
	}
	d.write(s, event.KindSessionReady, &event.SessionReady{Model: s.model})
}

// endFailed is a launch that did not finish. SessionStarted is already in the log,
// so the record exists and the Session does not, and the pair of Events is what
// says so.
//
// A stop that landed first has already ended the Session, and the failure it
// caused is not news, so nothing is written.
func (d *Daemon) endFailed(s *Session, code event.ErrorCode, msg string) {
	if !d.sessions.endOnce(s) {
		s.cancel()
		return
	}
	d.write(s, event.KindError, &event.Error{Code: code, Message: msg})
	s.sink.end()
	d.write(s, event.KindSessionEnded, &event.SessionEnded{Reason: event.EndFailed})
	// A Harness that died still has a group, and on Windows that group is a handle
	// that a child of its own may still be living inside.
	d.kill(s)
	s.cancel()
}

// ladder is ADR 0008's shutdown ladder, in order. Steps 1 and 2 free a Harness
// that is blocked on an answer nobody is going to give it, so a stop that skips
// them leaves it blocked until the kill. Steps 4 to 6 are the process, and a
// Session stopped while it is still Starting reaches them with no Run, which is
// the skip to step 4 the ladder names.
//
// Because step 6 is a kill, a stop always finishes.
func (d *Daemon) ladder(s *Session, run harness.Run) {
	d.refuseHeld(s, event.BySessionStopped)
	// Step 3 is the goodbye and the fence both. Each Adapter joins its own reader
	// before Close returns, so everything the Harness said is in the log by the
	// time the ledger folds what is still open, and a Session that is Starting has
	// no Run and leans on the Sink for the same thing.
	if run != nil {
		run.Close()
	}
	s.sink.end()
	d.kill(s)
}

// refuseHeld refuses every question this Session has open and frees whoever is
// waiting on one, which is the ladder's steps 1 and 2. A Harness blocked inside
// Approve cannot read anything until it has an answer, so writing the Event
// without delivering it would leave it blocked until the kill.
//
// Each refusal ends its Tool Call with it, because a Tool Call the Daemon refused
// is over. Whatever else is open was in flight and is nobody's to end here.
func (d *Daemon) refuseHeld(s *Session, by event.DecidedBy) {
	for _, call := range d.sessions.held(s) {
		d.refuse(s, call, by)
		d.sessions.tell(s, call, event.DecisionRefused)
	}
}

// kill is the ladder's last three steps against whatever process this Session has,
// and it closes that process's transcript. A Session whose Harness spawns none, or
// one that failed before it spawned, has neither and nothing here to do.
func (d *Daemon) kill(s *Session) {
	p, raw := d.sessions.process(s)
	if p == nil {
		return
	}
	if err := p.stop(d.stopWait); err != nil {
		d.log.Error("the Harness process tree may have outlived its Session", "session", s.id, "err", err)
	}
	if err := raw.Close(); err != nil {
		d.log.Error("the Session's transcript is short of what the Harness said", "session", s.id, "err", err)
	}
}

// listSessions answers with this Host's Sessions, live and ended, in start order.
// The Cursor beside them is where the log stood when they were read, so a Client
// that opens the stream there loses nothing that fell in between.
func (d *Daemon) listSessions(w http.ResponseWriter, r *http.Request) {
	// The Cursor is read first. One read behind the data replays what the answer
	// already carried, which costs a redrawn row; one read ahead of it drops what
	// landed in between, which is the loss this Cursor exists to prevent.
	at := d.events.Cursor()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Sessions []SessionView   `json:"sessions"`
		Cursor   protocol.Cursor `json:"cursor"`
	}{d.sessions.views(), at})
}

// The page GET /v1/sessions/{session}/events serves when the request asks for no
// size, and the largest it will serve whatever the request asks for.
const (
	defaultPage = 200
	maxPage     = 1000
)

// sessionEvents pages one Session's Events, oldest first. It reads the log rather
// than the registry, because the rows are the wire shape already and a Session
// that ended long ago is still in the file.
func (d *Daemon) sessionEvents(w http.ResponseWriter, r *http.Request) {
	id := event.SessionID(r.PathValue("session"))
	after, limit, err := page(r)
	if err != nil {
		refuse(w, protocol.StatusUnprocessable, protocol.Refusal{
			Reason: protocol.ReasonMalformed, Detail: err.Error(),
		})
		return
	}

	// Read before the page, for the reason listSessions gives.
	at := d.events.Cursor()
	events, err := d.events.SessionPage(id, after, limit)
	if err != nil {
		http.Error(w, "the Event log could not be read", http.StatusInternalServerError)
		return
	}
	// A first page with nothing in it is a Session this Host never had. The log is
	// asked rather than the registry, because the registry is memory and a Daemon
	// that restarted has none of it while the rows are all still there.
	if len(events) == 0 && after == 0 {
		refuse(w, protocol.StatusNoSession, protocol.Refusal{
			Reason: protocol.ReasonUnknownSession,
			Detail: fmt.Sprintf("this Host has no Session %q", id),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Events []protocol.Event `json:"events"`
		Cursor protocol.Cursor  `json:"cursor"`
	}{events, at})
}

// page reads where the request wants to read from and how much of it. A size of
// zero would page forever, so the request is clipped at both ends.
func page(r *http.Request) (after uint64, limit int, err error) {
	if after, err = number(r, "after", 0); err != nil {
		return 0, 0, err
	}
	asked, err := number(r, "limit", defaultPage)
	if err != nil {
		return 0, 0, err
	}
	return after, max(1, int(min(asked, maxPage))), nil
}

// number reads one query parameter, or its default when the request omits it.
func number(r *http.Request, name string, missing uint64) (uint64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return missing, nil
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not a number", name, raw)
	}
	return n, nil
}

// SessionView is one row of GET /v1/sessions.
type SessionView struct {
	ID      event.SessionID `json:"id"`
	Harness string          `json:"harness"`
	Model   string          `json:"model"`
	Vendor  string          `json:"vendor"`
	Cwd     string          `json:"cwd"`

	// State is folded from this Session's own Events, never stored.
	State     session.State   `json:"state"`
	EndReason event.EndReason `json:"endReason,omitempty"`

	// StartedAt is Unix microseconds, which is the stamp every other answer uses.
	StartedAt int64 `json:"startedAt"`
}

// refuse answers one of the two kinds of no. The body is a Refusal every time, so
// a Client parses one shape whatever was refused.
func refuse(w http.ResponseWriter, status int, r protocol.Refusal) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(r)
}

func blockingNames(ids []event.SessionID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = string(id)
	}
	return out
}

// Session is one Session on this Host, as the Daemon holds it. There is no state
// field: State is folded from the Events below, because ADR 0008 has every
// transition caused by an Event and none held internally.
type Session struct {
	id      event.SessionID
	harness string
	model   string
	vendor  string
	dir     string
	started time.Time

	// caps is the Harness Adapter's, read once at the start. The Approval Policy is
	// checked against it, and a Session outlives no Adapter, so it cannot go stale.
	caps harness.Capabilities

	// cancel ends the launch, the Run and every call either has in flight.
	cancel context.CancelFunc

	// sink is this Session's whole view of the Daemon, which the Adapter reports
	// through and the Session's end closes. It is made with the Session and never
	// replaced, so it is read without the registry mutex.
	sink *sink

	// run is the live Session the Adapter handed back, which the Daemon holds
	// because it is the Daemon that prompts and stops it. It is nil until the
	// Harness is up, and stays nil for a launch that failed.
	run harness.Run

	// proc is the Harness process, which the Daemon owns and the Adapter never
	// sees. It is nil for a Harness that spawns none, and passthrough is one.
	proc *harnessProcess

	// raw is where that process's bytes are kept. A Session with no process has no
	// transcript, because a transcript records what a Harness said.
	raw *transcript

	// events is this Session's own Events, in Seq order, which is what the fold
	// reads. Deltas are not Events and never land here.
	events []event.Event

	// ending is set by whoever writes SessionEnded, so a stop and a launch that
	// failed cannot both write one and leave the end reason to a race.
	ending bool

	// asking is the questions this Session is holding a Tool Call for, keyed by the
	// tool call id the Harness made at runtime. The Adapter waits on the channel and
	// the decision is what sends to it.
	asking map[string]chan event.Decision
}

// sessions is this Host's Session registry: every Session the Daemon started, in
// start order, live or ended. An ended Session stays, because a stopped Session is
// not deleted and its history stays readable.
//
// It is a slice and not a map keyed by id. One Host runs one Session at a time, so
// the list is short and a scan reads more plainly than a second index would.
//
// The mutex guards the slice and every mutable field of every Session in it, so
// the registry is the one place a Session is written to.
type sessions struct {
	mu  sync.Mutex
	all []*Session
}

func (r *sessions) add(s *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.all = append(r.all, s)
}

// find is one Session as a command needs it, with the Run to act on and the State
// that decides whether it may. The two are read together under the one mutex, so
// they describe the same moment.
func (r *sessions) find(id event.SessionID) (*Session, harness.Run, session.State) {
	r.mu.Lock()
	defer r.mu.Unlock()

	s := r.lookup(id)
	if s == nil {
		return nil, nil, 0
	}
	state, _ := session.Fold(s.events)
	return s, s.run, state
}

// ask registers one open question and hands back what the answer will arrive on.
func (r *sessions) ask(s *Session, id string) chan event.Decision {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s.asking == nil {
		s.asking = map[string]chan event.Decision{}
	}
	answer := make(chan event.Decision, 1)
	s.asking[id] = answer
	return answer
}

// answered forgets one question, whether it was decided or abandoned.
func (r *sessions) answered(s *Session, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(s.asking, id)
}

// tell delivers one decision to the Adapter holding that Tool Call, and never
// waits to. A question nobody is reading is one the Adapter has given up on, and
// a second answer to one question is one answer too many: the Event is the record
// either way, and a send that blocked here would block the whole registry with it.
func (r *sessions) tell(s *Session, id string, decision event.Decision) {
	r.mu.Lock()
	defer r.mu.Unlock()
	answer, ok := s.asking[id]
	if !ok {
		return
	}
	select {
	case answer <- decision:
	default:
	}
}

// kindOf is the ToolKind of one Tool Call this Session requested, which is the
// slot of the Approval Policy that decides it.
func (r *sessions) kindOf(s *Session, id string) (event.ToolKind, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range s.events {
		if e.Kind != event.KindToolCallRequested {
			continue
		}
		if p, ok := e.Payload.(*event.ToolCallRequested); ok && p.ToolCallID == id {
			return p.ToolKind, true
		}
	}
	return 0, false
}

// ruleFor is the Approval Policy slot that applied when this Tool Call was
// requested. A later policy change applies only to later Tool Calls.
func (r *sessions) ruleFor(s *Session, id string) (event.Rule, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, e := range s.events {
		if e.Kind != event.KindToolCallRequested {
			continue
		}
		p, ok := e.Payload.(*event.ToolCallRequested)
		if !ok || p.ToolCallID != id {
			continue
		}
		return session.Policy(s.events[:i+1])[p.ToolKind], true
	}
	return "", false
}

// policy is the Approval Policy this Session holds now, folded from its own Events.
func (r *sessions) policy(s *Session) event.Policy {
	r.mu.Lock()
	defer r.mu.Unlock()
	return session.Policy(s.events)
}

// held is the Tool Calls this Session is waiting on a decision for.
func (r *sessions) held(s *Session) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return session.Held(s.events)
}

func (r *sessions) openCalls(s *Session) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return session.OpenCalls(s.events)
}

// ending reports whether someone has already taken on writing SessionEnded.
func (r *sessions) ending(s *Session) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return s.ending
}

// endOnce reports whether this caller is the one that ends the Session. A stop and
// a launch that failed can both reach the end, and only the first writes it.
func (r *sessions) endOnce(s *Session) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s.ending {
		return false
	}
	s.ending = true
	return true
}

func (r *sessions) setRun(s *Session, run harness.Run) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s.run = run
}

// setProcess keeps the Harness process and its transcript, and reports whether the
// Session still wants them. A stop that landed while the Harness was starting has
// already run its kill and found no process, so the spawn takes the process back
// rather than leaving one nobody owns.
func (r *sessions) setProcess(s *Session, p *harnessProcess, raw *transcript) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s.ending {
		return false
	}
	s.proc, s.raw = p, raw
	return true
}

// process is the Harness process and the transcript its output is going to. They
// are read together because they end together.
func (r *sessions) process(s *Session) (*harnessProcess, *transcript) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return s.proc, s.raw
}

// record keeps one Event against the Session it belongs to, so the fold has
// something to read without going back to the log.
func (r *sessions) record(s *Session, e event.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s.events = append(s.events, e)
}

// live is every Session that has not ended, which is what admission is asked
// about.
func (r *sessions) live() []admission.Live {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []admission.Live
	for _, s := range r.all {
		if state, _ := session.Fold(s.events); state == session.Ended {
			continue
		}
		out = append(out, admission.Live{
			Session: s.id, Harness: s.harness, Model: s.model, Since: s.started,
		})
	}
	return out
}

func (r *sessions) views() []SessionView {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]SessionView, len(r.all))
	for i, s := range r.all {
		state, reason := session.Fold(s.events)
		out[i] = SessionView{
			ID: s.id, Harness: s.harness, Model: s.model, Vendor: s.vendor, Cwd: s.dir,
			State: state, EndReason: reason, StartedAt: s.started.UnixMicro(),
		}
	}
	return out
}

// newID makes a Session id this registry does not already hold. Three random
// bytes collide rarely and the check costs one scan, so the id stays short enough
// to type into a curl.
func (r *sessions) newID() event.SessionID {
	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		var b [3]byte
		rand.Read(b[:])
		id := event.SessionID("s-" + hex.EncodeToString(b[:]))
		if r.lookup(id) == nil {
			return id
		}
	}
}

// lookup is newID's scan. The caller holds the mutex.
func (r *sessions) lookup(id event.SessionID) *Session {
	for _, s := range r.all {
		if s.id == id {
			return s
		}
	}
	return nil
}
