package daemon

import (
	"testing"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
)

// live is a Session that has started, which is what an Adapter reports against.
func (h *host) live(t *testing.T) *Session {
	t.Helper()
	answer := h.started(t, h.post(t, "/v1/sessions", startBody))
	h.waitState(t, answer.Session, "Idle")
	return h.sessions.all[0]
}

// Text arrives as Deltas and lands in one Event, and the Event is open until the
// Adapter ends it.
func TestTheSinkKeepsOneEventOpenWhileItsTextArrives(t *testing.T) {
	h := newHost(t)
	k := &sink{d: h.Daemon, s: h.live(t)}

	k.Message("the ", false)
	k.Message("answer", false)
	if got := h.kinds(t); len(got) != 3 || got[2] != "AssistantMessage" {
		t.Fatalf("log = %v, want one AssistantMessage", got)
	}

	k.Message("", true)
	if got := h.kinds(t); len(got) != 3 {
		t.Fatalf("log = %v, want the same one Event", got)
	}
	if text, complete := h.message(t, 3); text != "the answer" || !complete {
		t.Errorf("text = %q, complete = %v", text, complete)
	}
}

// Reasoning and a message are two Events, and starting one closes the other, so
// one message never swallows the next.
func TestTheSinkClosesTheOpenEventWhenTheOtherKindStarts(t *testing.T) {
	h := newHost(t)
	k := &sink{d: h.Daemon, s: h.live(t)}

	k.Reasoning("thinking", false)
	k.Message("said", true)

	got := h.kinds(t)
	if len(got) != 4 || got[2] != "Reasoning" || got[3] != "AssistantMessage" {
		t.Fatalf("log = %v, want Reasoning then AssistantMessage", got)
	}
	if text, complete := h.message(t, 3); text != "thinking" || !complete {
		t.Errorf("the Reasoning was left %q, complete = %v", text, complete)
	}
}

// Every other call is one Event, and each one lands against its own Session.
func TestTheSinkWritesOneEventPerCall(t *testing.T) {
	h := newHost(t)
	k := &sink{d: h.Daemon, s: h.live(t)}

	k.ToolCallRequested("t1", "read", event.ToolRead, "read a file", nil)
	k.ToolCallEnded("t1", event.OutcomeOK, "two lines")
	k.Failed(event.ErrVendor, "the Vendor stopped answering")
	k.Completed("stop", event.Usage{Input: 10, Output: 4, Total: 14})

	want := []string{"SessionStarted", "SessionReady", "ToolCallRequested", "ToolCallEnded", "Error", "PromptCompleted"}
	got := h.kinds(t)
	if len(got) != len(want) {
		t.Fatalf("log = %v, want %v", got, want)
	}
	for i, kind := range want {
		if got[i] != kind {
			t.Fatalf("log = %v, want %v", got, want)
		}
	}
}

// An Adapter never learns that a write failed. The Session is cancelled instead,
// which is what its reader returns from.
func TestAWriteThatFailsCancelsTheSession(t *testing.T) {
	h := newHost(t)
	cancelled := make(chan struct{})
	s := &Session{id: "s-000000", cancel: func() { close(cancelled) }}
	h.events.Close()

	(&sink{d: h.Daemon, s: s}).Failed(event.ErrVendor, "anything")

	select {
	case <-cancelled:
	default:
		t.Error("the Session was left running with an Event log that refused it")
	}
}
