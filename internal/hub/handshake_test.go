package hub

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// The Handshake is the protocol version check the Hub and a Daemon run when the
// connection opens. It runs on the Event stream, so these drive the stream: a
// Daemon that refuses the version, and what the Hub does with the Host afterwards.

// refusals is what an old Daemon did: how many times it was dialled, and the
// request line and version header of the last dial. One reader and one writer
// under one mutex, because a test asserts on both after the stream has said its
// piece.
type refusals struct {
	mu    sync.Mutex
	dials int
	asked []string
}

func (r *refusals) saw(request string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dials++
	r.asked = append(r.asked, request)
}

func (r *refusals) seen() (int, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dials, append([]string(nil), r.asked...)
}

// oldDaemon answers every stream with the refusal a Daemon that cannot serve the
// Hub's version sends, and records what it was asked.
func oldDaemon(seen *refusals) dialFn {
	return func(context.Context, hostset.HostID) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			reader := bufio.NewReader(server)
			line, _ := reader.ReadString('\n')
			request := strings.TrimSpace(line)
			for {
				header, err := reader.ReadString('\n')
				if err != nil || strings.TrimSpace(header) == "" {
					break
				}
				name, value, _ := strings.Cut(header, ":")
				if strings.EqualFold(name, protocol.VersionHeader) {
					request += " " + protocol.VersionHeader + ": " + strings.TrimSpace(value)
				}
			}
			seen.saw(request)
			body := `{"reason":"protocol","detail":"this Daemon does not speak protocol 1","speaks":[2]}`
			fmt.Fprintf(server, "HTTP/1.1 426 Upgrade Required\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
		}()
		return client, nil
	}
}

// A Daemon that refuses the version makes its Host Incompatible, and the Hub never
// retries one: a version mismatch cannot fix itself, so retrying would hammer a
// Host that can never come Ready.
func TestARefusedHandshakeMakesTheHostIncompatibleAndStopsTheRetries(t *testing.T) {
	var seen refusals
	h := quick([]Host{{ID: "desk"}}, oldDaemon(&seen))
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	var said []protocol.HostStateFrame
	watch(t, srv.URL, func(f protocol.HostStateFrame) bool {
		said = append(said, f)
		return f.State == protocol.Incompatible
	})

	// Connecting once and Incompatible once. It is never Down: the machine answered
	// and what it said was that it speaks another version.
	if len(said) != 2 || said[0].State != protocol.Connecting {
		t.Fatalf("the Hub said %v", said)
	}
	last := said[len(said)-1]
	if last.Host != "desk" {
		t.Errorf("the frame is about %q", last.Host)
	}
	if len(last.Speaks) != 1 || last.Speaks[0] != 2 {
		t.Errorf("the Client is told the Host speaks %v, and the Daemon said [2]", last.Speaks)
	}

	// The backoff curve on this Hub is 5ms, so a Host still being retried would
	// have dialled many times over.
	after, _ := seen.seen()
	time.Sleep(200 * time.Millisecond)
	if now, _ := seen.seen(); now != after {
		t.Errorf("an Incompatible Host was dialled %d more times, and the Hub never retries one", now-after)
	}
}

// The Handshake runs on the Event stream and there is no endpoint of its own for
// it. The stream is the connection presence is measured on, so a second thing to
// open would be a second thing to keep alive and get wrong.
func TestTheHandshakeRunsOnTheEventStream(t *testing.T) {
	var seen refusals
	h := quick([]Host{{ID: "desk"}}, oldDaemon(&seen))
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	watch(t, srv.URL, func(f protocol.HostStateFrame) bool { return f.State == protocol.Incompatible })

	_, requests := seen.seen()
	if len(requests) != 1 {
		t.Fatalf("the Hub asked this Host %v", requests)
	}
	want := fmt.Sprintf("GET /v1/events HTTP/1.1 %s: %d", protocol.VersionHeader, protocol.Version)
	if requests[0] != want {
		t.Errorf("the Handshake ran on %q, want %q", requests[0], want)
	}
	for _, route := range protocol.Routes {
		if strings.Contains(route, "handshake") {
			t.Errorf("%q is an endpoint of the Handshake's own", route)
		}
	}
}

// An Incompatible Host stays listed, and the Client's stream stays open. A merged
// stream that ended would read to the Client as one that finished, and the browser
// would reconnect at once and keep doing it.
func TestAnIncompatibleHostDoesNotEndTheMergedStream(t *testing.T) {
	h := quick([]Host{{ID: "desk"}}, oldDaemon(&refusals{}))
	h.keepalive = 20 * time.Millisecond
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("the merged stream: %v", err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	incompatible := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("the stream ended on a Host the Hub had stopped working on: %v", err)
		}
		if strings.Contains(line, string(protocol.Incompatible)) {
			incompatible = true
		}
		// A keepalive after the last Host has stopped is the stream still being
		// there, which is the whole of what this asserts.
		if incompatible && strings.HasPrefix(line, ":") {
			return
		}
	}
}

// One Host being Incompatible does not stop another. The merged stream is the only
// thing the Client has.
func TestAnIncompatibleHostLeavesTheOthersWorking(t *testing.T) {
	old := oldDaemon(&refusals{})
	h := quick([]Host{{ID: "desk"}, {ID: "attic"}}, dialFn(func(ctx context.Context, id hostset.HostID) (net.Conn, error) {
		if id == "desk" {
			return old(ctx, id)
		}
		return liveDaemon(ctx, id)
	}))
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	byHost := map[string]protocol.HostState{}
	watch(t, srv.URL, func(f protocol.HostStateFrame) bool {
		if f.State != protocol.Connecting {
			byHost[f.Host] = f.State
		}
		return byHost["desk"] == protocol.Incompatible && byHost["attic"] == protocol.Ready
	})
}

// liveDaemon completes the Handshake and then beats, which is a Host that is
// Ready and stays that way.
func liveDaemon(context.Context, hostset.HostID) (net.Conn, error) {
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		bufio.NewReader(server).ReadString('\n')
		fmt.Fprint(server, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\nevent: hello\ndata: {\"protocol\":1}\n\n")
		beat := time.NewTicker(10 * time.Millisecond)
		defer beat.Stop()
		for range beat.C {
			if _, err := fmt.Fprint(server, protocol.Keepalive); err != nil {
				return
			}
		}
	}()
	return client, nil
}
