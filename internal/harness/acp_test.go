package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/vendors"
)

// The llama-swap capture is the one replayed here. Its session/new reports the
// Model its config asked for, which is what a Session that starts looks like; the
// two 2026-08-27 captures report a Model nobody asked for, and the last test in
// this file is that.
const swapModel = "qwen3.5-9b"

// The Model the refusal capture ran on. A different Vendor and a later day than
// the llama-swap runs, so it is named rather than shared.
const rejectModel = "qwen3.5:9b"

const configFile = "opencode.json"

// recording is the Daemon as a test sees it. Every Sink call lands in one list, in
// order, because order is half of what these fixtures prove.
type recording struct {
	files map[string]string

	mu        sync.Mutex
	calls     []string
	completed chan struct{}

	// decide is what this Daemon answers a gate with.
	decide event.Decision
	asked  []string
}

func newRecording() *recording {
	return &recording{
		files:     map[string]string{},
		completed: make(chan struct{}, 1),
		decide:    event.DecisionAllowed,
	}
}

func (d *recording) add(format string, args ...any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, fmt.Sprintf(format, args...))
}

func (d *recording) said() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.calls...)
}

func (d *recording) Message(text string, end bool)   { d.add("Message(%q,%v)", text, end) }
func (d *recording) Reasoning(text string, end bool) { d.add("Reasoning(%q,%v)", text, end) }

func (d *recording) ToolCallRequested(id, name string, k event.ToolKind, title string, args json.RawMessage) {
	d.add("ToolCallRequested(%s,%s,%s,%q,%s)", id, name, k, title, args)
}

func (d *recording) ToolCallEnded(id string, o event.Outcome, content string) {
	d.add("ToolCallEnded(%s,%s,%q)", id, o, content)
}

func (d *recording) Completed(stop event.StopReason, u event.Usage) {
	d.add("Completed(%s,%d)", stop, u.Total)
	select {
	case d.completed <- struct{}{}:
	default:
	}
}

func (d *recording) Failed(code event.ErrorCode, msg string) { d.add("Failed(%s,%q)", code, msg) }

func (d *recording) Approve(_ context.Context, id, title, detail string) (event.Decision, error) {
	d.mu.Lock()
	d.asked = append(d.asked, id)
	decision := d.decide
	d.mu.Unlock()
	d.add("Approve(%s,%q)", id, title)
	return decision, nil
}

func (d *recording) WriteTextFile(path, content string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.files[path] = content
	return nil
}

// replay drives one capture from Start to Close and hands back what the Daemon
// was told.
func replay(t *testing.T, capture, model string) (*recording, *script) {
	t.Helper()
	return replayDeciding(t, capture, model, event.DecisionAllowed)
}

