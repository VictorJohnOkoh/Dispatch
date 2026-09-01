package daemon

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// resume opens GET /v1/events carrying a Cursor, and the identity of the log that
// Cursor came from when one is given.
func (h *host) resume(t *testing.T, from protocol.Cursor, logID string) *reader {
	t.Helper()
	h.keepalive = 20 * time.Millisecond
	srv := httptest.NewServer(h.handler())
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/events", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set(protocol.CursorHeader, from.String())
	if logID != "" {
		req.Header.Set(protocol.LogHeader, logID)
	}

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("GET /v1/events: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	return newReader(t, resp)
}

// A Cursor taken while a message was still arriving is below that message, so the
// replay hands the message back and the Deltas that follow finish it. The Client
// sees the whole message, including the half that arrived after the Cursor.
func TestAReplayFromMidMessageReturnsTheMessageWhole(t *testing.T) {
	h := newHost(t)
	s := &Session{id: "s-open", cancel: func() {}}
	h.sessions.add(s)
	k := &sink{d: h.Daemon, s: s}
	k.Message("half a", false)

	// The message is open at Seq 1, so a reader that has read everything stands
	// at 0, which is a real Cursor and means the whole log.
	if at := h.events.Cursor(); at != 0 {
		t.Fatalf("Cursor with a message open at 1 = %d, want 0", at)
	}
	r := h.resume(t, 0, h.events.LogID())

	replayed := r.nextNamed(t, protocol.FrameEvent)
	var e protocol.Event
	replayed.decode(t, &e)
	if e.Seq != 1 || e.Kind != string(event.KindAssistantMessage) {
		t.Fatalf("replayed %+v, want the open message at 1", e)
	}
	if replayed.id != "" {
		t.Errorf("the replayed open message carries id %q, so the Cursor passed it", replayed.id)
	}

	k.Message(" message", true)
	final := r.nextNamed(t, protocol.FrameDelta)
	var delta protocol.Delta
	final.decode(t, &delta)
	if !delta.Final || delta.Text != "half a message" {
		t.Fatalf("final Delta = %+v", delta)
	}
	if final.id != "1" {
		t.Errorf("the final Delta carries id %q, want 1: the message is now whole", final.id)
	}
}

// A replay stops at the moment the reader joined, and what was written after it
// arrives once on the subscription rather than twice.
func TestAReplayHandsEachEventOverExactlyOnce(t *testing.T) {
	h := newHost(t)
	s := &Session{id: "s-old", cancel: func() {}}
	h.sessions.add(s)
	for _, text := range []string{"one", "two"} {
		if _, err := h.write(s, event.KindPromptSubmitted, &event.PromptSubmitted{Text: text}); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	r := h.resume(t, 0, "")
	for _, want := range []uint64{1, 2} {
		var e protocol.Event
		r.nextNamed(t, protocol.FrameEvent).decode(t, &e)
		if e.Seq != want {
			t.Fatalf("replayed Seq %d, want %d", e.Seq, want)
		}
	}

	if _, err := h.write(s, event.KindPromptSubmitted, &event.PromptSubmitted{Text: "three"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	var e protocol.Event
	r.nextNamed(t, protocol.FrameEvent).decode(t, &e)
	if e.Seq != 3 {
		t.Fatalf("the live Frame is Seq %d, want 3: an Event was sent twice", e.Seq)
	}
}

// A Cursor above anything the log ever allotted is answered with a Frame and not
// an error, so the Host stays Ready and the connection carries on.
func TestACursorAboveTheHighWaterMarkIsAResyncAndTheStreamStaysOpen(t *testing.T) {
	h := newHost(t)
	r := h.resume(t, 9999, "")

	var hello protocol.Hello
	r.nextNamed(t, protocol.FrameHello).decode(t, &hello)
	if hello.LogID != h.events.LogID() {
		t.Errorf("hello names log %q, want %q", hello.LogID, h.events.LogID())
	}

	var resync protocol.Resync
	r.nextNamed(t, protocol.FrameResync).decode(t, &resync)
	if resync.LogID != h.events.LogID() || resync.Latest != h.events.Latest() {
		t.Errorf("resync = %+v", resync)
	}

	// The connection is still open, which is the whole point of answering with a
	// Frame: a Host serving a resync is working perfectly.
	if next := r.nextNamed(t, protocol.FrameVendors); next.data == "" {
		t.Error("the stream closed after the resync")
	}
}

// A Cursor is a Sequence Number one log allotted, and it means nothing in another.
func TestACursorFromAnotherLogIsAResync(t *testing.T) {
	h := newHost(t)
	s := &Session{id: "s-old", cancel: func() {}}
	h.sessions.add(s)
	if _, err := h.write(s, event.KindPromptSubmitted, &event.PromptSubmitted{Text: "one"}); err != nil {
		t.Fatalf("write: %v", err)
	}

	r := h.resume(t, 0, "a-log-that-is-not-this-one")
	r.nextNamed(t, protocol.FrameResync)

	// Nothing was replayed, because the Cursor named a Sequence Number this log
	// never gave out. The next Frame is the vendors one that follows every hello.
	if next := r.next(t); next.name != string(protocol.FrameVendors) {
		t.Errorf("Frame after the resync is %q, want vendors", next.name)
	}
}

// The same Cursor with the log's own identity is served, which is what says the
// resync above came from the identity and not from the number.
func TestACursorCarryingThisLogsIdentityReplays(t *testing.T) {
	h := newHost(t)
	s := &Session{id: "s-old", cancel: func() {}}
	h.sessions.add(s)
	if _, err := h.write(s, event.KindPromptSubmitted, &event.PromptSubmitted{Text: "one"}); err != nil {
		t.Fatalf("write: %v", err)
	}

	r := h.resume(t, 0, h.events.LogID())
	var e protocol.Event
	r.nextNamed(t, protocol.FrameEvent).decode(t, &e)
	if e.Seq != 1 {
		t.Fatalf("replayed %+v, want Seq 1", e)
	}
}

// Every GET answers with the Cursor it read at, or a Client that opens the stream
// afterwards silently loses whatever fell between the two calls.
func TestTheGetAnswersCarryTheCursorTheyReadAt(t *testing.T) {
	h := newHost(t)
	id := h.started(t, h.post(t, "/v1/sessions", startBody)).Session
	h.waitState(t, id, "Idle")

	var sessions struct {
		Cursor protocol.Cursor `json:"cursor"`
	}
	decodeGet(t, h.Daemon, "/v1/sessions", &sessions)
	if sessions.Cursor != h.events.Cursor() {
		t.Errorf("GET /v1/sessions cursor = %d, want %d", sessions.Cursor, h.events.Cursor())
	}

	var events struct {
		Cursor protocol.Cursor `json:"cursor"`
	}
	decodeGet(t, h.Daemon, "/v1/sessions/"+string(id)+"/events", &events)
	if events.Cursor != h.events.Cursor() {
		t.Errorf("GET events cursor = %d, want %d", events.Cursor, h.events.Cursor())
	}
}
