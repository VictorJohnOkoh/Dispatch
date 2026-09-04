package harness

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
)

// Pi is the wire `pi --mode rpc` speaks: LF-delimited JSONL, events out and
// commands in, correlated by an id this Adapter chooses.
//
// The frames it answers are in docs/research/captures/pi-vendors/ for the turn
// and docs/research/captures/pi-gate-dispatch/ for the launch and the Gate. Those
// bytes are this file's specification and the tests replay them.

// dispatchGate is the Gate the Daemon ships, embedded so that it cannot drift
// from the Adapter that speaks its protocol. Pi gates nothing on its own.
//
//go:embed dispatch-gate.js
var dispatchGate []byte

// The Gate's half of the protocol. Both of its frames carry JSON inside a display
// field, because Pi's extension UI has no structured payload.
const (
	gateProtocol = "dispatch.gate/1"
	gateFile     = "dispatch-gate.js"
)

// startProbe is the first command on stdin. Start returns when it is answered, so
// anything the Daemon must know before then has to arrive ahead of that answer.
const startProbe = "start-probe"

// Pi is one Pi Harness, made once at Daemon start and shared by every Session.
type Pi struct {
	name string
	gate string // where the Gate was written, which -e names at every spawn
	caps Capabilities
}

// NewPi writes the Gate into dir and is the Pi Adapter. dir is the Daemon's own
// state directory and never a Workspace, because the Gate is the Daemon's file
// and no Session may reach it.
//
// All five slots gate, which is what captures/pi-gate-dispatch/ measured: every
// ToolKind was held in both the allow run and the deny run, and no tool call
// sailed past. fetch and other are declared although Pi's eight built-in tools
// reach only read, edit and execute, because the Gate's table covers every kind
// and an extension may register a tool in either. A slot that is never asked
// about is a tool that does not exist, not a gate that failed to fire.
func NewPi(dir string) (*Pi, error) {
	gate := filepath.Join(dir, gateFile)
	if err := os.WriteFile(gate, dispatchGate, 0o644); err != nil {
		return nil, fmt.Errorf("pi: the Gate could not be written to %s: %w", gate, err)
	}
	caps := Capabilities{Tools: true}
	for kind := range caps.Gates {
		caps.Gates[kind] = true
	}
	return &Pi{name: "pi", gate: gate, caps: caps}, nil
}

func (a *Pi) Capabilities() Capabilities { return a.caps }

// Start spawns the Harness and sends one command, and returns only once that
// command has been answered by a Pi whose Gate announced itself and whose Model
// is the one this Session asked for.
func (a *Pi) Start(ctx context.Context, spec SessionSpec, out Sink) (Run, error) {
	if out == nil {
		return nil, fmt.Errorf("%s: no Sink", a.name)
	}
	// Files is not required. Pi runs its own tools and delegates no write, so the
	// Workspace Root is the only bound on one and the Daemon holds that already.
	if spec.Spawn == nil {
		return nil, fmt.Errorf("%s: this Harness needs a Spawner", a.name)
	}
	// Only the Model is named. Pi's providers live in a Host-level models.json
	// written once at Host setup, and what that file called this Vendor is the
	// Host's business: the launch reads back which one answered rather than
	// spelling a name this repo would be guessing at.
	pipes, err := spec.Spawn(ctx, Launch{Args: []string{
		"--mode", "rpc",
		"--model", spec.Model,
		"-e", a.gate,
	}})
	if err != nil {
		return nil, err
	}

	r := &piRun{
		name:    a.name,
		gate:    a.gate,
		session: ctx,
		spec:    spec,
		out:     out,
		in:      pipes.In,
		calls:   map[string]*piCall{},
		stopped: make(chan struct{}),
		gone:    make(chan struct{}),
	}
	go r.read(pipes.Out)

	if err := r.checkLaunch(ctx); err != nil {
		r.stop()
		return nil, err
	}
	return r, nil
}