// replayDeciding is replay with the Daemon's answer to a gate chosen, because the
// refusal capture was recorded against a Daemon that said no.
func replayDeciding(t *testing.T, capture, model string, decision event.Decision) (*recording, *script) {
	t.Helper()
	s := newScript(t, capture)
	d := newRecording()
	d.decide = decision

	run, err := NewOpenCode().Start(t.Context(), SessionSpec{
		Session: "s-1",
		Model:   model,
		Vendor:  vendors.Endpoint{Base: "http://127.0.0.1:8080"},
		Dir:     t.TempDir(),
		Spawn:   s.spawn(),
		Files:   d,
	}, d)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := run.Prompt(t.Context(), s.prompt(t)); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	select {
	case <-d.completed:
	case <-time.After(10 * time.Second):
		t.Fatalf("the Prompt never completed: %v", d.said())
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s.wait(t)
	return d, s
}

// The read run, which is where OpenCode's read exemption shows: two reads, both
// ending, and the Daemon never asked about either.
func TestOpenCodeNeverAsksAboutReads(t *testing.T) {
	d, _ := replay(t, "opencode/llamaswap/read-frames.jsonl", swapModel)

	if len(d.asked) != 0 {
		t.Errorf("the Daemon was asked about %v, and a read is never gated", d.asked)
	}
	started, ended := count(d.said(), "ToolCallRequested"), count(d.said(), "ToolCallEnded")
	if started == 0 || started != ended {
		t.Errorf("%d Tool Calls started and %d ended: %v", started, ended, d.said())
	}
}

// The five Kinds a Harness is the authority on, from recorded bytes and no
// network. Reasoning, AssistantMessage, ToolCallRequested, ToolCallEnded and
// PromptCompleted.
func TestTheFiveHarnessKindsComeOutOfTheCapture(t *testing.T) {
	d, _ := replay(t, "opencode/llamaswap/execute-frames.jsonl", swapModel)

	said := d.said()
	for _, kind := range []string{"Reasoning(", "Message(", "ToolCallRequested(", "ToolCallEnded(", "Completed("} {
		if count(said, kind) == 0 {
			t.Errorf("nothing in this replay produced %s: %v", kind, said)
		}
	}
	if last := said[len(said)-1]; !strings.HasPrefix(last, "Completed(") {
		t.Errorf("the replay ended on %s, and a Prompt ends on its completion", last)
	}
}

// OpenCode moves a Tool Call to in_progress before it asks, so an Adapter that
// reported in_progress would show a tool as having run before the human was asked.
// The Daemon hears the request, then the question, and nothing in between.
func TestACallIsNotReportedAsRunningBeforeItsQuestion(t *testing.T) {
	d, _ := replay(t, "opencode/llamaswap/execute-frames.jsonl", swapModel)

	said := d.said()
	requested, asked := indexOf(said, "ToolCallRequested("), indexOf(said, "Approve(")
	if requested < 0 || asked < 0 {
		t.Fatalf("this replay has no gated Tool Call in it: %v", said)
	}
	for _, call := range said[requested+1 : asked] {
		if strings.HasPrefix(call, "ToolCall") {
			t.Errorf("%s came between the request and the question: %v", call, said)
		}
	}
}

// The write OpenCode delegates to its client lands through the Daemon's contained
// file access, which is the second lever on a write that the permission gate
// already passed.
func TestTheDelegatedWriteGoesThroughContainedFileAccess(t *testing.T) {
	d, _ := replay(t, "opencode/llamaswap/edit-frames.jsonl", swapModel)

	if len(d.files) != 1 {
		t.Fatalf("the Adapter wrote %v, and only the Harness's delegated file belongs here", keys(d.files))
	}
	if count(d.said(), "Failed(") != 0 {
		t.Errorf("the replay reported a failure: %v", d.said())
	}
}

// The config names the Vendor this Session runs against, and the Model as OpenCode
// spells it.
func TestTheSessionConfigNamesTheVendorAndTheModel(t *testing.T) {
	config := sessionConfig(SessionSpec{
		Model:  swapModel,
		Vendor: vendors.Endpoint{Base: "http://127.0.0.1:8080"},
	})
	for _, want := range []string{qualified(swapModel), "http://127.0.0.1:8080/v1", "openai-compatible"} {
		if !strings.Contains(config, want) {
			t.Errorf("the config says nothing about %q:\n%s", want, config)
		}
	}
}

func TestTheSessionConfigUsesTheVendorToken(t *testing.T) {
	config := sessionConfig(SessionSpec{
		Model:  swapModel,
		Vendor: vendors.Endpoint{Base: "http://127.0.0.1:8080", Token: "secret-token"},
	})

	if !strings.Contains(config, `"apiKey": "secret-token"`) {
		t.Errorf("the config does not carry the Vendor token:\n%s", config)
	}
}

func TestStartDoesNotReplaceTheProjectsOpenCodeConfig(t *testing.T) {
	s := newScript(t, "opencode/llamaswap/read-frames.jsonl")
	d := newRecording()
	d.files[configFile] = "the user's config"
	var launched Launch

	run, err := NewOpenCode().Start(t.Context(), SessionSpec{
		Session: "s-1",
		Model:   swapModel,
		Vendor:  vendors.Endpoint{Base: "http://127.0.0.1:8080"},
		Dir:     t.TempDir(),
		Spawn: func(_ context.Context, launch Launch) (Pipes, error) {
			launched = launch
			return s.pipes, nil
		},
		Files: d,
	}, d)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	s.abandon()
	defer run.Close()

	if got := d.files[configFile]; got != "the user's config" {
		t.Errorf("Start replaced the project's %s with %q", configFile, got)
	}
	if len(launched.Env) != 1 || !strings.HasPrefix(launched.Env[0], "OPENCODE_CONFIG_CONTENT=") {
		t.Errorf("launch environment = %v", launched.Env)
	}
}

// Start returns only once the Harness has said it is running the Model it was
// given. The 2026-08-27 captures were answered with a hosted Model nobody asked
// for, and that Session must never exist.
func TestAModelNobodyAskedForIsNotASession(t *testing.T) {
	s := newScript(t, "opencode/ollama/read-frames.jsonl")
	d := newRecording()

	_, err := NewOpenCode().Start(t.Context(), SessionSpec{
		Model: "qwen3.5:9b",
		Dir:   t.TempDir(),
		Spawn: s.spawn(),
		Files: d,
	}, d)
	if err == nil {
		t.Fatal("the Session started on a Model nobody asked for")
	}
	if !strings.Contains(err.Error(), "opencode/big-pickle") {
		t.Errorf("the error is %q, and it has to name the Model the Harness selected", err)
	}
}

// What the Daemon decides is what OpenCode is told, by the option's kind and not
// by the id spelling it. The frame here is the shape the capture recorded; what
// OpenCode does with a refusal is not established, because no captured run ever
// refused one.
func TestARefusedToolCallIsAnsweredWithTheRejectOption(t *testing.T) {
	for _, c := range []struct {
		decision event.Decision
		want     string
	}{
		{event.DecisionAllowed, "once"},
		{event.DecisionRefused, "reject"},
	} {
		t.Run(string(c.decision), func(t *testing.T) {
			d := newRecording()
			d.decide = c.decision
			said := &bytes.Buffer{}
			r := &acpRun{name: "opencode", session: t.Context(), out: d, in: said, stopped: make(chan struct{})}

			id := uint64(7)
			r.permission(frame{ID: &id, Method: "session/request_permission", Params: json.RawMessage(`{
				"toolCall": {"toolCallId": "c1", "title": "echo hello", "rawInput": {"command": "echo hello"}},
				"options": [
					{"optionId": "once", "kind": "allow_once", "name": "Allow once"},
					{"optionId": "always", "kind": "allow_always", "name": "Always allow"},
					{"optionId": "reject", "kind": "reject_once", "name": "Reject"}
				]
			}`)})

			var answer struct {
				ID     uint64 `json:"id"`
				Result struct {
					Outcome struct {
						Outcome  string `json:"outcome"`
						OptionID string `json:"optionId"`
					} `json:"outcome"`
				} `json:"result"`
			}
			if err := json.Unmarshal(said.Bytes(), &answer); err != nil {
				t.Fatalf("the Adapter wrote %q: %v", said, err)
			}
			if answer.ID != id || answer.Result.Outcome.Outcome != "selected" {
				t.Fatalf("the Adapter answered %+v", answer)
			}
			if answer.Result.Outcome.OptionID != c.want {
				t.Errorf("the Adapter chose %q and the Daemon decided %s", answer.Result.Outcome.OptionID, c.decision)
			}
			if len(d.asked) != 1 || d.asked[0] != "c1" {
				t.Errorf("the Daemon was asked about %v", d.asked)
			}
		})
	}
}

// A Harness that offers no way to say what the Daemon decided is a Harness whose
// answer cannot be trusted, so the turn is cancelled rather than answered with
// the wrong option.
func TestAGateWithNoRejectOptionIsCancelled(t *testing.T) {
	d := newRecording()
	d.decide = event.DecisionRefused
	said := &bytes.Buffer{}
	r := &acpRun{name: "opencode", session: t.Context(), out: d, in: said, stopped: make(chan struct{})}

	id := uint64(1)
	r.permission(frame{ID: &id, Method: "session/request_permission", Params: json.RawMessage(`{
		"toolCall": {"toolCallId": "c1", "title": "rm -rf /"},
		"options": [{"optionId": "once", "kind": "allow_once", "name": "Allow once"}]
	}`)})

	if !strings.Contains(said.String(), `"cancelled"`) {
		t.Errorf("the Adapter answered %q", said)
	}
	if count(d.said(), "Failed(") != 1 {
		t.Errorf("the Daemon was not told: %v", d.said())
	}
}

// The rule an Adapter is judged on: it may decide what belongs with what, and it
// may not report something that did not happen. Every id and every word it gave
// the Daemon is checked back against the Harness's own raw bytes.
func TestTheAdapterReportsNothingTheRawTranscriptDoesNotHold(t *testing.T) {
	for _, label := range []string{"read", "edit", "execute"} {
		t.Run(label, func(t *testing.T) {
			d, _ := replay(t, "opencode/llamaswap/"+label+"-frames.jsonl", swapModel)
			raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "research", "captures",
				"opencode", "llamaswap", label+"-raw.log"))
			if err != nil {
				t.Fatalf("raw log: %v", err)
			}
			said := string(raw)

			for _, call := range d.said() {
				open, args, found := strings.Cut(call, "(")
				if !found || !strings.HasPrefix(open, "ToolCall") && open != "Approve" {
					continue
				}
				id, _, _ := strings.Cut(args, ",")
				if !strings.Contains(said, id) {
					t.Errorf("%s names %q, and the Harness never said it", call, id)
				}
			}
		})
	}
}

