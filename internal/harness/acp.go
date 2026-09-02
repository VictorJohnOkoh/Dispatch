package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
)

// ACP is the wire OpenCode speaks: JSON-RPC 2.0, one object per line, in both
// directions. This Adapter is the protocol and not the Harness, so a second ACP
// Harness costs a Capabilities value and a Launch and no code at all.
//
// The frames it answers, and nothing else, are in
// docs/research/captures/opencode/. Those bytes are this file's specification and
// the tests replay them.

// providerKey is the name of the provider this Adapter writes into the Session's
// own opencode.json, and so the first half of the Model id OpenCode reports back.
// It is the name the captures were recorded under, and changing it means
// recapturing them.
const providerKey = "capstone"

// configFile is the per-Session config, written in the working directory. It
// merges with the user's global config rather than replacing it, so it makes this
// Session's Vendor reachable and hides nothing else.
const configFile = "opencode.json"

// frameLimit is the longest line this Adapter will read. A permission request
// carries the whole diff of an edit, so the limit is generous, and a Harness that
// writes more than this has stopped making sense.
const frameLimit = 8 << 20

// ACP is one ACP Harness, made once at Daemon start and shared by every Session.
type ACP struct {
	name string
	args []string
	caps Capabilities
}

// NewOpenCode is the OpenCode Adapter. Its Gates are declared and not read out of
// the Harness: OpenCode's own initialize advertises loadSession, MCP and four
// session capabilities and says nothing at all about gating.
//
// read is not among them. The capture counts two reads started, two ended and
// none asked, and the read never crosses the client seam either, so there is
// nothing to hold. The Workspace Root is the only bound on a read.
//
// fetch is not among them either, and that is the cautious answer rather than the
// measured one. OpenCode's permission block covers webfetch and the config this
// Adapter writes asks it to, but no capture ever exercised one. A Gate declared
// and then silent is a slot that says wait and behaves like auto, which is the one
// lie this project cannot afford, so it stays false until a capture settles it.
func NewOpenCode() *ACP {
	caps := Capabilities{Tools: true}
	caps.Gates[event.ToolEdit] = true
	caps.Gates[event.ToolExecute] = true
	return &ACP{name: "opencode", args: []string{"acp"}, caps: caps}
}

func (a *ACP) Capabilities() Capabilities { return a.caps }

// Start spawns the Harness, runs the handshake and opens one ACP session. It
// returns only once the Harness has reported that it is running spec.Model, so a
// Session on a Model nobody asked for never exists.
func (a *ACP) Start(ctx context.Context, spec SessionSpec, out Sink) (Run, error) {
	if out == nil {
		return nil, fmt.Errorf("%s: no Sink", a.name)
	}
	if spec.Spawn == nil || spec.Files == nil {
		return nil, fmt.Errorf("%s: this Harness needs both a Spawner and contained file access", a.name)
	}
	// The config is written before the spawn. OpenCode reads it from the working
	// directory as it starts, so a file written afterwards is a file it never saw.
	if err := spec.Files.WriteTextFile(configFile, sessionConfig(spec)); err != nil {
		return nil, fmt.Errorf("%s: the Session's %s could not be written: %w", a.name, configFile, err)
	}

	pipes, err := spec.Spawn(ctx, Launch{Args: a.args})
	if err != nil {
		return nil, err
	}

	r := &acpRun{
		name:    a.name,
		session: ctx,
		spec:    spec,
		out:     out,
		in:      pipes.In,
		held:    map[string]sessionUpdate{},
		stopped: make(chan struct{}),
	}
	go r.read(pipes.Out)

	if err := r.handshake(ctx); err != nil {
		r.stop()
		return nil, err
	}
	return r, nil
}

// sessionConfig is the Vendor this Session runs against, as OpenCode spells a
// provider. A config file makes a Vendor reachable; it does not select the Model,
// which is why Start reads the selection back.
func sessionConfig(spec SessionSpec) string {
	raw, _ := json.MarshalIndent(opencodeConfig{
		Schema: "https://opencode.ai/config.json",
		Model:  qualified(spec.Model),
		Provider: map[string]providerConfig{providerKey: {
			Name:   "Dispatch Vendor",
			NPM:    "@ai-sdk/openai-compatible",
			Option: providerOptions{BaseURL: spec.Vendor.Base + "/v1", APIKey: "not-required-for-a-local-vendor"},
			Models: map[string]modelConfig{spec.Model: {Name: spec.Model}},
		}},
	}, "", "  ")
	return string(raw) + "\n"
}

