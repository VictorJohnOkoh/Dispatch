package hub_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub"
	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// Host State is the Hub's view of one Host and the only place that view exists.
// It is derived from the liveness of the Hub's Event stream to that Host: there is
// no health check and no ping endpoint, so these drive the connection itself.

// states reads one merged stream and answers the host frames it carried, in order,
// until it has as many as the caller wants or the deadline passes.
func states(t *testing.T, url string, want int) []protocol.HostStateFrame {
	t.Helper()
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url+"/v1/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("the merged stream: %v", err)
	}
	defer resp.Body.Close()

	var out []protocol.HostStateFrame
	reader := bufio.NewReader(resp.Body)
	name := ""
	for len(out) < want {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("the stream ended after %d host frames: %v", len(out), err)
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:") && name == string(protocol.FrameHost):
			var f protocol.HostStateFrame
			if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &f); err != nil {
				t.Fatalf("host frame: %v", err)
			}
			out = append(out, f)
		}
	}
	return out
}

// A Host the Hub cannot dial at all is Down, and the cause is that nothing
// answered.
func TestAHostThatDoesNotAnswerIsDownUnreachable(t *testing.T) {
	h := hub.New([]hostset.Host{{ID: "desk"}}, dialFunc(func(context.Context, hostset.HostID) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Err: net.UnknownNetworkError("no route")}
	}))
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	got := states(t, srv.URL, 2)
	if got[0].State != protocol.Connecting {
		t.Errorf("the Hub said %q before it had reached anything", got[0].State)
	}
	if got[1].State != protocol.Down || got[1].Cause != protocol.Unreachable {
		t.Errorf("a Host that answered nothing is %q %q", got[1].State, got[1].Cause)
	}
	if got[1].Host != "desk" {
		t.Errorf("the frame is about %q", got[1].Host)
	}
}

// The tunnel opened and nothing was behind it. That is a different problem for the
// user: the machine is there and its Daemon is not.
func TestATunnelWithNoDaemonBehindItIsDownNoDaemon(t *testing.T) {
	h := hub.New([]hostset.Host{{ID: "desk"}}, dialFunc(func(context.Context, hostset.HostID) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			// Something is listening and it is not a Daemon: it takes the request
			// and hangs up.
			bufio.NewReader(server).ReadString('\n')
			server.Close()
		}()
		return client, nil
	}))
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	got := states(t, srv.URL, 2)
	if got[1].State != protocol.Down || got[1].Cause != protocol.NoDaemon {
		t.Errorf("a tunnel with nothing behind it is %q %q", got[1].State, got[1].Cause)
	}
}

// A Host whose Event stream is live is Ready, and nothing else makes one Ready.
func TestAHostWithALiveStreamIsReady(t *testing.T) {
	h := hub.New([]hostset.Host{{ID: "desk"}}, pipeDialer{
		handlers: map[hostset.HostID]http.Handler{"desk": streamHost("1", "PromptSubmitted", &sync.Map{}, "desk")},
	})
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	got := states(t, srv.URL, 2)
	if got[0].State != protocol.Connecting || got[1].State != protocol.Ready {
		t.Errorf("the Hub said %q then %q", got[0].State, got[1].State)
	}
	if got[1].Cause != "" {
		t.Errorf("a Ready Host carries the cause %q", got[1].Cause)
	}
}

// One Host being down does not stop another. The merged stream is the only thing
// the Client has, so a Host that ends it takes every other Host with it.
func TestOneHostBeingDownLeavesTheOthersWorking(t *testing.T) {
	var dials atomic.Int32
	h := hub.New([]hostset.Host{{ID: "desk"}, {ID: "attic"}}, dialFunc(func(ctx context.Context, id hostset.HostID) (net.Conn, error) {
		if id == "attic" {
			dials.Add(1)
			return nil, &net.OpError{Op: "dial", Err: net.UnknownNetworkError("no route")}
		}
		return pipeDialer{handlers: map[hostset.HostID]http.Handler{
			"desk": streamHost("1", "PromptSubmitted", &sync.Map{}, "desk"),
		}}.Dial(ctx, id)
	}))
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	// Four frames: both Hosts say what they are, and the one that is down keeps
	// trying while the one that is up stays Ready.
	byHost := map[string]protocol.HostStateFrame{}
	for _, f := range states(t, srv.URL, 4) {
		if f.State != protocol.Connecting {
			byHost[f.Host] = f
		}
	}
	if byHost["desk"].State != protocol.Ready {
		t.Errorf("the working Host is %q", byHost["desk"].State)
	}
	if byHost["attic"].State != protocol.Down {
		t.Errorf("the Host that is down is %q", byHost["attic"].State)
	}
	if dials.Load() == 0 {
		t.Error("the Host that is down was never dialled")
	}
}

// The Client is told Host State by a frame and never by an Event. Every Event
// carries a Session id, and a Host that is down has no Session to carry one.
func TestHostStateIsAFrameAndNeverAnEvent(t *testing.T) {
	if !protocol.FrameHost.OriginatedByHub() {
		t.Error("the host frame is forwarded, and the Hub is the only thing that knows Host State")
	}
	for _, other := range []protocol.Frame{protocol.FrameEvent, protocol.FrameDelta, protocol.FrameVendors, protocol.FrameResync, protocol.FrameHello} {
		if other.OriginatedByHub() {
			t.Errorf("%s is the Hub's own, and it comes from a Daemon", other)
		}
	}

	// And the Event model has no Host field to carry one in.
	raw, err := json.Marshal(protocol.Event{Seq: 1, Session: "s-1", Kind: "SessionStarted"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"host"`) {
		t.Errorf("an Event carries a Host: %s", raw)
	}
}

// The backoff curve starts over only after a connection that was Ready for a
// whole minute, so a Host that flaps does not get treated as recovered.
func TestTheBackoffResetsOnlyAfterASteadyMinute(t *testing.T) {
	if hub.SteadyFor != time.Minute {
		t.Errorf("the curve resets after %v, and ADR 0004 says a minute", hub.SteadyFor)
	}
	if hub.FirstDelay != time.Second || hub.MaxDelay != time.Minute {
		t.Errorf("the curve is %v to %v", hub.FirstDelay, hub.MaxDelay)
	}
	// A connection is only steady if it was Ready. One that never reached Ready is
	// a Host that is not there, however long the dial took.
	if protocol.StaleAfter <= protocol.KeepaliveInterval {
		t.Errorf("a Host is called Down after %v, and it beats every %v", protocol.StaleAfter, protocol.KeepaliveInterval)
	}
	if protocol.StaleAfter > 30*time.Second {
		t.Errorf("a pulled cable takes %v to reach the user", protocol.StaleAfter)
	}
}
