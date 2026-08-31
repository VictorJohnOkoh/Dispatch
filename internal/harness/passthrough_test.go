package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/vendors"
)

const testModel = "capstone/qwen3.5:9b"

// vendorFake answers the two calls a passthrough Session makes, and records the
// request bodies so a test can see the conversation the Adapter posted. No test in
// this file opens a socket.
type vendorFake struct {
	models  string // the body of GET /v1/models, or "" for the one Model above
	stream  string // the SSE body of POST /v1/chat/completions
	status  int    // the status of the chat call, 0 meaning 200
	halting bool   // hold the stream open after stream, until the request is cancelled

	posted []chatRequest
}

func (v *vendorFake) RoundTrip(req *http.Request) (*http.Response, error) {
	answer := func(status int, body io.ReadCloser) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: body, Request: req}, nil
	}

	switch req.URL.Path {
	case "/v1/models":
		body := v.models
		if body == "" {
			body = `{"data":[{"id":"` + testModel + `"}]}`
		}
		return answer(http.StatusOK, io.NopCloser(strings.NewReader(body)))

	case "/v1/chat/completions":
		var posted chatRequest
		if err := json.NewDecoder(req.Body).Decode(&posted); err != nil {
			return nil, err
		}
		v.posted = append(v.posted, posted)

		if v.status != 0 && v.status != http.StatusOK {
			return answer(v.status, io.NopCloser(strings.NewReader(`{"error":"no such model"}`)))
		}
		if v.halting {
			return answer(http.StatusOK, &haltingBody{ctx: req.Context(), head: strings.NewReader(v.stream)})
		}
		return answer(http.StatusOK, io.NopCloser(strings.NewReader(v.stream)))
	}
	return nil, errors.New("no fixture for " + req.URL.Path)
}

// haltingBody serves its head and then blocks, which is a Vendor that has sent
// some of a message and has not finished. Only cancelling the request ends it.
type haltingBody struct {
	ctx  context.Context
	head *strings.Reader
}

func (b *haltingBody) Read(p []byte) (int, error) {
	if b.head.Len() > 0 {
		return b.head.Read(p)
	}
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b *haltingBody) Close() error { return nil }

// startPassthrough starts a Session against v and fails the test if Spawn or Files
// is touched, because passthrough spawns nothing and writes no files.
func startPassthrough(t *testing.T, v *vendorFake) (Run, *recorder) {
	t.Helper()

	sink := newRecorder(t)
	spec := SessionSpec{
		Session: "s1",
		Model:   testModel,
		Vendor:  vendors.Endpoint{Kind: vendors.Ollama, Base: "http://127.0.0.1:11434"},
		Dir:     t.TempDir(),
		Spawn: func(context.Context, Launch) (Pipes, error) {
			t.Error("passthrough called Spawn, so a process exists")
			return Pipes{}, errors.New("no process")
		},
	}

	run, err := NewPassthrough(v).Start(context.Background(), spec, sink)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return run, sink
}

