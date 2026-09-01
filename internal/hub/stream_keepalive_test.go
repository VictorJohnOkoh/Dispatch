package hub

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
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
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != ": keepalive\n" {
		t.Fatalf("first idle-stream line = %q, %v", line, err)
	}
}
