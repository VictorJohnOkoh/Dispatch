package hub

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type idleDialer struct{}

func (idleDialer) Dial(_ context.Context, _ HostID) (net.Conn, error) {
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		if _, err := http.ReadRequest(bufio.NewReader(server)); err != nil {
			return
		}
		fmt.Fprint(server, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n")
		var b [1]byte
		server.Read(b[:])
	}()
	return client, nil
}

func TestMergedStreamSendsKeepaliveWhileHostsAreIdle(t *testing.T) {
	h := New([]Host{{ID: "desk"}}, idleDialer{})
	h.keepalive = 10 * time.Millisecond
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// The Host State comes first, because a Client that has not been told what a
	// Host is has nothing to draw. What follows it on an idle stream is the beat.
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("the idle stream ended: %v", err)
		}
		if line == ": keepalive\n" {
			return
		}
		if !strings.HasPrefix(line, "event: host") && !strings.HasPrefix(line, "data:") && line != "\n" {
			t.Fatalf("an idle stream said %q", line)
		}
	}
}

// The Client must learn the stream is open before the first frame or keepalive,
// which can be ten seconds away. Go holds the header until something flushes, so
// the handler flushes it itself.
func TestMergedStreamSendsItsHeaderBeforeAnyFrame(t *testing.T) {
	h := New([]Host{{ID: "desk"}}, idleDialer{})
	h.keepalive = time.Hour
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("the header did not arrive on its own: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}
