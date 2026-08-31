package daemon

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"sync"
	"testing"
	"time"
)

// lines is a log a test can read while the Daemon is still writing to it.
type lines struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lines) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

var addrLine = regexp.MustCompile(`addr=(\S+)`)

// boundAddr waits for the bind address slog writes at start.
func boundAddr(t *testing.T, l *lines) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if m := addrLine.FindStringSubmatch(l.String()); m != nil {
			return m[1]
		}
		if time.Now().After(deadline) {
			t.Fatalf("no bind address in the log: %s", l.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Serve binds, answers, and stays up until its context is done. Port 0 is asked
// for and the bound address is read back out of the log, which is the same line
// an operator reads.
func TestServeBindsLoopbackAndStaysUp(t *testing.T) {
	log := &lines{}
	d := New(nil, slog.New(slog.NewTextHandler(log, nil)))

	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- d.Serve(ctx, "127.0.0.1:0") }()

	resp, err := http.Get("http://" + boundAddr(t, log) + "/v1/models")
	if err != nil {
		t.Fatalf("the Daemon did not answer: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status %d", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return")
	}
}

// The Hub reaches the Daemon through an SSH tunnel, so an address off the loopback
// interface is refused at start rather than bound.
func TestServeRefusesAnAddressThatIsNotLoopback(t *testing.T) {
	for _, listen := range []string{"0.0.0.0:7717", "192.168.1.4:7717", "localhost:7717", "7717"} {
		if err := New(nil, quiet()).Serve(t.Context(), listen); err == nil {
			t.Errorf("Serve(%q) = nil, want an error", listen)
		}
	}
}