// The shape of opencode.json, cut down to the keys this Session needs. The two
// maps are keyed at runtime, by the provider this Adapter names and by the Model
// the Session asked for.
type opencodeConfig struct {
	Schema   string                    `json:"$schema"`
	Model    string                    `json:"model"`
	Provider map[string]providerConfig `json:"provider"`
}

type providerConfig struct {
	Name   string                 `json:"name"`
	NPM    string                 `json:"npm"`
	Option providerOptions        `json:"options"`
	Models map[string]modelConfig `json:"models"`
}

type providerOptions struct {
	BaseURL string `json:"baseURL"`
	APIKey  string `json:"apiKey"`
}

type modelConfig struct {
	Name string `json:"name"`
}

// qualified is a Model id as OpenCode names it: the provider, then the id the
// Vendor spells.
func qualified(model string) string { return providerKey + "/" + model }

// acpRun is one live Session. Everything the Harness says arrives on the one
// reader goroutine, so the Sink is called from one goroutine and needs no lock.
type acpRun struct {
	name    string
	session context.Context
	spec    SessionSpec
	out     Sink
	in      io.Writer

	id      string // the Harness's own session id
	stopped chan struct{}

	// writing is stdin, and it is its own lock. A Harness that stops reading blocks
	// whoever is writing to it, and everything below would be stuck behind that.
	writing sync.Mutex

	mu     sync.Mutex
	next   uint64 // the next JSON-RPC id
	prompt uint64 // the Prompt in flight, or 0
	done   bool   // Close has run, so nothing more is reported

	// waiting is the one request whose answer somebody is holding on for. Only the
	// handshake waits, and it waits for one at a time, so this is a field and not a
	// table of them.
	waiting   chan answer
	waitingID uint64

	openKind event.Kind // the appendable Event with text still arriving
	openID   string     // the Harness's id for that message

	// held is the Tool Calls the Harness has announced and not yet said anything
	// about. It is keyed by tool call id, which the Harness makes at runtime, and it
	// empties as each call is reported.
	held map[string]sessionUpdate
}

// answer is one response, as the caller of call reads it.
type answer struct {
	result json.RawMessage
	failed string
}

// handshake is initialize then session/new, and then the one check that matters:
// the Model OpenCode says it will use against the Model the Session asked for.
func (r *acpRun) handshake(ctx context.Context) error {
	var hello struct {
		ProtocolVersion int `json:"protocolVersion"`
	}
	if err := r.call(ctx, "initialize", initializeParams(), &hello); err != nil {
		return err
	}
	if hello.ProtocolVersion != protocolVersion {
		return fmt.Errorf("%s: this Harness speaks ACP %d and this Adapter speaks %d",
			r.name, hello.ProtocolVersion, protocolVersion)
	}

	var opened struct {
		SessionID     string `json:"sessionId"`
		ConfigOptions []struct {
			ID           string `json:"id"`
			CurrentValue string `json:"currentValue"`
		} `json:"configOptions"`
	}
	if err := r.call(ctx, "session/new", map[string]any{
		"cwd":        r.spec.Dir,
		"mcpServers": []any{},
	}, &opened); err != nil {
		return err
	}
	if opened.SessionID == "" {
		return fmt.Errorf("%s: the Harness opened a session with no id", r.name)
	}
	r.id = opened.SessionID

	// The config key works and is not authoritative. One captured run wrote
	// capstone/qwen3.5:9b and was answered opencode/big-pickle, a hosted model on
	// somebody else's endpoint, so what session/new reports is the only fact here.
	for _, option := range opened.ConfigOptions {
		if option.ID != "model" {
			continue
		}
		if option.CurrentValue != qualified(r.spec.Model) {
			return fmt.Errorf("%s: this Session asked for %s and the Harness selected %s",
				r.name, qualified(r.spec.Model), option.CurrentValue)
		}
		return nil
	}
	return fmt.Errorf("%s: the Harness did not report which Model it selected", r.name)
}

const protocolVersion = 1

