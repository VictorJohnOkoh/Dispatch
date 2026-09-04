package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// chatFake is the Vendor's completion surface, as a passthrough Session reaches
// it. Only /v1/chat/completions exists, because that is the only call one makes.
type chatFake struct {
	mu      sync.Mutex
	stream  string // the SSE body to serve
	halt    bool   // hold the body open after stream, until the request is cancelled
	status  int    // the status to answer with, 0 meaning 200
	entered chan struct{}
	proceed chan struct{}
}

func (c *chatFake) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path != "/v1/chat/completions" {
		return nil, errors.New("no fixture for " + req.URL.Path)
	}
	if c.entered != nil {
		close(c.entered)
		<-c.proceed
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.status != 0 {
		return &http.Response{StatusCode: c.status, Header: http.Header{}, Request: req,
			Body: io.NopCloser(strings.NewReader(`{"error":"no such model"}`))}, nil
	}
	var served io.ReadCloser = io.NopCloser(strings.NewReader(c.stream))
	if c.halt {
		served = &halting{ctx: req.Context(), head: strings.NewReader(c.stream)}
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: served, Request: req}, nil
}

func (c *chatFake) serve(stream string, halt bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stream, c.halt = stream, halt
}

// halting serves its head and then blocks, which is a Vendor that has sent some of
// a message and has not finished. Only cancelling the request ends it.
type halting struct {
	ctx  context.Context
	head *strings.Reader
}

func (b *halting) Read(p []byte) (int, error) {
	if b.head.Len() > 0 {
		return b.head.Read(p)
	}
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b *halting) Close() error { return nil }

// helloStream is one whole Prompt: two text chunks, a finish reason and the usage.
const helloStream = `data: {"choices":[{"delta":{"content":"He"}}]}

data: {"choices":[{"delta":{"content":"llo"}}]}

data: {"choices":[{"delta":{},"finish_reason":"stop"}]}

data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":2,"total_tokens":14,"prompt_tokens_details":{"cached_tokens":8}}}

data: [DONE]
`

// halfStream is a Prompt that started and did not finish: text, and no terminator
// and no finish reason after it. It is served with halt, so the body stays open.
const halfStream = `data: {"choices":[{"delta":{"content":"He"}}]}

data: {"choices":[{"delta":{"content":"llo"}}]}
`

const promptBody = `{"text":"hello"}`

// idle starts a Session and waits for it to be ready to take a Prompt.
func (h *host) idle(t *testing.T) event.SessionID {
	t.Helper()
	answer := h.started(t, h.post(t, "/v1/sessions", startBody))
	h.waitState(t, answer.Session, "Idle")
	return answer.Session
}

// command posts one Session command and checks it was accepted.
func (h *host) command(t *testing.T, id event.SessionID, path, body string) {
	t.Helper()
	w := h.post(t, "/v1/sessions/"+string(id)+"/"+path, body)
	if w.Code != protocol.StatusAccepted {
		t.Fatalf("%s: status %d: %s", path, w.Code, w.Body.String())
	}
}

// payload decodes the one Event of that Kind in a Session's page, and fails when
// the Session has none.
func (h *host) payload(t *testing.T, id event.SessionID, kind event.Kind, into any) {
	t.Helper()
	for _, e := range h.page(t, "/v1/sessions/"+string(id)+"/events") {
		if e.Kind == string(kind) {
			if err := json.Unmarshal(e.Payload, into); err != nil {
				t.Fatalf("%s payload: %v", kind, err)
			}
			return
		}
	}
	t.Fatalf("Session %s has no %s", id, kind)
}

