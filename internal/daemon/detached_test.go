package daemon

import (
	"slices"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// bare is a Session in the registry with no Harness behind it, for a test that
// writes its Events by hand.
func (h *host) bare(id event.SessionID) *Session {
	s := &Session{id: id, cancel: func() {}}
	s.sink = &sink{d: h.Daemon, s: s}
	h.sessions.add(s)
	return s
}

// waitReaders waits until the Daemon counts this many open Event streams, which is
// the moment a stream that closed has written whatever it was going to.
func (h *host) waitReaders(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		h.attaching.Lock()
		got := h.readers
		h.attaching.Unlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d Event streams open, want %d", got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Behaviour 2's record. The Daemon writes HubDetached when its last reader goes,
// and HubAttached when one comes back, and the reader that came back is sent it.
func TestALiveSessionRecordsTheHubLeavingAndComingBack(t *testing.T) {
	h := newHost(t)
	_, first := h.stream(t)
	id := h.idle(t)

	first.close()
	h.waitReaders(t, 0)

	_, second := h.stream(t)
	var e protocol.Event
	second.nextNamed(t, protocol.FrameEvent).decode(t, &e)
	if e.Kind != string(event.KindHubAttached) || e.Session != string(id) {
		t.Fatalf("the returning reader was sent %+v, want HubAttached for %s", e, id)
	}

	want := []string{"SessionStarted", "SessionReady", "HubDetached", "HubAttached"}
	if got := h.kinds(t); !slices.Equal(got, want) {
		t.Fatalf("log = %v, want %v", got, want)
	}
}

// The pair brackets a gap, and the first reader after boot closes none.
func TestTheFirstReaderWritesNoHubAttached(t *testing.T) {
	h := newHost(t)
	h.idle(t)

	_, r := h.stream(t)
	r.nextNamed(t, protocol.FrameVendors)

	if got := h.kinds(t); slices.Contains(got, "HubAttached") {
		t.Fatalf("log = %v, and no Hub had left", got)
	}
}

// A Hub that reconnects before its old stream is seen to drop never left, so only
// the last stream to close writes HubDetached, and it writes it once.
func TestOverlappingStreamsWriteOneHubDetached(t *testing.T) {
	h := newHost(t)
	_, first := h.stream(t)
	_, second := h.stream(t)
	h.idle(t)

	first.close()
	h.waitReaders(t, 1)
	if got := h.kinds(t); slices.Contains(got, "HubDetached") {
		t.Fatalf("log = %v, and a reader is still attached", got)
	}

	second.close()
	h.waitReaders(t, 0)
	want := []string{"SessionStarted", "SessionReady", "HubDetached"}
	if got := h.kinds(t); !slices.Equal(got, want) {
		t.Fatalf("log = %v, want %v", got, want)
	}
}

// An ended Session has no gap to record, because nothing is happening in it.
func TestAnEndedSessionRecordsNoHub(t *testing.T) {
	h := newHost(t)
	_, r := h.stream(t)
	id := h.idle(t)
	h.command(t, id, "stop", "")
	h.waitState(t, id, "Ended")

	r.close()
	h.waitReaders(t, 0)

	if got := h.kinds(t); slices.Contains(got, "HubDetached") {
		t.Fatalf("log = %v, and the Session had ended", got)
	}
}