// initializeParams says what this client can do. writeTextFile is offered because
// the Daemon's contained file access is exactly that one call; readTextFile is
// not, and OpenCode never asked for it in any captured run.
func initializeParams() map[string]any {
	return map[string]any{
		"protocolVersion": protocolVersion,
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": false, "writeTextFile": true},
			"terminal": false,
		},
		"clientInfo": map[string]any{"name": "dispatch", "version": "1"},
	}
}

// Prompt sends the Prompt and returns once the Harness has taken it. The Prompt
// completes on the Sink, when the answer to this request arrives.
func (r *acpRun) Prompt(ctx context.Context, text string) error {
	r.mu.Lock()
	if r.done {
		r.mu.Unlock()
		return fmt.Errorf("%s: the Session is closed", r.name)
	}
	if r.prompt != 0 {
		r.mu.Unlock()
		return fmt.Errorf("%s: a Prompt is already in flight", r.name)
	}
	id := r.nextID()
	r.prompt = id
	r.mu.Unlock()

	err := r.write(request{JSONRPC: "2.0", ID: &id, Method: "session/prompt", Params: map[string]any{
		"sessionId": r.id,
		"prompt":    []any{map[string]any{"type": "text", "text": text}},
	}})
	if err != nil {
		r.mu.Lock()
		r.prompt = 0
		r.mu.Unlock()
	}
	return err
}

// Interrupt abandons the Prompt in flight. ACP cancels with a notification and
// the Harness ends the turn itself, so the Prompt is bounded by the answer to
// session/prompt as it always is.
//
// No captured run ever cancelled one, so what OpenCode does with this is written
// down nowhere and is not established here.
func (r *acpRun) Interrupt(context.Context) error {
	r.mu.Lock()
	prompting := r.prompt != 0
	r.mu.Unlock()
	if !prompting {
		return nil
	}
	return r.write(request{JSONRPC: "2.0", Method: "session/cancel", Params: map[string]any{
		"sessionId": r.id,
	}})
}

// Close says the protocol's own goodbye and stops reporting. The answer to it is
// not waited for: the ladder's next step closes stdin and the one after that kills
// the group, so a Harness that will not say goodbye is already handled.
//
// The reader goroutine keeps draining stdout, because a full pipe stops a Harness
// that is not dead yet.
func (r *acpRun) Close() error {
	r.mu.Lock()
	if r.done {
		r.mu.Unlock()
		return nil
	}
	id := r.nextID()
	r.mu.Unlock()

	err := r.write(request{JSONRPC: "2.0", ID: &id, Method: "session/close", Params: map[string]any{
		"sessionId": r.id,
	}})
	r.stop()
	return err
}

