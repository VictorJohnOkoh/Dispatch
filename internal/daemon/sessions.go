package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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

	adapter := d.harness(req.Harness)
	if adapter == nil {
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
		model:   req.Model,
		vendor:  vendor.Endpoint().Base,
		dir:     dir,
		started: time.Now().UTC(),
		cancel:  cancel,
	}
	// The Session joins the registry only once its first Event is in the log. A
	// Session with no Event folds to Starting, so one added ahead of a write that
	// failed would hold the Host's one slot for as long as the Daemon runs.
	started, err := d.write(s, event.KindSessionStarted, &event.SessionStarted{
		Harness: s.harness, Model: s.model, Vendor: s.vendor, Cwd: s.dir,
	})
	if err != nil {
		http.Error(w, "the Event log refused the write", http.StatusInternalServerError)
		return
	}
	d.sessions.add(s)

	go d.launch(ctx, s, adapter, vendor)

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
func (d *Daemon) launch(ctx context.Context, s *Session, adapter harness.Adapter, vendor vendors.Adapter) {
	if err := vendor.Load(ctx, s.model); err != nil {
		d.endFailed(s, event.ErrVendor, fmt.Sprintf("the Model %s would not load: %v", s.model, err))
		return
	}

	// Spawn and Files are not filled: passthrough calls neither, and the process
	// supervisor and the contained file access they name have not landed.
	run, err := adapter.Start(ctx, harness.SessionSpec{
		Session: s.id,
		Model:   s.model,
		Vendor:  vendor.Endpoint(),
		Dir:     s.dir,
	}, &sink{d: d, s: s})
	if err != nil {
		d.endFailed(s, event.ErrHarnessFailed, err.Error())
		return
	}
	d.sessions.setRun(s, run)

	d.write(s, event.KindSessionReady, &event.SessionReady{Model: s.model})
}

// endFailed is a launch that did not finish. SessionStarted is already in the log,
// so the record exists and the Session does not, and the pair of Events is what
// says so.
func (d *Daemon) endFailed(s *Session, code event.ErrorCode, msg string) {
	d.write(s, event.KindError, &event.Error{Code: code, Message: msg})
	d.write(s, event.KindSessionEnded, &event.SessionEnded{Reason: event.EndFailed})
	s.cancel()
}

// listSessions answers with this Host's Sessions, live and ended, in start order.
func (d *Daemon) listSessions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Sessions []SessionView `json:"sessions"`
	}{d.sessions.views()})
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

	// cancel ends the launch, the Run and every call either has in flight.
	cancel context.CancelFunc

	// run is the live Session the Adapter handed back, which the Daemon holds
	// because it is the Daemon that prompts and stops it. It is nil until the
	// Harness is up, and stays nil for a launch that failed.
	run harness.Run

	// events is this Session's own Events, in Seq order, which is what the fold
	// reads. Deltas are not Events and never land here.
	events []event.Event
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

func (r *sessions) setRun(s *Session, run harness.Run) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s.run = run
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

// known reports whether this Host ever started a Session by that id, ended or not.
func (r *sessions) known(id event.SessionID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lookup(id) != nil
}

// lookup is the registry's scan. The caller holds the mutex.
func (r *sessions) lookup(id event.SessionID) *Session {
	for _, s := range r.all {
		if s.id == id {
			return s
		}
	}
	return nil
}