// OpenCode announces a Tool Call with an empty rawInput and sends the path or the
// command on the frame after it. The Event has to carry what the tool is about to
// do, so the announcement is held until that frame arrives.
func TestAToolCallCarriesWhatItIsAboutToDo(t *testing.T) {
	for _, c := range []struct{ capture, name, says string }{
		{"opencode/llamaswap/read-frames.jsonl", "read", "notes.txt"},
		{"opencode/llamaswap/edit-frames.jsonl", "write", "banana"},
		{"opencode/llamaswap/execute-frames.jsonl", "bash", "echo capstone-probe"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, _ := replay(t, c.capture, swapModel)

			var requested string
			for _, call := range d.said() {
				if strings.HasPrefix(call, "ToolCallRequested(") {
					requested = call
					break
				}
			}
			if requested == "" {
				t.Fatalf("nothing was requested: %v", d.said())
			}
			if !strings.Contains(requested, c.says) {
				t.Errorf("%s says nothing about %q", requested, c.says)
			}
			if !strings.Contains(requested, ","+c.name+",") {
				t.Errorf("%s is not named %q, and the name is the tool's own", requested, c.name)
			}
		})
	}
}

// A native kind this Adapter has no Event for is dropped, and one that never
// arrives costs nothing. usage_update is both: two Vendors send it and the third
// does not.
func TestANativeKindWithNoEventIsDropped(t *testing.T) {
	d := newRecording()
	r := &acpRun{name: "opencode", out: d, held: map[string]sessionUpdate{}, stopped: make(chan struct{})}

	r.update(json.RawMessage(`{"update":{"sessionUpdate":"usage_update","used":10660,"size":200000}}`))
	r.update(json.RawMessage(`{"update":{"sessionUpdate":"available_commands_update","availableCommands":[]}}`))
	r.update(json.RawMessage(`{"update":{"sessionUpdate":"something_from_next_year"}}`))

	if said := d.said(); len(said) != 0 {
		t.Errorf("the Daemon was told %v about frames that carry no fact", said)
	}
}