// promptAndDrain submits one Prompt and waits for the reader to finish with it, so
// everything the Sink received is on the test's goroutine afterwards.
func promptAndDrain(t *testing.T, run Run) {
	t.Helper()
	if err := run.Prompt(context.Background(), "hello"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	drain(t, run)
}

// drain joins the one goroutine a Run owns. It reaches inside the Run because the
// Sink alone cannot say when the reader stopped: a truncated stream ends a Prompt
// with an Error and no Completed.
func drain(t *testing.T, run Run) {
	t.Helper()
	r := run.(*ptRun)
	r.mu.Lock()
	done := r.done
	r.mu.Unlock()
	if done != nil {
		<-done
	}
}

const helloStream = `data: {"choices":[{"delta":{"content":"He"}}]}

data: {"choices":[{"delta":{"content":"llo"}}]}

data: {"choices":[{"delta":{},"finish_reason":"stop"}]}

data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":2,"total_tokens":14,"prompt_tokens_details":{"cached_tokens":8}}}

data: [DONE]

`

func TestPassthroughDeclaresNoGates(t *testing.T) {
	caps := NewPassthrough(nil).Capabilities()

	if caps.Tools {
		t.Error("passthrough declares tools")
	}
	for kind, gated := range caps.Gates {
		if gated {
			t.Errorf("passthrough declares a Gate for %s", event.ToolKind(kind))
		}
	}
}

func TestPassthroughTurnsStreamedTokensIntoAnAssistantMessage(t *testing.T) {
	run, sink := startPassthrough(t, &vendorFake{stream: helloStream})
	promptAndDrain(t, run)

	if got := sink.joined(); got != "Hello" {
		t.Errorf("message %q, want %q", got, "Hello")
	}
	if len(sink.messages) != 3 {
		t.Fatalf("want two Deltas and a close, got %d Message calls", len(sink.messages))
	}
	last := sink.messages[len(sink.messages)-1]
	if !last.end || last.text != "" {
		t.Errorf("the message closes with %+v, want an empty final call", last)
	}
	if len(sink.reasoning) != 0 {
		t.Errorf("want no Reasoning, got %d calls", len(sink.reasoning))
	}
}

func TestPassthroughWritesTheStopReasonAndTheUsage(t *testing.T) {
	run, sink := startPassthrough(t, &vendorFake{stream: helloStream})
	promptAndDrain(t, run)

	if got := sink.stopReason(); got != "stop" {
		t.Errorf("stop reason %q, want %q", got, "stop")
	}
	want := event.Usage{Input: 12, Output: 2, CacheRead: 8, Total: 14}
	if got := sink.completed[0].usage; got != want {
		t.Errorf("usage %+v, want %+v", got, want)
	}
}

func TestPassthroughGivesReasoningItsOwnEvent(t *testing.T) {
	stream := `data: {"choices":[{"delta":{"reasoning_content":"hmm"}}]}

data: {"choices":[{"delta":{"content":"Hello"}}]}

data: {"choices":[{"delta":{},"finish_reason":"stop"}]}

data: [DONE]

`
	run, sink := startPassthrough(t, &vendorFake{stream: stream})
	promptAndDrain(t, run)

	if len(sink.reasoning) != 1 || sink.reasoning[0].text != "hmm" {
		t.Fatalf("reasoning %+v, want one call carrying hmm", sink.reasoning)
	}
	if got := sink.joined(); got != "Hello" {
		t.Errorf("message %q, want %q", got, "Hello")
	}
}

func TestPassthroughReportsAVendorErrorAndCompletesThePrompt(t *testing.T) {
	stream := `data: {"choices":[{"delta":{"content":"He"}}]}

{"error":"llama runner process has terminated"}
`
	run, sink := startPassthrough(t, &vendorFake{stream: stream})
	promptAndDrain(t, run)

	if len(sink.failures) != 1 {
		t.Fatalf("want one Error, got %+v", sink.failures)
	}
	if sink.failures[0].code != event.ErrVendor {
		t.Errorf("error code %q, want %q", sink.failures[0].code, event.ErrVendor)
	}
	if !strings.Contains(sink.failures[0].msg, "llama runner") {
		t.Errorf("the Error lost the Vendor's own words: %q", sink.failures[0].msg)
	}
	// The Session stays usable, so the Prompt is bounded and the message is closed.
	if got := sink.stopReason(); got != stopError {
		t.Errorf("stop reason %q, want %q", got, stopError)
	}
	if last := sink.messages[len(sink.messages)-1]; !last.end {
		t.Error("the message was left open on a Session that stays usable")
	}
}

func TestPassthroughLeavesATruncatedMessageOpen(t *testing.T) {
	stream := `data: {"choices":[{"delta":{"content":"He"}}]}

`
	run, sink := startPassthrough(t, &vendorFake{stream: stream})
	promptAndDrain(t, run)

	if len(sink.failures) != 1 || sink.failures[0].code != event.ErrStreamTruncated {
		t.Fatalf("want one stream_truncated Error, got %+v", sink.failures)
	}
	if len(sink.completed) != 0 {
		t.Errorf("a truncated stream completed the Prompt: %+v", sink.completed)
	}
	if last := sink.messages[len(sink.messages)-1]; last.end {
		t.Error("a torn message was closed, so it reads as finished")
	}
}

func TestInterruptEndsThePromptAndLeavesTheSessionUsable(t *testing.T) {
	head := `data: {"choices":[{"delta":{"content":"He"}}]}

`
	run, sink := startPassthrough(t, &vendorFake{stream: head, halting: true})

	if err := run.Prompt(context.Background(), "hello"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if err := run.Interrupt(context.Background()); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}

	if got := sink.stopReason(); got != stopInterrupted {
		t.Errorf("stop reason %q, want %q", got, stopInterrupted)
	}
	if len(sink.failures) != 0 {
		t.Errorf("an interrupt was reported as a fault: %+v", sink.failures)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestPassthroughCarriesTheConversationForward(t *testing.T) {
	v := &vendorFake{stream: helloStream}
	run, _ := startPassthrough(t, v)

	for _, prompt := range []string{"first", "second"} {
		if err := run.Prompt(context.Background(), prompt); err != nil {
			t.Fatalf("Prompt %q: %v", prompt, err)
		}
		drain(t, run)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if len(v.posted) != 2 {
		t.Fatalf("want two posts, got %d", len(v.posted))
	}
	second := v.posted[1].Messages
	want := []chatMessage{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "Hello"},
		{Role: "user", Content: "second"},
	}
	if len(second) != len(want) {
		t.Fatalf("the second Prompt carried %+v, want %+v", second, want)
	}
	for i := range want {
		if second[i] != want[i] {
			t.Errorf("message %d is %+v, want %+v", i, second[i], want[i])
		}
	}
	if !v.posted[0].Stream || !v.posted[0].StreamOptions.IncludeUsage {
		t.Error("the Prompt did not ask for a stream carrying the usage")
	}
}

func TestStartRefusesAModelTheVendorDoesNotServe(t *testing.T) {
	v := &vendorFake{models: `{"data":[{"id":"some/other-model"}]}`}
	spec := SessionSpec{Model: testModel, Vendor: vendors.Endpoint{Base: "http://127.0.0.1:11434"}}

	_, err := NewPassthrough(v).Start(context.Background(), spec, newRecorder(t))
	if !errors.Is(err, vendors.ErrModelNotFound) {
		t.Fatalf("Start: %v, want %v", err, vendors.ErrModelNotFound)
	}
}

func TestPromptCarriesTheVendorsRefusalWhenTheCallFails(t *testing.T) {
	run, _ := startPassthrough(t, &vendorFake{status: http.StatusNotFound})

	err := run.Prompt(context.Background(), "hello")
	if err == nil {
		t.Fatal("Prompt accepted a Prompt the Vendor refused")
	}
	if !strings.Contains(err.Error(), "no such model") {
		t.Errorf("the error lost the Vendor's own words: %v", err)
	}
}

func TestPassthroughRefusesASecondPromptWhileOneIsInFlight(t *testing.T) {
	run, _ := startPassthrough(t, &vendorFake{stream: "", halting: true})

	if err := run.Prompt(context.Background(), "first"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if err := run.Prompt(context.Background(), "second"); err == nil {
		t.Error("a second Prompt was accepted while one was in flight")
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestPassthroughSendsTheTokenWhenTheVendorWantsOne(t *testing.T) {
	var seen string
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		seen = req.Header.Get("Authorization")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(bytes.NewReader([]byte(`{"data":[{"id":"` + testModel + `"}]}`))),
			Request:    req,
		}, nil
	})
	spec := SessionSpec{
		Model:  testModel,
		Vendor: vendors.Endpoint{Base: "http://127.0.0.1:11434", Token: "secret"},
	}

	if _, err := NewPassthrough(rt).Start(context.Background(), spec, newRecorder(t)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if seen != "Bearer secret" {
		t.Errorf("Authorization %q, want %q", seen, "Bearer secret")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