// piRun is one live Session. Everything the Harness says arrives on the one
// reader goroutine, so the Sink is called from one goroutine and needs no lock.
type piRun struct {
	name    string
	gate    string // the file -e named, which is the only extension that is a Gate
	session context.Context
	spec    SessionSpec
	out     Sink
	in      io.Writer

	stopped chan struct{} // Close or a failed Start has run
	gone    chan struct{} // the Harness's stdout reached its end

	writing sync.Mutex // stdin, and its own lock, as in acp.go

	mu   sync.Mutex
	done bool // Close has run, so nothing more is reported

	// The launch, as the reader fills it in. All three are read once, when the
	// start probe is answered, because that is where Start's decision is made.
	ready       bool         // the Gate announced itself
	gateFailure string       // an extension loaded and then threw
	probe       chan piFrame // whoever is waiting for the start probe's answer

	prompts uint64 // how many Prompts this Session has sent
	prompt  string // the id of the Prompt in flight, or empty

	// The Prompt in flight, as its turns account for it. Pi accounts per turn and
	// a Prompt may take several, so the Event carries their sum.
	stopReason event.StopReason
	usage      event.Usage

	openKind event.Kind // the appendable Event with text still arriving

	// calls is every Tool Call of the Prompt in flight, keyed by the toolCallId Pi
	// carries on every frame about one. pending is the ones not yet announced,
	// oldest first.
	calls   map[string]*piCall
	pending []string
}

// piCall is one Tool Call, as much of it as the Harness has said so far.
type piCall struct {
	name      string
	args      json.RawMessage
	announced bool
}

// checkLaunch sends one command and reads the launch off the frames that arrive
// before its answer. Pi has no handshake of its own, so the command is get_state,
// whose answer says which Model and which Vendor this Session actually got.
func (r *piRun) checkLaunch(ctx context.Context) error {
	answered := make(chan piFrame, 1)
	r.mu.Lock()
	r.probe = answered
	r.mu.Unlock()

	if err := r.write(command{ID: startProbe, Type: "get_state"}); err != nil {
		return err
	}
	select {
	case got := <-answered:
		return r.accept(got)
	case <-r.gone:
		// The reader sends the probe answer before it closes gone. Both may be
		// ready before this goroutine runs, so keep the answer that arrived first.
		select {
		case got := <-answered:
			return r.accept(got)
		default:
		}
		// Pi exits 1 on an extension that does not parse, before it answers anything.
		return fmt.Errorf("%s: the Harness ended before it answered the start probe", r.name)
	case <-r.stopped:
		return fmt.Errorf("%s: the Session ended before the start probe was answered", r.name)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// accept is the launch decision. A Session whose Gate did not load would run
// ungated, so it is refused rather than started, which is what ADR 0008 asks of a
// Gate that depends on a loadable component.
func (r *piRun) accept(got piFrame) error {
	r.mu.Lock()
	failure, ready := r.gateFailure, r.ready
	r.mu.Unlock()

	if failure != "" {
		return fmt.Errorf("%s: the Gate would not load: %s", r.name, failure)
	}
	if !ready {
		return fmt.Errorf("%s: the Gate never announced itself, so this Session would run with no gate at all", r.name)
	}
	if !got.Success {
		return fmt.Errorf("%s: the Harness refused the start probe: %s", r.name, got.Error)
	}

	var state struct {
		Model struct {
			ID       string `json:"id"`
			Provider string `json:"provider"`
			BaseURL  string `json:"baseUrl"`
		} `json:"model"`
	}
	if err := json.Unmarshal(got.Data, &state); err != nil {
		return fmt.Errorf("%s: the answer to the start probe could not be read: %w", r.name, err)
	}
	if state.Model.ID != r.spec.Model {
		return fmt.Errorf("%s: this Session asked for %s and the Harness selected %s",
			r.name, r.spec.Model, state.Model.ID)
	}
	// The Vendor is checked as well as the Model, because a Host's models.json may
	// name one Model under two providers and only one of them is this Session's.
	if !sameEndpoint(state.Model.BaseURL, r.spec.Vendor.Base) {
		return fmt.Errorf("%s: this Session's Vendor is %s and the Harness selected %s at %s",
			r.name, r.spec.Vendor.Base, state.Model.Provider, state.Model.BaseURL)
	}
	return nil
}

// sameEndpoint is whether two addresses are the same Vendor. The scheme and the
// host are compared and the path is not, because the path is how the Host's
// models.json was written and not a fact about the Vendor. The two are parsed
// rather than matched as text: 127.0.0.1:12345 starts with 127.0.0.1:1234 and is
// another Vendor.
func sameEndpoint(reported, want string) bool {
	got, err := url.Parse(reported)
	if err != nil {
		return false
	}
	asked, err := url.Parse(want)
	if err != nil {
		return false
	}
	return strings.EqualFold(got.Scheme, asked.Scheme) && strings.EqualFold(got.Host, asked.Host)
}

// Prompt sends the Prompt and returns once the Harness has taken it. The Prompt
// completes on the Sink, when the agent settles.
func (r *piRun) Prompt(ctx context.Context, text string) error {
	r.mu.Lock()
	if r.done {
		r.mu.Unlock()
		return fmt.Errorf("%s: the Session is closed", r.name)
	}
	if r.prompt != "" {
		r.mu.Unlock()
		return fmt.Errorf("%s: a Prompt is already in flight", r.name)
	}
	r.prompts++
	id := fmt.Sprintf("prompt-%d", r.prompts)
	r.prompt = id
	r.stopReason, r.usage = "", event.Usage{}
	r.calls, r.pending = map[string]*piCall{}, nil
	r.mu.Unlock()

	// The field is message and not prompt. Sending prompt is answered with an
	// undefined-property crash rather than with a schema error, so this one is
	// spelled from the capture.
	err := r.write(command{ID: id, Type: "prompt", Message: text})
	if err != nil {
		r.mu.Lock()
		r.prompt = ""
		r.mu.Unlock()
	}
	return err
}

// Interrupt abandons the Prompt in flight, which Pi answers by ending the turn,
// so the Prompt is still bounded by agent_settled as it always is. No captured
// run ever aborted one, so what Pi does with this is not established here.
func (r *piRun) Interrupt(context.Context) error {
	r.mu.Lock()
	prompting := r.prompt != ""
	r.mu.Unlock()
	if !prompting {
		return nil
	}
	return r.write(command{Type: "abort"})
}

// Close stops reporting. Pi's protocol has no goodbye, so nothing is sent: the
// ladder's next step closes stdin, and that is the signal Pi answers by exiting.
//
// The reader goroutine keeps draining stdout, because a full pipe stops a Harness
// that is not dead yet.
func (r *piRun) Close() error {
	r.stop()
	return nil
}

func (r *piRun) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return
	}
	r.done = true
	close(r.stopped)
}