// The milestone's watch. A Prompt is submitted, the assistant's text arrives as
// Deltas on GET /v1/events, and PromptSubmitted and PromptCompleted bound it.
func TestAPromptRunsFromSubmittedToCompleted(t *testing.T) {
	h := newHost(t)
	h.chat.serve(helloStream, false)
	_, out := h.stream(t)

	id := h.idle(t)
	h.command(t, id, "prompts", promptBody)
	h.waitState(t, id, "Idle")

	want := []string{"SessionStarted", "SessionReady", "PromptSubmitted", "AssistantMessage", "PromptCompleted"}
	if got := h.kinds(t); len(got) != len(want) {
		t.Fatalf("log = %v, want %v", got, want)
	} else {
		for i, kind := range want {
			if got[i] != kind {
				t.Fatalf("log = %v, want %v", got, want)
			}
		}
	}

	// The text arrives as Deltas on the stream. The first chunk opened the Event
	// and travelled in it, so the Deltas are the rest, and the final one carries the
	// whole text and replaces rather than appends.
	var seen []protocol.Delta
	for {
		f := out.nextNamed(t, protocol.FrameDelta)
		var d protocol.Delta
		if err := json.Unmarshal([]byte(f.data), &d); err != nil {
			t.Fatalf("delta: %v", err)
		}
		seen = append(seen, d)
		if d.Final {
			break
		}
	}
	if len(seen) != 2 {
		t.Fatalf("%d Deltas, want llo and the final one: %+v", len(seen), seen)
	}
	if seen[0].Text != "llo" || seen[0].N != 2 {
		t.Errorf("a Delta appends at N: %+v", seen[0])
	}
	if final := seen[1]; final.Text != "Hello" || final.N != 5 {
		t.Errorf("the final Delta = %+v, want the whole text at its own length", final)
	}

	// PromptCompleted carries the stop reason and the usage, and the message is
	// whole in the log.
	var done event.PromptCompleted
	h.payload(t, id, event.KindPromptCompleted, &done)
	if done.StopReason != "stop" {
		t.Errorf("stop reason = %q", done.StopReason)
	}
	if want := (event.Usage{Input: 12, Output: 2, CacheRead: 8, Total: 14}); done.Usage != want {
		t.Errorf("usage = %+v, want %+v", done.Usage, want)
	}
	if text, complete := h.message(t, 4); text != "Hello" || !complete {
		t.Errorf("the message is %q, complete %v", text, complete)
	}
}

// The Session records the Vendor and the Model that served it, which is what makes
// a usage split readable later: the three Vendors disagree on it.
func TestTheSessionRecordsTheVendorAndModelThatServedIt(t *testing.T) {
	h := newHost(t)
	id := h.idle(t)

	var started event.SessionStarted
	h.payload(t, id, event.KindSessionStarted, &started)
	if started.Vendor != h.vendor.Endpoint().Base || started.Model != "qwen3:8b" {
		t.Errorf("SessionStarted = %+v, want the Vendor and Model that serve it", started)
	}

	var ready event.SessionReady
	h.payload(t, id, event.KindSessionReady, &ready)
	if ready.Model != "qwen3:8b" {
		t.Errorf("SessionReady names the Model %q", ready.Model)
	}
}

// A Prompt on a Starting Session is refused, and that refusal is the reason the
// fold ships in the Daemon.
func TestAPromptOnAStartingSessionIsRefused(t *testing.T) {
	h := newHost(t)
	h.vendor.gate = make(chan struct{})
	answer := h.started(t, h.post(t, "/v1/sessions", startBody))
	h.waitState(t, answer.Session, "Starting")

	r := h.refusal(t, h.post(t, "/v1/sessions/"+string(answer.Session)+"/prompts", promptBody), protocol.StatusConflict)
	if r.Reason != protocol.ReasonState {
		t.Errorf("reason = %q", r.Reason)
	}
	if !strings.Contains(r.Detail, "Starting") || !strings.Contains(r.Detail, "Idle") {
		t.Errorf("detail = %q, want the State it is in and the one it needs", r.Detail)
	}
	if got := h.kinds(t); len(got) != 1 {
		t.Errorf("the refusal wrote %d Events, want none", len(got)-1)
	}

	close(h.vendor.gate)
}