// stop makes the Session report nothing more and frees whatever is waiting on an
// answer that is not coming.
func (r *acpRun) stop() {
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
// said goodbye still drains.
func (r *acpRun) read(out io.Reader) {
	lines := bufio.NewScanner(out)
	lines.Buffer(make([]byte, 0, 64<<10), frameLimit)
	for lines.Scan() {
		line := lines.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var f frame
		if err := json.Unmarshal(line, &f); err != nil {
			r.report(func() { r.out.Failed(event.ErrAdapterFailed, "the Harness wrote a line that is not a frame") })
			continue
		}
		r.dispatch(f)
	}
}

// report runs one Sink call unless the Session has said goodbye. Everything after
// Close is the Harness talking to nobody.
func (r *acpRun) report(call func()) {
	// The lock is held across the call, so a Close landing beside it cannot let one
	// Sink call through behind the goodbye. Nothing the Sink does comes back here.
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.done {
		call()
	}
}

// frame is one line, whichever of the three JSON-RPC shapes it is.
type frame struct {
	ID     *uint64         `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (r *acpRun) dispatch(f frame) {
	if f.Method == "" && f.ID != nil {
		r.answered(*f.ID, f)
		return
	}
	switch f.Method {
	case "session/update":
		r.update(f.Params)
	case "session/request_permission":
		r.permission(f)
	case "fs/write_text_file":
		r.writeFile(f)
	default:
		// A Harness may say things this Adapter has no Event for. A notification is
		// dropped; a request is answered, because one that is not answered is a
		// Harness that waits forever.
		if f.ID != nil {
			r.fail(*f.ID, -32601, "this client does not serve "+f.Method)
		}
	}
}

// answered hands one response to whoever is waiting for it. The Prompt is the one
// request whose answer is a fact rather than a return value.
func (r *acpRun) answered(id uint64, f frame) {
	r.mu.Lock()
	held := r.waiting
	waiting := held != nil && r.waitingID == id
	if waiting {
		r.waiting = nil
	}
	prompting := r.prompt == id
	if prompting {
		r.prompt = 0
	}
	r.mu.Unlock()

	switch {
	case waiting:
		if f.Error != nil {
			held <- answer{failed: f.Error.Message}
			return
		}
		held <- answer{result: f.Result}
	case prompting:
		r.completed(f)
	}
}

// completed bounds the Prompt. An open message is closed first, because a Session
// that stays usable may not leave one open: the Cursor sits below every open
// Event, so it would never move again.
func (r *acpRun) completed(f frame) {
	r.report(func() {
		r.closeOpen()
		r.announceAll()
		if f.Error != nil {
			r.out.Failed(event.ErrAdapterFailed, f.Error.Message)
			r.out.Completed(event.StopError, event.Usage{})
			return
		}
		var done struct {
			StopReason string `json:"stopReason"`
			Usage      struct {
				Input     int `json:"inputTokens"`
				Output    int `json:"outputTokens"`
				CacheRead int `json:"cachedReadTokens"`
				Total     int `json:"totalTokens"`
			} `json:"usage"`
		}
		// A Prompt is bounded either way, because a Prompt that is never bounded
		// leaves the Session Working and refusing every Prompt after it. What is not
		// invented is the reason: an answer nobody could read stops on this project's
		// own word for it rather than on an empty one attributed to the Vendor.
		if err := json.Unmarshal(f.Result, &done); err != nil {
			r.out.Failed(event.ErrAdapterFailed, "the answer to this Prompt could not be read")
			r.out.Completed(event.StopError, event.Usage{})
			return
		}
		r.out.Completed(event.StopReason(done.StopReason), event.Usage{
			Input: done.Usage.Input, Output: done.Usage.Output,
			CacheRead: done.Usage.CacheRead, Total: done.Usage.Total,
		})
	})
}

// sessionUpdate is every shape the agent reports progress in. The fields that are
// absent on any one of them stay zero, which is what lets one struct read them all.
type sessionUpdate struct {
	Kind      string `json:"sessionUpdate"`
	MessageID string `json:"messageId"`
	Content   struct {
		Text string `json:"text"`
	} `json:"content"`

	ToolCallID string          `json:"toolCallId"`
	Status     string          `json:"status"`
	ToolKind   string          `json:"kind"`
	Title      string          `json:"title"`
	RawInput   json.RawMessage `json:"rawInput"`

	// name is the announcement's title, kept when a later frame overwrites Title.
	name string
}

// Name is the tool's own name, which OpenCode puts in the title of the frame that
// announces the call and replaces with something friendlier afterwards.
func (u sessionUpdate) Name() string {
	if u.name != "" {
		return u.name
	}
	return u.Title
}

// contentBlock is one piece of a tool call's result. The Client renders the text
// of it and nothing else.
type contentBlock struct {
	Content struct {
		Text string `json:"text"`
	} `json:"content"`
}

func (r *acpRun) update(params json.RawMessage) {
	var body struct {
		Update json.RawMessage `json:"update"`
	}
	if json.Unmarshal(params, &body) != nil {
		return
	}
	var u sessionUpdate
	// Content is a text block on a message and a list on a tool call, so the two
	// are read separately and each ignores the shape it does not want.
	json.Unmarshal(body.Update, &u)
	var blocks struct {
		Blocks []contentBlock `json:"content"`
	}
	json.Unmarshal(body.Update, &blocks)

	switch u.Kind {
	case "agent_message_chunk":
		r.report(func() { r.text(event.KindAssistantMessage, u.MessageID, u.Content.Text) })

	case "agent_thought_chunk":
		r.report(func() { r.text(event.KindReasoning, u.MessageID, u.Content.Text) })

	case "tool_call":
		// Held rather than reported. OpenCode announces a call with an empty rawInput
		// and sends the path or the command on the frame after it, so reporting this
		// one would write a Tool Call that names nothing it is about to do.
		r.hold(u)

	case "tool_call_update":
		// in_progress is deliberately not an Event of its own. OpenCode moves a call
		// there before it asks permission, so a Client that drew it would show a tool
		// as having run before the human was asked. Its arguments are still taken,
		// because deciding what belongs with what is the Adapter's to do.
		outcome, terminal := outcomeOf(u.Status)
		if !terminal {
			r.fill(u)
			return
		}
		r.report(func() {
			r.announce(u.ToolCallID)
			r.out.ToolCallEnded(u.ToolCallID, outcome, blockText(blocks.Blocks))
		})

	default:
		// usage_update is absent on one Vendor and available_commands_update carries
		// no fact about this Session, so an Adapter tolerates a native kind it does
		// not recognise as well as one that never arrives.
	}
}

// hold keeps an announced Tool Call until there is something worth reporting
// about it.
func (r *acpRun) hold(u sessionUpdate) {
	u.name = u.Title
	r.mu.Lock()
	defer r.mu.Unlock()
	r.held[u.ToolCallID] = u
}

// fill takes the arguments and the better title off a later frame. The name stays
// the one the announcement gave, which is the tool's, so name and title are the
// two different things ADR 0006 has them be.
func (r *acpRun) fill(u sessionUpdate) {
	r.mu.Lock()
	waiting, ok := r.held[u.ToolCallID]
	if ok && len(u.RawInput) > 0 && string(u.RawInput) != "{}" {
		waiting.RawInput, waiting.Title = u.RawInput, u.Title
		r.held[u.ToolCallID] = waiting
		ok = true
	} else {
		ok = false
	}
	r.mu.Unlock()
	if ok {
		r.report(func() { r.announce(u.ToolCallID) })
	}
}

// announce writes the Tool Call the Harness announced, once, and before anything
// that refers to it. Whatever is still held when the Prompt completes is written
// then, because the Daemon is about to close it and a call cannot end before it
// was requested. The caller holds the mutex.
func (r *acpRun) announce(id string) {
	u, ok := r.held[id]
	if !ok {
		return
	}
	delete(r.held, id)
	r.closeOpen()
	r.out.ToolCallRequested(u.ToolCallID, u.Name(), toolKind(u.ToolKind), u.Title, u.RawInput)
}

// announceAll writes every Tool Call still held, oldest announcement first. The
// caller holds the mutex.
func (r *acpRun) announceAll() {
	for _, id := range slices.Sorted(maps.Keys(r.held)) {
		r.announce(id)
	}
}

// text adds to the open message, and starts a new one when the Harness starts a
// new one. The Sink closes the open Event when the other kind arrives, so only a
// second message of the same kind has to be closed here.
func (r *acpRun) text(kind event.Kind, id, chunk string) {
	if r.openKind == kind && r.openID != id {
		r.closeOpen()
	}
	r.openKind, r.openID = kind, id
	if kind == event.KindReasoning {
		r.out.Reasoning(chunk, false)
		return
	}
	r.out.Message(chunk, false)
}

func (r *acpRun) closeOpen() {
	r.openKind, r.openID = closeOpen(r.out, r.openKind), ""
}

// permission is the one place the Harness blocks on this client. The reader waits
// with it, which is what the Harness is doing too.
func (r *acpRun) permission(f frame) {
	if f.ID == nil {
		return
	}
	var ask struct {
		ToolCall struct {
			ToolCallID string          `json:"toolCallId"`
			Title      string          `json:"title"`
			RawInput   json.RawMessage `json:"rawInput"`
		} `json:"toolCall"`
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	if err := json.Unmarshal(f.Params, &ask); err != nil {
		r.fail(*f.ID, -32602, "this permission request could not be read")
		return
	}

	r.report(func() { r.announce(ask.ToolCall.ToolCallID) })

	decision, err := r.out.Approve(r.session, ask.ToolCall.ToolCallID, ask.ToolCall.Title, string(ask.ToolCall.RawInput))
	if err != nil {
		r.answer(*f.ID, map[string]any{"outcome": map[string]any{"outcome": "cancelled"}})
		return
	}
	want := "allow_once"
	if decision == event.DecisionRefused {
		want = "reject_once"
	}
	for _, option := range ask.Options {
		if option.Kind == want {
			r.answer(*f.ID, map[string]any{"outcome": map[string]any{
				"outcome": "selected", "optionId": option.OptionID,
			}})
			return
		}
	}
	// A Harness that offers no way to say this is a Harness whose answer cannot be
	// trusted, so the turn is cancelled rather than answered with the wrong option.
	r.report(func() {
		r.out.Failed(event.ErrAdapterFailed, "the Harness offered no "+want+" for a Tool Call it asked about")
	})
	r.answer(*f.ID, map[string]any{"outcome": map[string]any{"outcome": "cancelled"}})
}

// writeFile is the write OpenCode delegates to its client, and the Daemon's second
// lever on one: a path outside the Workspace Root is refused here even though the
// permission gate passed.
func (r *acpRun) writeFile(f frame) {
	if f.ID == nil {
		return
	}
	var ask struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(f.Params, &ask); err != nil {
		r.fail(*f.ID, -32602, "this write could not be read")
		return
	}
	if err := r.spec.Files.WriteTextFile(ask.Path, ask.Content); err != nil {
		r.report(func() { r.out.Failed(event.ErrAdapterFailed, err.Error()) })
		r.fail(*f.ID, -32603, err.Error())
		return
	}
	r.answer(*f.ID, map[string]any{})
}

// toolKind maps ACP's own classes onto the five. A class this Adapter does not
// know becomes other, which is the slot the default Approval Policy holds at wait.
func toolKind(kind string) event.ToolKind {
	switch kind {
	case "read":
		return event.ToolRead
	case "edit":
		return event.ToolEdit
	case "execute":
		return event.ToolExecute
	case "fetch":
		return event.ToolFetch
	default:
		return event.ToolOther
	}
}

// outcomeOf reads a tool call's status. Only what the Harness said is reported:
// refused is the Daemon's own word and unknown is its synthesis, so neither can
// be reached from here.
func outcomeOf(status string) (event.Outcome, bool) {
	switch status {
	case "completed":
		return event.OutcomeOK, true
	case "failed":
		return event.OutcomeError, true
	default:
		return "", false
	}
}

func blockText(blocks []contentBlock) string {
	var said strings.Builder
	for _, b := range blocks {
		said.WriteString(b.Content.Text)
	}
	return said.String()
}

// request is one frame this Adapter writes. A nil ID is a notification, which
// nothing answers.
type request struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      *uint64   `json:"id,omitempty"`
	Method  string    `json:"method,omitempty"`
	Params  any       `json:"params,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

// call sends one request and waits for its answer, the Session ending, or ctx.
func (r *acpRun) call(ctx context.Context, method string, params any, into any) error {
	held := make(chan answer, 1)

	r.mu.Lock()
	id := r.nextID()
	r.waiting, r.waitingID = held, id
	r.mu.Unlock()

	if err := r.write(request{JSONRPC: "2.0", ID: &id, Method: method, Params: params}); err != nil {
		return err
	}

	select {
	case got := <-held:
		if got.failed != "" {
			return fmt.Errorf("%s: the Harness refused %s: %s", r.name, method, got.failed)
		}
		if err := json.Unmarshal(got.result, into); err != nil {
			return fmt.Errorf("%s: the answer to %s could not be read: %w", r.name, method, err)
		}
		return nil
	case <-r.stopped:
		return fmt.Errorf("%s: the Session ended before %s was answered", r.name, method)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// nextID is allocated under the mutex, because the reader answers the Harness
// while the Daemon is sending its own requests.
func (r *acpRun) nextID() uint64 {
	r.next++
	return r.next
}

func (r *acpRun) answer(id uint64, result any) {
	r.write(request{JSONRPC: "2.0", ID: &id, Result: result})
}

func (r *acpRun) fail(id uint64, code int, message string) {
	r.write(request{JSONRPC: "2.0", ID: &id, Error: &rpcError{Code: code, Message: message}})
}

// write puts one frame on stdin. Writes are serialised because the reader answers
// the Harness's requests while the Daemon is sending its own.
func (r *acpRun) write(f request) error {
	line, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("%s: %w", r.name, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.in.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("%s: the Harness would not take a frame: %w", r.name, err)
	}
	return nil
}
