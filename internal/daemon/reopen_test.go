package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/harness"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

func TestClosedSessionReopensOnPrompt(t *testing.T) {
	for _, reason := range []string{"stopped", "lost", "failed"} {
		t.Run(reason, func(t *testing.T) {
			h := newHost(t)
			h.chat.serve(helloStream, false)
			id := h.idle(t)
			h.command(t, id, "prompts", promptBody)
			h.waitState(t, id, "Idle")
			if reason == "lost" {
				d := killed(t, h)
				d.harnesses = []Harness{{Name: "passthrough", Adapter: harness.NewPassthrough(h.chat)}}
				d.vendors.pollAll(t.Context())
				h.Daemon = d
			} else if reason == "failed" {
				s, _, _ := h.sessions.find(id)
				h.endFailed(s, event.ErrHarnessFailed, "test crash")
			} else {
				h.command(t, id, "stop", "")
			}
			w := h.post(t, "/v1/sessions/"+string(id)+"/prompts", `{"text":"remember"}`)
			if w.Code != protocol.StatusAccepted {
				t.Fatalf("reopen: %d: %s", w.Code, w.Body.String())
			}
			h.waitState(t, id, "Idle")
			h.chat.mu.Lock()
			messages := h.chat.histories[len(h.chat.histories)-1]
			h.chat.mu.Unlock()
			if len(messages) != 3 || messages[0].Content != "hello" || messages[1].Content != "Hello" || messages[2].Content != "remember" {
				t.Fatalf("reopened history: %+v", messages)
			}
			if got := h.list(t); len(got) != 1 {
				t.Fatalf("reopen duplicated Session: %+v", got)
			}
			h.command(t, id, "stop", "")
		})
	}
}

// A reopen can take as long as a Model load. A caller that gave up before the
// load finished saw the Prompt fail, so the Prompt is not sent after it.
func TestAReopenPromptNobodyWaitsForIsNotSent(t *testing.T) {
	h := newHost(t)
	id := h.idle(t)
	h.command(t, id, "stop", "")
	h.vendor.gate = make(chan struct{})
	h.vendor.loading = make(chan struct{})

	ctx, cancel := context.WithCancel(t.Context())
	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+string(id)+"/prompts", strings.NewReader(promptBody)).WithContext(ctx)
	done := make(chan struct{})
	go func() { h.handler().ServeHTTP(httptest.NewRecorder(), r); close(done) }()
	<-h.vendor.loading
	cancel()
	close(h.vendor.gate)
	<-done

	h.waitState(t, id, "Idle")
	if slices.Contains(h.kinds(t), string(event.KindPromptSubmitted)) {
		t.Error("a Prompt was sent after its caller gave up")
	}
	h.command(t, id, "stop", "")
}

func TestAdmissionRefusesReopenWithoutEvents(t *testing.T) {
	h := newHost(t)
	closed := h.idle(t)
	h.command(t, closed, "stop", "")
	live := h.idle(t)
	before := h.events.Cursor()
	w := h.post(t, "/v1/sessions/"+string(closed)+"/prompts", promptBody)
	refusal := h.refusal(t, w, protocol.StatusConflict)
	if refusal.Reason != protocol.ReasonAdmission || len(refusal.Blocking) != 1 || refusal.Blocking[0] != string(live) {
		t.Fatalf("refusal: %+v", refusal)
	}
	if h.events.Cursor() != before {
		t.Fatal("refused reopen wrote Events")
	}
	h.command(t, live, "stop", "")
}

type rememberedHarness struct {
	specs chan harness.SessionSpec
}

func (a *rememberedHarness) Capabilities() harness.Capabilities { return newGating().Capabilities() }
func (a *rememberedHarness) Start(_ context.Context, spec harness.SessionSpec, _ harness.Sink) (harness.Run, error) {
	a.specs <- spec
	spec.Remember("native-history")
	return a, nil
}
func (a *rememberedHarness) Prompt(context.Context, string) error { return nil }
func (a *rememberedHarness) Interrupt(context.Context) error      { return nil }
func (a *rememberedHarness) Close() error                         { return nil }

func TestReopenKeepsHarnessModelDirectoryPolicyAndReference(t *testing.T) {
	a := &rememberedHarness{specs: make(chan harness.SessionSpec, 2)}
	h := newHost(t, Harness{Name: "remembered", Adapter: a})
	id := h.started(t, h.post(t, "/v1/sessions", `{"harness":"remembered","model":"qwen3:8b"}`)).Session
	h.waitState(t, id, "Idle")
	first := <-a.specs
	h.command(t, id, "policy", `{"policy":{"read":"auto","edit":"refuse","execute":"auto","fetch":"auto","other":"auto"}}`)
	h.command(t, id, "stop", "")
	w := h.post(t, "/v1/sessions/"+string(id)+"/prompts", promptBody)
	if w.Code != protocol.StatusAccepted {
		t.Fatalf("reopen: %d: %s", w.Code, w.Body.String())
	}
	next := <-a.specs
	if next.Reference != "native-history" || next.Session != first.Session || next.Model != first.Model || next.Dir != first.Dir || next.Vendor != first.Vendor {
		t.Fatalf("reopened spec: %+v", next)
	}
	s, _, _ := h.sessions.find(id)
	if p := h.sessions.policy(s); p[event.ToolEdit] != event.RuleRefuse || p[event.ToolExecute] != event.RuleAuto {
		t.Fatalf("policy: %v", p)
	}
	h.command(t, id, "stop", "")
}

func TestFailedReopenWritesErrorThenClosed(t *testing.T) {
	h := newHost(t)
	id := h.idle(t)
	h.command(t, id, "stop", "")
	h.harnesses = nil
	w := h.post(t, "/v1/sessions/"+string(id)+"/prompts", promptBody)
	if w.Code != protocol.StatusConflict {
		t.Fatalf("failed reopen: %d", w.Code)
	}
	events, err := h.events.SessionEvents(id)
	if err != nil {
		t.Fatal(err)
	}
	end := len(events)
	if events[end-3].Kind != event.KindSessionStarted || events[end-2].Kind != event.KindError || events[end-1].Kind != event.KindSessionEnded {
		t.Fatalf("failed reopen Events: %v", events)
	}
	if got := events[end-1].Payload.(*event.SessionEnded).Reason; got != event.EndFailed {
		t.Fatalf("reason: %s", got)
	}
}