// A second Prompt while the first is in flight is refused, because PromptSubmitted
// is written before the Harness is asked.
func TestASecondPromptWhileWorkingIsRefused(t *testing.T) {
	h := newHost(t)
	h.chat.serve(halfStream, true)
	id := h.idle(t)
	h.command(t, id, "prompts", promptBody)
	h.waitState(t, id, "Working")

	r := h.refusal(t, h.post(t, "/v1/sessions/"+string(id)+"/prompts", promptBody), protocol.StatusConflict)
	if r.Reason != protocol.ReasonState {
		t.Errorf("reason = %q", r.Reason)
	}
	h.command(t, id, "stop", "")
}

// The two ways a Prompt is the request's own fault, and both are 422.
func TestAMalformedPromptIsRefusedAsTheRequestsOwnFault(t *testing.T) {
	for _, c := range []struct{ name, body string }{
		{"body", `{"text":`},
		{"no text", `{}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHost(t)
			id := h.idle(t)
			r := h.refusal(t, h.post(t, "/v1/sessions/"+string(id)+"/prompts", c.body), protocol.StatusUnprocessable)
			if r.Reason != protocol.ReasonMalformed {
				t.Errorf("reason = %q", r.Reason)
			}
		})
	}
}

// A Prompt the Vendor would not take is bounded all the same, because
// PromptSubmitted is in the log and a Session left Working refuses every Prompt
// after it.
func TestAPromptTheVendorRefusesIsStillBounded(t *testing.T) {
	h := newHost(t)
	id := h.idle(t)
	h.chat.mu.Lock()
	h.chat.status = http.StatusNotFound
	h.chat.mu.Unlock()

	w := h.post(t, "/v1/sessions/"+string(id)+"/prompts", promptBody)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want the Host failing rather than the request being wrong", w.Code)
	}

	view := h.waitState(t, id, "Idle")
	if view.EndReason != "" {
		t.Errorf("the Session ended with %q, want it still usable", view.EndReason)
	}
	var done event.PromptCompleted
	h.payload(t, id, event.KindPromptCompleted, &done)
	if done.StopReason != event.StopError {
		t.Errorf("stop reason = %q, want %q", done.StopReason, event.StopError)
	}
	if got := h.kinds(t); len(got) != 5 || got[3] != "Error" || got[4] != "PromptCompleted" {
		t.Errorf("log = %v, want the Prompt bounded by an Error and a PromptCompleted", got)
	}
}

// Interrupt abandons the Prompt and keeps the Session: it goes back to Idle, not
// Ended, and the next Prompt runs.
func TestInterruptAbandonsThePromptAndKeepsTheSession(t *testing.T) {
	h := newHost(t)
	h.chat.serve(halfStream, true)
	id := h.idle(t)
	h.command(t, id, "prompts", promptBody)
	h.waitState(t, id, "Working")

	h.command(t, id, "interrupt", "")

	view := h.waitState(t, id, "Idle")
	if view.EndReason != "" {
		t.Errorf("the Session ended with %q", view.EndReason)
	}
	var done event.PromptCompleted
	h.payload(t, id, event.KindPromptCompleted, &done)
	if done.StopReason != event.StopInterrupted {
		t.Errorf("stop reason = %q, want %q", done.StopReason, event.StopInterrupted)
	}

	// The Session is usable, which is the whole difference between interrupt and
	// stop.
	h.chat.serve(helloStream, false)
	h.command(t, id, "prompts", promptBody)
	h.waitState(t, id, "Idle")
}

func TestInterruptCannotOvertakePromptRegistration(t *testing.T) {
	h := newHost(t)
	h.chat.serve(halfStream, true)
	h.chat.entered = make(chan struct{})
	h.chat.proceed = make(chan struct{})
	id := h.idle(t)

	promptDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		promptDone <- h.post(t, "/v1/sessions/"+string(id)+"/prompts", promptBody)
	}()
	<-h.chat.entered

	interruptDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		interruptDone <- h.post(t, "/v1/sessions/"+string(id)+"/interrupt", "")
	}()
	select {
	case <-interruptDone:
		t.Fatal("Interrupt returned before the Harness registered the Prompt")
	case <-time.After(50 * time.Millisecond):
	}

	close(h.chat.proceed)
	if w := <-promptDone; w.Code != protocol.StatusAccepted {
		t.Fatalf("prompt: status %d: %s", w.Code, w.Body.String())
	}
	if w := <-interruptDone; w.Code != protocol.StatusAccepted {
		t.Fatalf("interrupt: status %d: %s", w.Code, w.Body.String())
	}
	h.waitState(t, id, "Idle")
}

// Stop ends the Session with reason stopped. The Prompt in flight is not bounded
// and its message is left torn, because the user asked for this.
func TestStopEndsTheSessionWithReasonStopped(t *testing.T) {
	h := newHost(t)
	h.chat.serve(halfStream, true)
	id := h.idle(t)
	h.command(t, id, "prompts", promptBody)
	h.waitState(t, id, "Working")

	h.command(t, id, "stop", "")

	view := h.waitState(t, id, "Ended")
	if view.EndReason != event.EndStopped {
		t.Errorf("end reason = %q, want %q", view.EndReason, event.EndStopped)
	}
	want := []string{"SessionStarted", "SessionReady", "PromptSubmitted", "AssistantMessage", "SessionEnded"}
	got := h.kinds(t)
	if len(got) != len(want) {
		t.Fatalf("log = %v, want %v", got, want)
	}
	for i, kind := range want {
		if got[i] != kind {
			t.Fatalf("log = %v, want %v", got, want)
		}
	}
	if text, complete := h.message(t, 4); text != "Hello" || complete {
		t.Errorf("the message is %q, complete %v, want every byte it had and torn", text, complete)
	}

	// Every command on an ended Session is refused, and the slot it held is free.
	r := h.refusal(t, h.post(t, "/v1/sessions/"+string(id)+"/prompts", promptBody), protocol.StatusConflict)
	if r.Reason != protocol.ReasonState {
		t.Errorf("reason = %q", r.Reason)
	}
	h.idle(t)
}

// Load turned the Vendor's evictor off for the Session's life, so the end of that
// life is where the Model comes back. Without this the Model sits in VRAM until
// somebody restarts the Vendor.
func TestAStoppedSessionGivesItsModelBack(t *testing.T) {
	h := newHost(t)
	id := h.idle(t)

	h.command(t, id, "stop", "")
	h.waitState(t, id, "Ended")

	if got := h.vendor.unloads(); len(got) != 1 || got[0] != "qwen3:8b" {
		t.Errorf("Unload was called with %v, want [qwen3:8b]", got)
	}
}

// A stop on a Session that is still Starting ends it as stopped, and the launch it
// cancelled does not write a second end.
func TestStopWhileStartingEndsTheSessionOnce(t *testing.T) {
	h := newHost(t)
	h.vendor.gate = make(chan struct{})
	answer := h.started(t, h.post(t, "/v1/sessions", startBody))
	h.waitState(t, answer.Session, "Starting")

	h.command(t, answer.Session, "stop", "")
	close(h.vendor.gate)

	view := h.waitState(t, answer.Session, "Ended")
	if view.EndReason != event.EndStopped {
		t.Errorf("end reason = %q, want %q", view.EndReason, event.EndStopped)
	}
	if got := h.kinds(t); len(got) != 2 || got[1] != "SessionEnded" {
		t.Errorf("log = %v, want SessionStarted and one SessionEnded", got)
	}
}

// Interrupt is refused on a Session with no Prompt in flight, because there is
// nothing to abandon.
func TestInterruptWithNoPromptIsRefused(t *testing.T) {
	h := newHost(t)
	id := h.idle(t)

	r := h.refusal(t, h.post(t, "/v1/sessions/"+string(id)+"/interrupt", ""), protocol.StatusConflict)
	if r.Reason != protocol.ReasonState {
		t.Errorf("reason = %q", r.Reason)
	}
}

// The Approval Policy is refused for a Harness that runs no tools. Passthrough is
// the only one this build ships, so the endpoint exists and stays unexercised.
func TestAPolicyIsRefusedForAHarnessWithNoGates(t *testing.T) {
	h := newHost(t)
	id := h.idle(t)

	body := `{"policy":{"read":"auto","edit":"wait","execute":"wait","fetch":"auto","other":"auto"}}`
	r := h.refusal(t, h.post(t, "/v1/sessions/"+string(id)+"/policy", body), protocol.StatusUnprocessable)
	if r.Reason != protocol.ReasonNoGate {
		t.Errorf("reason = %q", r.Reason)
	}
	if got := h.kinds(t); len(got) != 2 {
		t.Errorf("the refusal wrote %d Events, want none", len(got)-2)
	}

	// A policy with a slot missing is the request itself being wrong.
	partial := `{"policy":{"read":"auto"}}`
	if r := h.refusal(t, h.post(t, "/v1/sessions/"+string(id)+"/policy", partial), protocol.StatusUnprocessable); r.Reason != protocol.ReasonMalformed {
		t.Errorf("a partial policy = %q", r.Reason)
	}
}

// A decision on a question nobody asked is refused. Passthrough holds no Tool
// Call, so a Session here is never Asking.
func TestADecisionWithNoQuestionIsRefused(t *testing.T) {
	h := newHost(t)
	id := h.idle(t)

	body := `{"toolCallId":"t-1","decision":"allowed"}`
	r := h.refusal(t, h.post(t, "/v1/sessions/"+string(id)+"/approvals", body), protocol.StatusConflict)
	if r.Reason != protocol.ReasonState {
		t.Errorf("reason = %q, want the State refusing before the question is looked for", r.Reason)
	}

	nonsense := `{"toolCallId":"t-1","decision":"maybe"}`
	if r := h.refusal(t, h.post(t, "/v1/sessions/"+string(id)+"/approvals", nonsense), protocol.StatusUnprocessable); r.Reason != protocol.ReasonMalformed {
		t.Errorf("a decision nobody knows = %q", r.Reason)
	}
}

// Every command on a Session this Host never had answers the same way.
func TestACommandOnAnUnknownSessionIsRefused(t *testing.T) {
	h := newHost(t)
	for _, c := range []struct{ path, body string }{
		{"prompts", promptBody},
		{"interrupt", ""},
		{"stop", ""},
		{"policy", `{"policy":{"read":"auto","edit":"auto","execute":"auto","fetch":"auto","other":"auto"}}`},
		{"approvals", `{"toolCallId":"t-1","decision":"allowed"}`},
	} {
		t.Run(c.path, func(t *testing.T) {
			r := h.refusal(t, h.post(t, "/v1/sessions/s-gone/"+c.path, c.body), protocol.StatusNoSession)
			if r.Reason != protocol.ReasonUnknownSession {
				t.Errorf("reason = %q", r.Reason)
			}
		})
	}
}

// The mux serves every endpoint, spelled as protocol spells them, so the two
// cannot drift.
func TestTheMuxServesEveryEndpoint(t *testing.T) {
	h := newHost(t)
	mux, ok := h.handler().(*http.ServeMux)
	if !ok {
		t.Fatalf("the handler is %T", h.handler())
	}
	for _, route := range protocol.Routes {
		method, path, _ := strings.Cut(route, " ")
		handler, pattern := mux.Handler(httptest.NewRequest(method, path, nil))
		if handler == nil || pattern != route {
			t.Errorf("%s is served by %q", route, pattern)
		}
	}
}