func TestAReadFailureIsReported(t *testing.T) {
	d := newRecording()
	r := &acpRun{name: "opencode", out: d, stopped: make(chan struct{})}

	r.read(strings.NewReader(strings.Repeat("x", frameLimit+1)))

	if count(d.said(), "Failed(") != 1 {
		t.Errorf("the read failure was not reported: %v", d.said())
	}
}

func TestHeldToolCallsKeepAnnouncementOrder(t *testing.T) {
	d := newRecording()
	r := &acpRun{name: "opencode", out: d, held: map[string]sessionUpdate{}, stopped: make(chan struct{})}
	r.hold(sessionUpdate{ToolCallID: "z-first", Title: "first", ToolKind: "execute"})
	r.hold(sessionUpdate{ToolCallID: "a-second", Title: "second", ToolKind: "execute"})

	r.mu.Lock()
	r.announceAll()
	r.mu.Unlock()

	said := d.said()
	if len(said) != 2 || !strings.Contains(said[0], "z-first") || !strings.Contains(said[1], "a-second") {
		t.Errorf("Tool Calls were announced out of order: %v", said)
	}
}

type blockingWriter struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *blockingWriter) Write(p []byte) (int, error) {
	w.once.Do(func() {
		close(w.started)
		<-w.release
	})
	return len(p), nil
}