// read is the one goroutine that turns what the Harness says into Sink calls. It
// runs until stdout ends, which is the process going away, so a Session that has
// been closed still drains.
func (r *piRun) read(out io.Reader) {
	defer close(r.gone)

	// Strict JSONL with LF as the sole delimiter, which is what ScanLines is: it
	// splits on \n and strips a trailing \r. A splitter that also honoured the
	// Unicode line separators would cut a frame in half.
	lines := bufio.NewScanner(out)
	lines.Buffer(make([]byte, 0, 64<<10), frameLimit)
	for lines.Scan() {
		line := lines.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var f piFrame
		if err := json.Unmarshal(line, &f); err != nil {
			r.report(func() { r.out.Failed(event.ErrAdapterFailed, "the Harness wrote a line that is not a frame") })
			continue
		}
		r.dispatch(line, f)
	}
	if err := lines.Err(); err != nil {
		r.report(func() {
			r.out.Failed(event.ErrAdapterFailed, "the Harness output could not be read: "+err.Error())
		})
	}
}

// report runs one Sink call unless the Session has been closed, holding the lock
// across it for the reason acp.go's report gives.
func (r *piRun) report(call func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.done {
		call()
	}
}

// piFrame is one line, whichever of Pi's frames it is. A field that is absent on
// any one shape stays zero, which is what lets one struct read them all.
type piFrame struct {
	Type string `json:"type"`
	ID   string `json:"id"`

	// response, and the error text of an extension_error with the file it came from
	Success       bool            `json:"success"`
	Error         string          `json:"error"`
	Data          json.RawMessage `json:"data"`
	ExtensionPath string          `json:"extensionPath"`

	// extension_ui_request. The Gate's payload is in whichever display field the
	// method has: message on notify and title on select. Message is raw because Pi
	// spells a notify's display string and a turn's whole message with the same
	// key, and only one of the two is a string.
	Method  string          `json:"method"`
	Message json.RawMessage `json:"message"`
	Title   string          `json:"title"`

	// message_update, which is every streamed delta
	Delta piDelta `json:"assistantMessageEvent"`

	// tool_execution_start, _update and _end
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Args       json.RawMessage `json:"args"`
	Result     piResult        `json:"result"`
	IsError    bool            `json:"isError"`
}

