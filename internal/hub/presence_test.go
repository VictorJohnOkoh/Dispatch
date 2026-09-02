package hub

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// Presence is connection liveness, so these drive the connection: a Host that
// blinks, a Host that goes quiet, and a Host that flaps. The timings are the
// Hub's own fields, shortened, because ADR 0004's curve is minutes long and the
// rules it encodes are what is under test rather than the numbers.

// watch reads one merged stream and hands every host frame to a caller, until it
// says it has seen enough.
func watch(t *testing.T, url string, enough func(protocol.HostStateFrame) bool) {
	t.Helper()
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url+"/v1/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("the merged stream: %v", err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	name := ""
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("the stream ended before the Hub had said enough: %v", err)
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:") && name == string(protocol.FrameHost):
			var f protocol.HostStateFrame
			if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &f); err != nil {
				t.Fatalf("host frame: %v", err)
			}
			if enough(f) {
				return
			}
		}
	}
}

// quick is a Hub whose curve is short enough to watch.
func quick(hosts []Host, dialer HostDialer) *Hub {
	h := New(hosts, dialer)
	h.keepalive = time.Hour
	h.backoff = 5 * time.Millisecond
	h.steady = 200 * time.Millisecond
	h.stale = 100 * time.Millisecond
	return h
}

// A blink is not a failure. ADR 0004 holds a Host at Connecting until three
// attempts in a row have failed, because a stream that dropped is usually back
// before the third and dimming a Host for a blink costs the user more.
func TestAHostIsConnectingUntilThreeAttemptsHaveFailed(t *testing.T) {
	var dials atomic.Int32
	h := quick([]Host{{ID: "desk"}}, dialFn(func(context.Context, hostset.HostID) (net.Conn, error) {
		dials.Add(1)
		return nil, fmt.Errorf("no route")
	}))
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	var said []protocol.HostState
	watch(t, srv.URL, func(f protocol.HostStateFrame) bool {
		said = append(said, f.State)
		return f.State == protocol.Down
	})

	// Connecting once, and Down only after the third dial. The Hub says nothing in
	// between, because nothing about the Host changed.
	if len(said) != 2 || said[0] != protocol.Connecting || said[1] != protocol.Down {
		t.Fatalf("the Hub said %v", said)
	}
	if n := dials.Load(); n < downAfter {
		t.Errorf("the Hub gave up after %d dials, and a blink is %d", n, downAfter)
	}
}

func TestHTTP200WithoutAHandshakeNeverMakesAHostReady(t *testing.T) {
	var dials atomic.Int32
	h := quick([]Host{{ID: "desk"}}, dialFn(func(context.Context, hostset.HostID) (net.Conn, error) {
		dials.Add(1)
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			bufio.NewReader(server).ReadString('\n')
			fmt.Fprint(server, "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\nnot a Dispatch Handshake")
		}()
		return client, nil
	}))
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	var said []protocol.HostState
	watch(t, srv.URL, func(f protocol.HostStateFrame) bool {
		said = append(said, f.State)
		return f.State == protocol.Ready || f.State == protocol.Down
	})

	if said[len(said)-1] != protocol.Down {
		t.Fatalf("an unrelated HTTP process made the Host %s", said[len(said)-1])
	}
	if n := dials.Load(); n < downAfter {
		t.Errorf("the Hub stopped after %d failed Handshakes", n)
	}
}

// A stream that is open and silent looks exactly like a stream that is working.
// The Daemon beats, so a connection that says nothing for the wait is one the
// Client has to be told about.
func TestALiveConnectionThatGoesQuietEnds(t *testing.T) {
	h := quick([]Host{{ID: "desk"}}, dialFn(func(context.Context, hostset.HostID) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			bufio.NewReader(server).ReadString('\n')
			// A Daemon that completed its Handshake and then stopped beating.
			fmt.Fprint(server, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\nevent: hello\ndata: {\"protocol\":1}\n\n")
			<-time.After(10 * time.Second)
			server.Close()
		}()
		return client, nil
	}))
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	began := time.Now()
	var ready bool
	watch(t, srv.URL, func(f protocol.HostStateFrame) bool {
		ready = ready || f.State == protocol.Ready
		// The stream went quiet, so the Host is Connecting again rather than Ready.
		return ready && f.State == protocol.Connecting
	})
	if took := time.Since(began); took > 5*time.Second {
		t.Errorf("a silent stream took %v to notice", took)
	}
}

// The curve starts over only after a connection that was Ready for the whole
// steady window. A flap that reset it would let a flapping Host redial at full
// speed forever.
func TestAFlappingHostDoesNotResetTheBackoff(t *testing.T) {
	var dials atomic.Int32
	flapping := func(context.Context, hostset.HostID) (net.Conn, error) {
		dials.Add(1)
		client, server := net.Pipe()
		go func() {
			bufio.NewReader(server).ReadString('\n')
			// Ready, and gone again at once. That is a flap.
			fmt.Fprint(server, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\nevent: hello\ndata: {\"protocol\":1}\n\n")
			server.Close()
		}()
		return client, nil
	}

	h := quick([]Host{{ID: "desk"}}, dialFn(flapping))
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	// Watch it flap a few times, then let the curve run.
	rounds := 0
	watch(t, srv.URL, func(f protocol.HostStateFrame) bool {
		if f.State == protocol.Ready {
			rounds++
		}
		return rounds == 4
	})
	after := dials.Load()
	time.Sleep(200 * time.Millisecond)

	// Four flaps have taken the delay to at least 5, 10, 20, 40ms. A curve that had
	// reset would have redialled many more times in the window than one that did
	// not, so the count is what tells them apart.
	if grew := dials.Load() - after; grew > 8 {
		t.Errorf("the Host redialled %d times in 200ms, so the curve reset on a flap", grew)
	}
}

// dialFn is a dialer written inline, for a test that cares about how a dial fails
// rather than about what is behind it.
type dialFn func(context.Context, hostset.HostID) (net.Conn, error)

func (f dialFn) Dial(ctx context.Context, id hostset.HostID) (net.Conn, error) { return f(ctx, id) }