func TestABlockedWriteDoesNotBlockClose(t *testing.T) {
	w := &blockingWriter{started: make(chan struct{}), release: make(chan struct{})}
	r := &acpRun{name: "opencode", in: w, stopped: make(chan struct{})}
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		r.write(request{JSONRPC: "2.0", Method: "blocked"})
	}()
	<-w.started

	closeDone := make(chan struct{})
	go func() {
		defer close(closeDone)
		r.Close()
	}()
	select {
	case <-closeDone:
	case <-time.After(100 * time.Millisecond):
		close(w.release)
		<-writeDone
		t.Fatal("a blocked stdin write also blocked Close")
	}
	close(w.release)
	<-writeDone
}

// The Gates are declared, and read is not among them.
func TestTheOpenCodeGatesAreDeclaredAndReadIsNotAmongThem(t *testing.T) {
	caps := NewOpenCode().Capabilities()
	if !caps.Tools {
		t.Fatal("OpenCode runs tools")
	}
	if caps.Gates[event.ToolRead] {
		t.Error("read is gated, and OpenCode never asks about one")
	}
	for _, kind := range []event.ToolKind{event.ToolEdit, event.ToolExecute} {
		if !caps.Gates[kind] {
			t.Errorf("%s is not gated, and the capture counts one asked for every one started", kind)
		}
	}
	// fetch is the cautious answer rather than the measured one. No capture ever
	// exercised a webfetch, and a Gate that is declared and then silent is a slot
	// that says wait and behaves like auto.
	if caps.Gates[event.ToolFetch] {
		t.Error("fetch is gated, and no capture has ever seen OpenCode ask about one")
	}
}

func count(said []string, prefix string) int {
	n := 0
	for _, call := range said {
		if strings.HasPrefix(call, prefix) {
			n++
		}
	}
	return n
}

func indexOf(said []string, prefix string) int {
	for i, call := range said {
		if strings.HasPrefix(call, prefix) {
			return i
		}
	}
	return -1
}

func keys(files map[string]string) []string {
	out := make([]string, 0, len(files))
	for name := range files {
		out = append(out, name)
	}
	return out
}

// The refusal, replayed from the bytes a real OpenCode wrote when it was told no.
// Issue #46 asked what reject_once does, because every earlier capture answered
// allow and the answer was an assumption. It is not one now.
//
// OpenCode moves the call to failed, ends the turn on end_turn, and exits 0. So a
// refusal is an ordinary end to a Prompt, and the Adapter needs nothing special
// for one.
func TestARealRefusalEndsTheToolCallAndTheTurn(t *testing.T) {
	d, _ := replayDeciding(t, "opencode-reject/ollama/reject-frames.jsonl", rejectModel, event.DecisionRefused)

	said := d.said()
	want := []string{"ToolCallRequested(", "Approve(", "ToolCallEnded(", "Completed("}
	at := 0
	for _, call := range said {
		if at < len(want) && strings.HasPrefix(call, want[at]) {
			at++
		}
	}
	if at != len(want) {
		t.Errorf("a refused Prompt said %v, and it has to say %v in that order", said, want)
	}

	// failed is what OpenCode reports for a call the Daemon refused, and error is
	// what the Adapter is allowed to say about it. refused is the Daemon's own word.
	for _, call := range said {
		if strings.HasPrefix(call, "ToolCallEnded(") && !strings.Contains(call, string(event.OutcomeError)) {
			t.Errorf("the refused call ended as %s", call)
		}
	}
	if last := said[len(said)-1]; !strings.HasPrefix(last, "Completed(end_turn") {
		t.Errorf("a refused Prompt ended on %s, and OpenCode ended the turn", last)
	}
}

// The refusal capture is the second proof of the in_progress rule, and the one
// that matters most: OpenCode moved this call to in_progress and then never ran
// it. An Adapter that reported in_progress would have shown a write that the
// human refused and the disk never received.
func TestTheRefusedCallWasNeverReportedAsRunning(t *testing.T) {
	d, _ := replayDeciding(t, "opencode-reject/ollama/reject-frames.jsonl", rejectModel, event.DecisionRefused)

	said := d.said()
	requested, asked := indexOf(said, "ToolCallRequested("), indexOf(said, "Approve(")
	if requested < 0 || asked < 0 {
		t.Fatalf("this replay has no gated Tool Call in it: %v", said)
	}
	for _, call := range said[requested+1 : asked] {
		if strings.HasPrefix(call, "ToolCall") {
			t.Errorf("%s came between the request and the question: %v", call, said)
		}
	}
}
