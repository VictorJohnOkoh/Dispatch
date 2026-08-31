package harness

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
)

// stub is a Harness Adapter with no Harness behind it, which is what the seam is
// for: it reaches the outside world only through the Spawn, Files and Sink its
// caller supplies, and a test supplies all three.
type stub struct {
	caps Capabilities
}

func (s *stub) Capabilities() Capabilities { return s.caps }

func (s *stub) Start(ctx context.Context, spec SessionSpec, out Sink) (Run, error) {
	if spec.Spawn == nil {
		return nil, errors.New("stub: no Spawner")
	}
	if _, err := spec.Spawn(ctx, Launch{Args: []string{"--mode", "rpc"}}); err != nil {
		return nil, err
	}
	return &stubRun{out: out}, nil
}

type stubRun struct{ out Sink }

func (r *stubRun) Prompt(ctx context.Context, text string) error {
	r.out.Message("ok", true)
	r.out.Completed("stop", event.Usage{})
	return nil
}

func (r *stubRun) Interrupt(ctx context.Context) error { return nil }
func (r *stubRun) Close() error                        { return nil }

func TestAStubAdapterSatisfiesTheInterface(t *testing.T) {
	var spawned Launch
	spec := SessionSpec{
		Session: "s1",
		Model:   "capstone/qwen3.5:9b",
		Spawn: func(_ context.Context, l Launch) (Pipes, error) {
			spawned = l
			return Pipes{}, nil
		},
	}

	var adapter Adapter = &stub{caps: Capabilities{Tools: true}}
	sink := newRecorder(t)

	run, err := adapter.Start(context.Background(), spec, sink)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := run.Prompt(context.Background(), "hello"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if len(spawned.Args) != 2 {
		t.Errorf("the Adapter chose no arguments, got %v", spawned.Args)
	}
	if got := sink.stopReason(); got != "stop" {
		t.Errorf("stop reason %q, want %q", got, "stop")
	}
}

// TestAnAdapterHasNoOutputPathBesideTheSink reads the interface itself. Every method
// a Run offers answers with an error and nothing else, so an Adapter holding a fact
// has the Sink to report it on and nowhere else.
func TestAnAdapterHasNoOutputPathBesideTheSink(t *testing.T) {
	errorType := reflect.TypeOf((*error)(nil)).Elem()
	runType := reflect.TypeOf((*Run)(nil)).Elem()

	for i := range runType.NumMethod() {
		m := runType.Method(i)
		if m.Type.NumOut() != 1 || m.Type.Out(0) != errorType {
			t.Errorf("Run.%s answers with something other than an error", m.Name)
		}
	}

	start, _ := reflect.TypeOf((*Adapter)(nil)).Elem().MethodByName("Start")
	if start.Type.NumOut() != 2 || start.Type.Out(0) != runType || start.Type.Out(1) != errorType {
		t.Error("Start answers with something other than a Run and an error")
	}
}

// recorder is the Daemon as a test sees it. The four tool methods fail the test
// rather than record, because a passthrough Session must never reach them.
type recorder struct {
	t *testing.T

	messages  []text
	reasoning []text
	failures  []failure
	completed []completion
}

type text struct {
	text string
	end  bool
}

type failure struct {
	code event.ErrorCode
	msg  string
}

type completion struct {
	stop  event.StopReason
	usage event.Usage
}

func (r *recorder) Message(s string, end bool)   { r.messages = append(r.messages, text{s, end}) }
func (r *recorder) Reasoning(s string, end bool) { r.reasoning = append(r.reasoning, text{s, end}) }

func (r *recorder) Completed(stop event.StopReason, u event.Usage) {
	r.completed = append(r.completed, completion{stop, u})
}

func (r *recorder) Failed(code event.ErrorCode, msg string) {
	r.failures = append(r.failures, failure{code, msg})
}

func newRecorder(t *testing.T) *recorder { return &recorder{t: t} }

func (r *recorder) ToolCallRequested(id, name string, k event.ToolKind, title string, args json.RawMessage) {
	r.t.Errorf("ToolCallRequested(%q) on a Session with no tools", name)
}

func (r *recorder) ToolCallEnded(id string, o event.Outcome, content string) {
	r.t.Errorf("ToolCallEnded(%q) on a Session with no tools", id)
}

func (r *recorder) Approve(ctx context.Context, id, title, detail string) (event.Decision, error) {
	r.t.Errorf("Approve(%q) on a Session with no Gates", id)
	return event.DecisionRefused, nil
}

// joined is every Message text in order, which is the AssistantMessage the Daemon
// accumulates out of the Deltas.
func (r *recorder) joined() string {
	var whole string
	for _, m := range r.messages {
		whole += m.text
	}
	return whole
}

func (r *recorder) stopReason() event.StopReason {
	if len(r.completed) != 1 {
		r.t.Fatalf("want one PromptCompleted, got %d", len(r.completed))
	}
	return r.completed[0].stop
}
