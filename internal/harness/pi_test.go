package harness

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/vendors"
)

type piWriter func([]byte) (int, error)

func (w piWriter) Write(p []byte) (int, error) { return w(p) }

// piVendor is one Session's Vendor and the Model on it, which travel together
// through every replay because the launch checks them together.
type piVendor struct {
	name   string
	model  string
	vendor vendors.Endpoint
}

// The three Vendors the pi-vendors pass drove. The same wizard and the same
// prompts, so what differs between the three rows is the Vendor and nothing else.
var piVendors = []piVendor{
	{"lmstudio", "qwen/qwen3.5-9b", vendors.Endpoint{Kind: vendors.LMStudio, Base: "http://127.0.0.1:1234"}},
	{"ollama", "qwen3.5:9b", vendors.Endpoint{Kind: vendors.Ollama, Base: "http://127.0.0.1:11434"}},
	{"llamacpp", "qwen3.5-9b", vendors.Endpoint{Kind: vendors.LlamaSwap, Base: "http://127.0.0.1:8080"}},
}

// lmstudio is the run the gate captures were recorded on, and the one every test
// that is not about Vendor coverage uses.
var lmstudio = piVendors[0]

// piAdapter is the Adapter under test, with its Gate written into a directory
// this test owns.
func piAdapter(t *testing.T) *Pi {
	t.Helper()
	a, err := NewPi(t.TempDir())
	if err != nil {
		t.Fatalf("NewPi: %v", err)
	}
	return a
}

// startPi runs the launch and hands back what the Daemon was told. It drives no
// Prompt, because the launch failures have none to drive.
func startPi(t *testing.T, s *piScript, on piVendor, decision event.Decision) (*recording, Run, error) {
	t.Helper()
	d := newRecording()
	d.decide = decision
	run, err := piAdapter(t).Start(t.Context(), SessionSpec{
		Session: "s-1",
		Model:   on.model,
		Vendor:  on.vendor,
		Dir:     t.TempDir(),
		Spawn:   s.spawn(),
	}, d)
	return d, run, err
}