// piTurn is turn_end's account of the turn. It is read from the same line as the
// frame and not from a field on it, because Pi spells this "message" and spells
// the display string of a notify "message" too.
type piTurn struct {
	Message struct {
		StopReason string `json:"stopReason"`
		Usage      struct {
			Input     int `json:"input"`
			Output    int `json:"output"`
			CacheRead int `json:"cacheRead"`
			Total     int `json:"totalTokens"`
		} `json:"usage"`
	} `json:"message"`
}

// piDelta is one piece of the assistant's message as it is written.
type piDelta struct {
	Type     string `json:"type"`
	Delta    string `json:"delta"`
	ToolCall struct {
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"toolCall"`
}

type piResult struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

func (r *piRun) dispatch(line []byte, f piFrame) {
	switch f.Type {
	case "response":
		r.answered(f)

	case "extension_ui_request":
		r.dialog(f)

	case "extension_error":
		r.extensionFailed(f)

	case "message_update":
		r.delta(f.Delta)

	case "tool_execution_start":
		// Not evidence that anything ran: Pi announces a tool as started and then
		// waits for the Gate. It is the announcement for a call the message stream
		// never carried, and it repeats one it did.
		r.report(func() {
			r.hold(f.ToolCallID, f.ToolName, f.Args)
			r.announce(f.ToolCallID)
		})

	case "tool_execution_update":
		// Streaming tool output, dropped. Pi sends it and no other Harness does, so
		// ADR 0005 gave it no Kind. The raw bytes are in the Session's transcript.

	case "tool_execution_end":
		r.toolEnded(f)

	case "turn_end":
		r.turnEnded(line)

	case "agent_settled":
		r.completed()

	default:
		// agent_start, session, message_start, agent_end and the rest. agent_end
		// repeats the whole conversation, which the log holds already. A frame this
		// Adapter has no Event for is dropped rather than guessed at.
	}
}

// answered hands the start probe to whoever is waiting for it, and bounds a
// Prompt the Harness would not take.
func (r *piRun) answered(f piFrame) {
	r.mu.Lock()
	if f.ID == startProbe && r.probe != nil {
		waiting := r.probe
		r.probe = nil
		r.mu.Unlock()
		waiting <- f
		return
	}
	refused := f.ID != "" && f.ID == r.prompt && !f.Success
	if refused {
		r.prompt = ""
	}
	r.mu.Unlock()

	if refused {
		r.report(func() {
			r.closeOpen()
			r.announceAll()
			r.out.Failed(event.ErrAdapterFailed, "the Harness would not take this Prompt: "+f.Error)
			r.out.Completed(event.StopError, event.Usage{})
		})
	}
}

// extensionFailed records a Gate that loaded and then threw. Before the start
// probe is answered this fails the launch; after it the Gate is already known to
// have announced, so a later failure is reported and the Daemon decides.
//
// Only the file this Adapter loaded is the Gate. A Host may load extensions of
// its own, and calling one of those a Gate failure would be reporting something
// that did not happen. Such a failure during the launch is left alone: if it did
// stop the Gate announcing, the launch still fails, and it fails saying that.
func (r *piRun) extensionFailed(f piFrame) {
	r.mu.Lock()
	launching := r.probe != nil
	mine := filepath.Clean(f.ExtensionPath) == filepath.Clean(r.gate)
	if launching && mine {
		r.gateFailure = f.Error
	}
	r.mu.Unlock()
	if !launching {
		r.report(func() { r.out.Failed(event.ErrAdapterFailed, "an extension failed: "+f.Error) })
	}
}

// gateFrame is what the Gate puts in a display field, on both of its frames.
type gateFrame struct {
	Protocol   string `json:"protocol"`
	Event      string `json:"event"`
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
}

// dialog answers Pi's extension UI. The Gate's two frames are the announcement
// and the question; any other blocking dialog is cancelled, because one that is
// never answered is a Harness that waits forever.
func (r *piRun) dialog(f piFrame) {
	switch f.Method {
	case "notify":
		if said, ok := gateSaid(display(f.Message)); ok && said.Event == "ready" {
			r.mu.Lock()
			r.ready = true
			r.mu.Unlock()
		}

	case "select":
		said, ok := gateSaid(f.Title)
		if !ok || said.Event != "request" {
			r.write(command{Type: "extension_ui_response", ID: f.ID, Cancelled: true})
			return
		}
		r.ask(f.ID, said)

	case "confirm", "input", "editor":
		r.write(command{Type: "extension_ui_response", ID: f.ID, Cancelled: true})

	default:
		// notify's siblings setStatus, setWidget and setTitle are fire-and-forget,
		// and nothing is waiting on one.
	}
}

// ask puts the Gate's question to the Daemon and answers Pi with the decision.
// The reader waits with it, which is what the Harness is doing too.
func (r *piRun) ask(dialog string, said gateFrame) {
	var detail string
	r.report(func() {
		if call := r.calls[said.ToolCallID]; call != nil {
			detail = string(call.args)
		}
		r.announce(said.ToolCallID)
	})

	decision, err := r.out.Approve(r.session, said.ToolCallID, said.ToolName, detail)
	// A Gate that fails open is not a Gate, so a question nobody could answer is a
	// denial rather than a cancelled dialog.
	value := "allow"
	if err != nil || decision == event.DecisionRefused {
		value = "deny"
	}
	r.write(command{Type: "extension_ui_response", ID: dialog, Value: value})
}

func (r *piRun) delta(d piDelta) {
	switch d.Type {
	case "thinking_delta":
		r.report(func() { r.text(event.KindReasoning, d.Delta) })
	case "text_delta":
		r.report(func() { r.text(event.KindAssistantMessage, d.Delta) })

	case "thinking_end":
		r.report(func() { r.closeIf(event.KindReasoning) })
	case "text_end":
		r.report(func() { r.closeIf(event.KindAssistantMessage) })

	case "toolcall_end":
		// The whole call, arguments and all, as soon as the model has finished
		// writing it and before anything has tried to run it.
		r.report(func() {
			r.hold(d.ToolCall.ID, d.ToolCall.Name, d.ToolCall.Arguments)
			r.announce(d.ToolCall.ID)
		})

	default:
		// thinking_start, text_start and toolcall_start open a block whose content
		// arrives on the deltas after them, and a toolcall_delta is a fragment of
		// JSON that is only valid once toolcall_end has assembled it.
	}
}

// toolEnded reports what the Harness said happened. A denial arrives here as an
// error carrying the Gate's own words, and it stays an error: refused is the
// Daemon's word for its own decision and an Adapter may not borrow it.
func (r *piRun) toolEnded(f piFrame) {
	outcome := event.OutcomeOK
	if f.IsError {
		outcome = event.OutcomeError
	}
	r.report(func() {
		r.hold(f.ToolCallID, f.ToolName, nil)
		r.announce(f.ToolCallID)
		r.out.ToolCallEnded(f.ToolCallID, outcome, resultText(f.Result))
	})
}

// turnEnded takes the turn's account of itself. A Prompt may take several turns,
// so the usage is summed and the stop reason is the last turn's.
func (r *piRun) turnEnded(line []byte) {
	var turn piTurn
	if json.Unmarshal(line, &turn) != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopReason = event.StopReason(turn.Message.StopReason)
	r.usage.Input += turn.Message.Usage.Input
	r.usage.Output += turn.Message.Usage.Output
	r.usage.CacheRead += turn.Message.Usage.CacheRead
	r.usage.Total += turn.Message.Usage.Total
}

// completed bounds the Prompt on agent_settled, which is the agent finished and
// not retrying. agent_end is not it: an auto-retry sends one and then carries on.
//
// An open message is closed first, because a Session that stays usable may not
// leave one open: the Cursor sits below every open Event, so it would never move
// again.
func (r *piRun) completed() {
	r.mu.Lock()
	if r.prompt == "" || r.done {
		r.mu.Unlock()
		return
	}
	r.prompt = ""
	stop, used := r.stopReason, r.usage
	r.mu.Unlock()

	r.report(func() {
		r.closeOpen()
		r.announceAll()
		// Bounded either way, and on this project's own word rather than on an empty
		// one attributed to the Vendor. See completed in acp.go.
		if stop == "" {
			r.out.Failed(event.ErrAdapterFailed, "this Prompt settled with no turn to say why it stopped")
			r.out.Completed(event.StopError, used)
			return
		}
		r.out.Completed(stop, used)
	})
}

// hold keeps a Tool Call until there is something worth reporting about it. The
// first frame to name a call is the one carrying its arguments. The caller holds
// the mutex.
func (r *piRun) hold(id, name string, args json.RawMessage) {
	if id == "" || r.calls[id] != nil {
		return
	}
	r.calls[id] = &piCall{name: name, args: args}
	r.pending = append(r.pending, id)
}

// announce writes the Tool Call the Harness named, once, and before anything that
// refers to it. Whatever is still pending when the Prompt completes is written
// then, because the Daemon is about to close it and a call cannot end before it
// was requested. The caller holds the mutex.
func (r *piRun) announce(id string) {
	call := r.calls[id]
	if call == nil || call.announced {
		return
	}
	call.announced = true
	for i, waiting := range r.pending {
		if waiting == id {
			r.pending = append(r.pending[:i], r.pending[i+1:]...)
			break
		}
	}
	r.closeOpen()
	// Pi supplies no display string of its own, so the title is the tool's name.
	r.out.ToolCallRequested(id, call.name, toolKindOf(call.name), call.name, call.args)
}

// announceAll writes every Tool Call still pending, oldest first. The caller
// holds the mutex.
func (r *piRun) announceAll() {
	for len(r.pending) > 0 {
		r.announce(r.pending[0])
	}
}

// text adds to the open message. The Sink closes the open Event when the other
// kind arrives, so only the kind has to be tracked here.
func (r *piRun) text(kind event.Kind, chunk string) {
	r.openKind = kind
	if kind == event.KindReasoning {
		r.out.Reasoning(chunk, false)
		return
	}
	r.out.Message(chunk, false)
}

func (r *piRun) closeIf(kind event.Kind) {
	if r.openKind == kind {
		r.closeOpen()
	}
}

func (r *piRun) closeOpen() {
	r.openKind = closeOpen(r.out, r.openKind)
}

// display is a raw field read as the string a dialog shows. A frame whose field
// of that name is an object is not a dialog, and is not read as one.
func display(raw json.RawMessage) string {
	var shown string
	if json.Unmarshal(raw, &shown) != nil {
		return ""
	}
	return shown
}

// gateSaid reads the JSON the Gate put in a display field. A dialog raised by
// somebody else's extension is not one, and is not treated as one.
func gateSaid(display string) (gateFrame, bool) {
	var said gateFrame
	if json.Unmarshal([]byte(display), &said) != nil {
		return gateFrame{}, false
	}
	return said, said.Protocol == gateProtocol
}

// toolKindOf maps Pi's tool names onto the five, and it is the Gate's own table.
// The two must agree: the Gate names the kind it held and the Daemon gates the
// kind this returns, so a name in one and not the other would gate one slot and
// report another.
func toolKindOf(name string) event.ToolKind {
	switch name {
	case "read", "grep", "find", "ls":
		return event.ToolRead
	case "edit", "write":
		return event.ToolEdit
	case "bash", "powershell":
		return event.ToolExecute
	case "fetch":
		return event.ToolFetch
	default:
		return event.ToolOther
	}
}

func resultText(result piResult) string {
	var said strings.Builder
	for _, block := range result.Content {
		said.WriteString(block.Text)
	}
	return said.String()
}

// command is one line this Adapter writes. The id is echoed back on the response
// that answers it, which is how a command and its answer are correlated.
type command struct {
	ID        string `json:"id,omitempty"`
	Type      string `json:"type"`
	Message   string `json:"message,omitempty"`
	Value     string `json:"value,omitempty"`
	Cancelled bool   `json:"cancelled,omitempty"`
}

func (r *piRun) write(c command) error {
	line, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("%s: %w", r.name, err)
	}
	r.writing.Lock()
	defer r.writing.Unlock()
	if _, err := r.in.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("%s: the Harness would not take a command: %w", r.name, err)
	}
	return nil
}
