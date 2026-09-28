package hub

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// ADR 0004's one way out of Incompatible is the user commanding a retry. These
// drive that command against a stream that is open, because Host State lives in
// that stream and nowhere else.

func retry(t *testing.T, url, host string) int {
	t.Helper()
	resp, err := http.Post(url+"/v1/hosts/"+host+"/retry", "", nil)
	if err != nil {
		t.Fatalf("the retry: %v", err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// A retry dials the Host once. The Daemon still refuses, so the Host goes back to
// Incompatible and the Hub stops again, which is one more refused Handshake in
// the Daemon's log and no more than that.
func TestARetryDialsAnIncompatibleHostOnce(t *testing.T) {
	var seen refusals
	h := quick([]Host{{ID: "desk"}}, oldDaemon(&seen))
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	var said []protocol.HostState
	watch(t, srv.URL, func(f protocol.HostStateFrame) bool {
		said = append(said, f.State)
		if f.State != protocol.Incompatible {
			return false
		}
		if len(said) == 2 {
			if code := retry(t, srv.URL, "desk"); code != http.StatusAccepted {
				t.Fatalf("the retry was answered %d", code)
			}
			return false
		}
		// The backoff curve on this Hub is 5ms, so a Host still being retried would
		// have dialled many times over.
		time.Sleep(200 * time.Millisecond)
		return true
	})

	want := []protocol.HostState{protocol.Connecting, protocol.Incompatible, protocol.Connecting, protocol.Incompatible}
	if len(said) != len(want) {
		t.Fatalf("the Hub said %v, want %v", said, want)
	}
	for i := range want {
		if said[i] != want[i] {
			t.Errorf("frame %d said %s, want %s", i+1, said[i], want[i])
		}
	}
	if dials, _ := seen.seen(); dials != 2 {
		t.Errorf("the Host was dialled %d times for one retry, want 2", dials)
	}
}

// A retry reaches a Host that has come back as Ready, which is what the user
// wanted when they fixed the version on the Host.
func TestARetryReachesAHostThatWasFixed(t *testing.T) {
	var fixed atomic.Bool
	old := oldDaemon(&refusals{})
	h := quick([]Host{{ID: "desk"}}, dialFn(func(ctx context.Context, id hostset.HostID) (net.Conn, error) {
		if fixed.Load() {
			return liveDaemon(ctx, id)
		}
		return old(ctx, id)
	}))
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	var said []protocol.HostState
	watch(t, srv.URL, func(f protocol.HostStateFrame) bool {
		said = append(said, f.State)
		if f.State == protocol.Incompatible {
			fixed.Store(true)
			retry(t, srv.URL, "desk")
		}
		return f.State == protocol.Ready
	})

	want := []protocol.HostState{protocol.Connecting, protocol.Incompatible, protocol.Connecting, protocol.Ready}
	if len(said) != len(want) {
		t.Fatalf("the Hub said %v, want %v", said, want)
	}
}

// The user retries one Host. Every other Incompatible Host stays where it is.
func TestARetryLeavesTheOtherHostsAlone(t *testing.T) {
	var desk, attic refusals
	h := quick([]Host{{ID: "desk"}, {ID: "attic"}}, dialFn(func(ctx context.Context, id hostset.HostID) (net.Conn, error) {
		if id == "desk" {
			return oldDaemon(&desk)(ctx, id)
		}
		return oldDaemon(&attic)(ctx, id)
	}))
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	incompatible := map[string]int{}
	watch(t, srv.URL, func(f protocol.HostStateFrame) bool {
		if f.State != protocol.Incompatible {
			return false
		}
		incompatible[f.Host]++
		if incompatible["desk"] == 1 && incompatible["attic"] == 1 {
			retry(t, srv.URL, "desk")
		}
		return incompatible["desk"] == 2
	})
	time.Sleep(50 * time.Millisecond)

	if dials, _ := attic.seen(); dials != 1 {
		t.Errorf("a retry on desk dialled attic %d times", dials)
	}
}

// Only an Incompatible Host takes a retry. A Host that is Ready or on the backoff
// curve is already being worked on, and a retry must not add a dial to that.
func TestARetryOnAHostThatIsNotIncompatibleChangesNothing(t *testing.T) {
	var dials atomic.Int32
	h := quick([]Host{{ID: "desk"}}, dialFn(func(ctx context.Context, id hostset.HostID) (net.Conn, error) {
		dials.Add(1)
		return liveDaemon(ctx, id)
	}))
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	var said []protocol.HostState
	watch(t, srv.URL, func(f protocol.HostStateFrame) bool {
		said = append(said, f.State)
		if f.State != protocol.Ready {
			return false
		}
		retry(t, srv.URL, "desk")
		time.Sleep(100 * time.Millisecond)
		return true
	})

	if len(said) != 2 {
		t.Errorf("the Hub said %v", said)
	}
	if n := dials.Load(); n != 1 {
		t.Errorf("a retry on a Ready Host dialled it %d times", n)
	}
}

// A retry names one configured Host, and a name the Hub does not hold is the
// Client's mistake.
func TestARetryOnAnUnknownHostIsNotFound(t *testing.T) {
	h := quick([]Host{{ID: "desk"}}, oldDaemon(&refusals{}))
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	if code := retry(t, srv.URL, "attic"); code != http.StatusNotFound {
		t.Errorf("a retry on a Host that is not configured was answered %d", code)
	}
}