// replayPi drives one capture from Start through every Prompt it recorded to
// Close, and hands back what the Daemon was told.
func replayPi(t *testing.T, s *piScript, on piVendor, decision event.Decision) *recording {
	t.Helper()
	d, run, err := startPi(t, s, on, decision)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, text := range s.prompts(t) {
		if err := run.Prompt(t.Context(), text); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		select {
		case <-d.completed:
		case <-time.After(10 * time.Second):
			t.Fatalf("the Prompt %q never completed: %v", text, d.said())
		}
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s.wait(t)
	return d
}

// replayVendor drives one pi-vendors event stream.
func replayVendor(t *testing.T, on piVendor, capture string) *recording {
	t.Helper()
	s := newPiStream(t, "pi-vendors/"+on.name+"/"+capture, on.model, on.vendor.Base)
	return replayPi(t, s, on, event.DecisionAllowed)
}

// The five Kinds a Harness is the authority on, from recorded bytes and no
// network, on all three Vendors. Reasoning, AssistantMessage, ToolCallRequested,
// ToolCallEnded and PromptCompleted.
func TestTheFiveHarnessKindsComeOutOfThePiCaptures(t *testing.T) {
	for _, vendor := range piVendors {
		t.Run(vendor.name, func(t *testing.T) {
			d := replayVendor(t, vendor, "tool-events.jsonl")

			said := d.said()
			for _, kind := range []string{"Reasoning(", "Message(", "ToolCallRequested(", "ToolCallEnded(", "Completed("} {
				if count(said, kind) == 0 {
					t.Errorf("nothing in this replay produced %s: %v", kind, said)
				}
			}
			if last := said[len(said)-1]; !strings.HasPrefix(last, "Completed(") {
				t.Errorf("the replay ended on %s, and a Prompt ends on its completion", last)
			}
			// The Adapter reports nothing that did not happen, and a frame it could
			// not translate is something that did.
			if failed := count(said, "Failed("); failed != 0 {
				t.Errorf("this replay reported %d failures and the capture holds none: %v", failed, said)
			}
		})
	}
}

// Every Tool Call reaches a terminal status, and reaches it after it was
// requested. A Client that drew a call it never saw requested would have nothing
// to attach the result to.
func TestEveryPiToolCallReachesATerminalStatus(t *testing.T) {
	for _, vendor := range piVendors {
		t.Run(vendor.name, func(t *testing.T) {
			d := replayVendor(t, vendor, "tool-events.jsonl")

			said := d.said()
			requested, ended := count(said, "ToolCallRequested("), count(said, "ToolCallEnded(")
			if requested == 0 || requested != ended {
				t.Errorf("%d Tool Calls were requested and %d ended: %v", requested, ended, said)
			}
			if first, last := indexOf(said, "ToolCallRequested("), indexOf(said, "ToolCallEnded("); first > last {
				t.Errorf("a Tool Call ended before it was requested: %v", said)
			}
		})
	}
}

// Streaming tool output is out of v1, so the partial result Pi sends is dropped
// rather than reported. The whole of it still arrives on the call's end, and the
// raw bytes are in the Session's transcript either way.
func TestPiStreamedToolOutputIsDropped(t *testing.T) {
	d := replayVendor(t, lmstudio, "tool-events.jsonl")

	for _, call := range d.said() {
		if strings.Contains(call, "partialResult") {
			t.Errorf("a partial tool result was reported: %s", call)
		}
	}
	// The end carries the output the updates were fragments of, so nothing a human
	// reads is lost by dropping them. The dropped bytes are still written down:
	// stdout goes to the transcript whole, which daemon's
	// TestTheTranscriptHoldsTheHarnessStdout is.
	if !strings.Contains(strings.Join(d.said(), "\n"), "sample.txt") {
		t.Errorf("the tool's output never reached the Daemon: %v", d.said())
	}
}

// The Gates the capture settled, and no others. All five, because the Gate's
// table covers every ToolKind and both captured runs held every one of them.
func TestThePiGatesAreDeclaredForEveryToolKind(t *testing.T) {
	caps := piAdapter(t).Capabilities()

	if !caps.Tools {
		t.Error("Pi runs tools, and this Adapter says it does not")
	}
	for kind := range caps.Gates {
		if !caps.Gates[kind] {
			t.Errorf("%s is not gated, and captures/pi-gate-dispatch/ held it in both runs", event.ToolKind(kind))
		}
	}
}

// Every ToolKind is held, and the Daemon is asked about each one. This is the
// coverage the allow run measured, read back out of its own bytes.
func TestPiHoldsEveryToolKind(t *testing.T) {
	d := replayPi(t, newPiScript(t, "gate-allow-frames.jsonl"), lmstudio, event.DecisionAllowed)

	said := d.said()
	if count(said, "ToolCallRequested(") == 0 {
		t.Fatalf("this replay has no Tool Call in it: %v", said)
	}
	for kind := 0; kind < event.NumToolKinds; kind++ {
		want := "," + event.ToolKind(kind).String() + ","
		found := false
		for _, call := range said {
			if strings.HasPrefix(call, "ToolCallRequested(") && strings.Contains(call, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("no Tool Call was reported as %s: %v", event.ToolKind(kind), said)
		}
	}
	if failed := count(said, "Failed("); failed != 0 {
		t.Errorf("this replay reported %d failures and the capture holds none: %v", failed, said)
	}
	if len(d.asked) != count(said, "ToolCallRequested(") {
		t.Errorf("the Daemon was asked about %d of %d Tool Calls, and the Gate holds every one",
			len(d.asked), count(said, "ToolCallRequested("))
	}
}

// The Daemon's refusal reaches Pi as the Gate's own word for it. The capture's
// own out frames say deny, so the script fails the test if the Adapter says
// anything else.
func TestPiAnswersTheGateWithTheDaemonsRefusal(t *testing.T) {
	d := replayPi(t, newPiScript(t, "gate-deny-frames.jsonl"), lmstudio, event.DecisionRefused)

	said := d.said()
	if count(said, "ToolCallEnded(") == 0 {
		t.Fatalf("this replay has no Tool Call in it: %v", said)
	}
	for _, call := range said {
		if strings.HasPrefix(call, "ToolCallEnded(") && !strings.Contains(call, ","+string(event.OutcomeError)+",") {
			t.Errorf("a denied Tool Call ended as %s, and Pi reports a denial as an error", call)
		}
		// refused is the Daemon's word for its own decision, and an Adapter that
		// borrowed it would be reporting a decision it never made.
		if strings.Contains(call, string(event.OutcomeRefused)) {
			t.Errorf("the Adapter reported %s, which only the Daemon may say", call)
		}
	}
}

// A Tool Call is requested before the Daemon is asked about it. Pi says a tool
// has started while it is still waiting for the Gate, so a start is not evidence
// that anything ran and the Daemon hears the request, then the question, and
// nothing in between.
func TestAPiCallIsNotReportedAsRunningBeforeItsQuestion(t *testing.T) {
	d := replayPi(t, newPiScript(t, "gate-allow-frames.jsonl"), lmstudio, event.DecisionAllowed)

	said := d.said()
	requested, asked := indexOf(said, "ToolCallRequested("), indexOf(said, "Approve(")
	if requested < 0 || asked < 0 {
		t.Fatalf("this replay has no gated Tool Call in it: %v", said)
	}
	if requested > asked {
		t.Errorf("the Daemon was asked about a Tool Call it had not been told about: %v", said)
	}
	for _, call := range said[requested+1 : asked] {
		if strings.HasPrefix(call, "ToolCall") {
			t.Errorf("%s came between the request and the question: %v", call, said)
		}
	}
}

// A Gate that never announced is a Session with no gate anywhere, so the launch
// fails rather than degrading. This is the general shape of the three: the probe
// was answered and nothing came ahead of it.
func TestAPiWithNoGateIsNotASession(t *testing.T) {
	_, _, err := startPi(t, newPiScript(t, "no-gate-frames.jsonl"), lmstudio, event.DecisionAllowed)

	if err == nil {
		t.Fatal("a Pi whose Gate never announced started a Session")
	}
	if !strings.Contains(err.Error(), "announce") {
		t.Errorf("the launch failed with %q, which does not say the Gate was missing", err)
	}
}

// The second shape: the Gate loaded and then threw, and Pi says so in a frame of
// its own that arrives before the probe's answer.
func TestAPiGateThatThrewIsNotASession(t *testing.T) {
	_, _, err := startPi(t, newPiScript(t, "silent-gate-frames.jsonl"), lmstudio, event.DecisionAllowed)

	if err == nil {
		t.Fatal("a Pi whose Gate threw started a Session")
	}
	// Both halves: this Adapter's own words for the decision, and Pi's words for
	// the cause, which are the extension's and not this Adapter's to invent.
	if !strings.Contains(err.Error(), "would not load") {
		t.Errorf("the launch failed with %q, which does not say the Gate was the reason", err)
	}
	if !strings.Contains(err.Error(), "the Gate failed to announce") {
		t.Errorf("the launch failed with %q, and Pi said why", err)
	}
}

// An extension that is not the Gate is the Host's own, and its failure is not
// this Adapter's to report as a Gate that would not load. The launch still fails,
// because the Gate did not announce, and that is what it says.
func TestAnotherPiExtensionsFailureIsNotTheGates(t *testing.T) {
	s := newPiScriptFrom(t, "silent-gate-frames.jsonl", "/opt/pi/extensions/somebody-elses.js")
	_, _, err := startPi(t, s, lmstudio, event.DecisionAllowed)

	if err == nil {
		t.Fatal("a Pi whose Gate never announced started a Session")
	}
	if strings.Contains(err.Error(), "would not load") {
		t.Errorf("the launch failed with %q, and this Adapter's Gate is not what failed", err)
	}
	if !strings.Contains(err.Error(), "announce") {
		t.Errorf("the launch failed with %q, which does not say the Gate was missing", err)
	}
}

// Pi may answer the start probe and then close stdout before Start gets CPU.
// The answer arrived first, so the launch decision must use it.
func TestPiLaunchKeepsAProbeAnswerThatArrivedBeforeExit(t *testing.T) {
	for range 100 {
		r := &piRun{
			name:    "pi",
			spec:    SessionSpec{Model: lmstudio.model, Vendor: lmstudio.vendor},
			ready:   true,
			stopped: make(chan struct{}),
			gone:    make(chan struct{}),
		}
		r.in = piWriter(func(p []byte) (int, error) {
			r.answered(piFrame{
				ID:      startProbe,
				Success: true,
				Data: json.RawMessage(`{"model":{"id":"qwen/qwen3.5-9b",` +
					`"provider":"lmstudio","baseUrl":"http://127.0.0.1:1234/v1"}}`),
			})
			close(r.gone)
			return len(p), nil
		})

		if err := r.checkLaunch(t.Context()); err != nil {
			t.Fatalf("a start-probe answer was lost behind process exit: %v", err)
		}
	}
}

// The third shape: the extension did not parse, so Pi exited before it answered
// anything at all.
func TestAPiThatExitedDuringLaunchIsNotASession(t *testing.T) {
	_, _, err := startPi(t, newPiScript(t, "broken-gate-frames.jsonl"), lmstudio, event.DecisionAllowed)

	if err == nil {
		t.Fatal("a Pi that exited during its launch started a Session")
	}
	if !strings.Contains(err.Error(), "ended before") {
		t.Errorf("the launch failed with %q, which does not say the Harness went away", err)
	}
}

// The Model is selected and then read back, because a Host's models.json may name
// a Model this Session did not ask for.
func TestAModelPiDidNotSelectIsNotASession(t *testing.T) {
	asked := lmstudio
	asked.model = "qwen3.5:9b"
	_, _, err := startPi(t, newPiScript(t, "gate-allow-frames.jsonl"), asked, event.DecisionAllowed)

	if err == nil {
		t.Fatal("a Session started on a Model nobody asked for")
	}
	if !strings.Contains(err.Error(), "qwen3.5:9b") {
		t.Errorf("the launch failed with %q, which does not name the Model that was asked for", err)
	}
}

// The Vendor is read back as well, because one Model id may sit under two
// providers and only one of them is this Session's.
func TestAVendorPiDidNotSelectIsNotASession(t *testing.T) {
	asked := lmstudio
	asked.vendor = piVendors[1].vendor
	_, _, err := startPi(t, newPiScript(t, "gate-allow-frames.jsonl"), asked, event.DecisionAllowed)

	if err == nil {
		t.Fatal("a Session started against a Vendor nobody asked for")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:11434") {
		t.Errorf("the launch failed with %q, which does not name the Vendor that was asked for", err)
	}
}

// The Vendor is one address and not the text of one. A Vendor on port 12345 is
// not the Vendor on port 1234, although one address begins with the other.
func TestAPiVendorOnAnotherPortIsNotThisOne(t *testing.T) {
	asked := lmstudio
	asked.vendor.Base = "http://127.0.0.1:123"
	_, _, err := startPi(t, newPiScript(t, "gate-allow-frames.jsonl"), asked, event.DecisionAllowed)

	if err == nil {
		t.Fatal("a Session started against a Vendor on another port")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:123") {
		t.Errorf("the launch failed with %q, which does not name the Vendor that was asked for", err)
	}
}

// Behaviour 10: the same Model, the same Prompt and the same Host render the same
// way for OpenCode and for Pi. What that means is that the two Harnesses report
// the same Kinds, every Tool Call is requested before it ends, and the Prompt
// ends once. Nothing above this seam can tell which Harness ran.
func TestPiAndOpenCodeRenderTheSameWay(t *testing.T) {
	pi := replayVendor(t, piVendors[2], "tool-events.jsonl")
	opencode, _ := replay(t, "opencode/llamaswap/execute-frames.jsonl", swapModel)

	for _, kind := range []string{"Reasoning(", "Message(", "ToolCallRequested(", "ToolCallEnded(", "Completed("} {
		if (count(pi.said(), kind) == 0) != (count(opencode.said(), kind) == 0) {
			t.Errorf("%s is reported by one Harness and not the other", kind)
		}
	}
	for _, d := range []*recording{pi, opencode} {
		said := d.said()
		if count(said, "Completed(") != 1 {
			t.Errorf("this Prompt ended %d times: %v", count(said, "Completed("), said)
		}
		if indexOf(said, "ToolCallRequested(") > indexOf(said, "ToolCallEnded(") {
			t.Errorf("a Tool Call ended before it was requested: %v", said)
		}
	}
}

// The Gate the Adapter loads is the Gate this repo holds, because an Adapter that
// wrote a different one would speak a protocol nothing answers.
func TestTheGateOnDiskIsTheGateTheAdapterLoads(t *testing.T) {
	if !strings.Contains(string(dispatchGate), gateProtocol) {
		t.Errorf("the embedded Gate does not speak %s", gateProtocol)
	}
	// Every name in the Adapter's table is a name the Gate classifies, so the slot
	// that is gated and the slot that is reported are the same slot.
	for _, name := range []string{"read", "grep", "find", "ls", "edit", "write", "bash", "powershell", "fetch"} {
		if !strings.Contains(string(dispatchGate), "\t"+name+": ") {
			t.Errorf("the Gate has no entry for %q, and the Adapter maps it to %s", name, toolKindOf(name))
		}
	}
}
