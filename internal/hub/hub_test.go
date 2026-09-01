package hub_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/daemon"
	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/eventlog"
	"github.com/VictorJohnOkoh/Dispatch/internal/hub"
	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"github.com/VictorJohnOkoh/Dispatch/internal/workspace"
)

type pipeDialer struct {
	handlers map[hostset.HostID]http.Handler
	seen     *sync.Map
}

type dialFunc func(context.Context, hostset.HostID) (net.Conn, error)

func (f dialFunc) Dial(ctx context.Context, id hostset.HostID) (net.Conn, error) {
	return f(ctx, id)
}

func (d pipeDialer) Dial(_ context.Context, id hostset.HostID) (net.Conn, error) {
	client, server := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	go serveOne(ctx, server, d.handlers[id])
	return pipeConn{Conn: client, cancel: cancel}, nil
}

// pipeConn ends the stub Daemon's request when the Hub hangs up, the way a real
// server ends a handler when its connection closes.
type pipeConn struct {
	net.Conn
	cancel context.CancelFunc
}

func (c pipeConn) Close() error {
	c.cancel()
	return c.Conn.Close()
}

func TestTheMergedStreamCarriesEveryHostAndSplitsAReconnectCursor(t *testing.T) {
	seen := &sync.Map{}
	d := pipeDialer{seen: seen, handlers: map[hostset.HostID]http.Handler{
		"desk": streamHost("1", "FutureKind", seen, "desk"),
		"pi":   streamHost("7", "PromptSubmitted", seen, "pi"),
	}}
	srv := httptest.NewServer(hub.New([]hostset.Host{{ID: "desk"}, {ID: "pi"}}, d).Handler())
	defer srv.Close()

	// This Hub has heard no Hello, so it can name neither log. It sends neither
	// Cursor and answers each Host with a resync, because a Cursor it cannot check
	// against a log would resume a replaced one at a number that means something
	// else there.
	merged(t, srv.URL+"/v1/events", "desk=0,pi=6", `"host":"desk"`, `"host":"pi"`, `"kind":"FutureKind"`, "event: resync", `"logId":"desk-log"`, "id: desk=1,pi=7")
	desk, _ := seen.Load(hostset.HostID("desk"))
	pi, _ := seen.Load(hostset.HostID("pi"))
	if desk != "|" || pi != "|" {
		t.Errorf("Daemon cursors = %v and %v, want neither sent", desk, pi)
	}

	// The Hello named both logs, so this one resumes and each Daemon reads its own
	// Cursor and its own log identity.
	merged(t, srv.URL+"/v1/events", "desk=1,pi=7", "id: desk=1,pi=7", `"host":"pi"`)
	desk, _ = seen.Load(hostset.HostID("desk"))
	pi, _ = seen.Load(hostset.HostID("pi"))
	if desk != "1|desk-log" || pi != "7|pi-log" {
		t.Errorf("reconnect = %v and %v", desk, pi)
	}
}

func TestAHostThatIsDownDoesNotEndTheMergedStream(t *testing.T) {
	var dials atomic.Int32
	pipes := pipeDialer{seen: &sync.Map{}, handlers: map[hostset.HostID]http.Handler{
		"desk": streamHost("3", "PromptSubmitted", &sync.Map{}, "desk"),
	}}
	d := dialFunc(func(ctx context.Context, id hostset.HostID) (net.Conn, error) {
		if dials.Add(1) == 1 {
			return nil, errors.New("the Host is down")
		}
		return pipes.Dial(ctx, id)
	})
	srv := httptest.NewServer(hub.New([]hostset.Host{{ID: "desk"}}, d).Handler())
	defer srv.Close()

	merged(t, srv.URL+"/v1/events", "", `"kind":"PromptSubmitted"`)
	if dials.Load() < 2 {
		t.Errorf("the Hub dialled %d times, want a retry", dials.Load())
	}
}

// merged opens the Client's merged stream at url and reads it until every want has
// appeared. A stream that ends first is the failure: a Host that is down must not
// end it.
func merged(t *testing.T, url, cursor string, wants ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if cursor != "" {
		req.Header.Set(protocol.CursorHeader, cursor)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body strings.Builder
	scan := bufio.NewScanner(resp.Body)
	for scan.Scan() {
		body.WriteString(scan.Text())
		body.WriteByte('\n')
		if has(body.String(), wants) {
			return body.String()
		}
	}
	t.Fatalf("the merged stream ended before %q:\n%s", wants, body.String())
	return ""
}

func streamHost(id, kind string, seen *sync.Map, host hostset.HostID) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store(host, r.Header.Get(protocol.CursorHeader)+"|"+r.Header.Get(protocol.LogHeader))
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: hello\ndata: {\"protocol\":1,\"logId\":%q,\"latest\":%s}\n\n", host+"-log", id)
		fmt.Fprintf(w, "id: %s\nevent: event\ndata: {\"seq\":%s,\"session\":\"s\",\"at\":1,\"kind\":\"%s\",\"payload\":{}}\n\n", id, id, kind)
		// A real Daemon holds the stream open, and the Hub redials one that does not.
		<-r.Context().Done()
	})
}

func serveOne(ctx context.Context, conn net.Conn, handler http.Handler) {
	defer conn.Close()
	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return
	}
	w := &pipeResponse{header: make(http.Header), conn: conn}
	handler.ServeHTTP(w, req.WithContext(ctx))
}

type pipeResponse struct {
	header http.Header
	conn   net.Conn
	wrote  bool
}

func (w *pipeResponse) Header() http.Header { return w.header }
func (w *pipeResponse) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.wrote = true
	fmt.Fprintf(w.conn, "HTTP/1.1 %d %s\r\n", status, http.StatusText(status))
	w.header.Write(w.conn)
	io.WriteString(w.conn, "Transfer-Encoding: chunked\r\n\r\n")
}
func (w *pipeResponse) Write(p []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if len(p) == 0 {
		return 0, nil
	}
	if _, err := fmt.Fprintf(w.conn, "%x\r\n", len(p)); err != nil {
		return 0, err
	}
	if _, err := w.conn.Write(p); err != nil {
		return 0, err
	}
	_, err := io.WriteString(w.conn, "\r\n")
	return len(p), err
}
func (w *pipeResponse) Flush() {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
}

func TestTheHubListsHostsAndForwardsACommand(t *testing.T) {
	seen := ""
	d := pipeDialer{handlers: map[hostset.HostID]http.Handler{
		"desk": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = r.Method + " " + r.URL.Path
			w.WriteHeader(http.StatusAccepted)
		}),
	}}
	h := hub.New([]hostset.Host{{ID: "desk"}}, d).Handler()

	list := httptest.NewRecorder()
	h.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/v1/hosts", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"id":"desk"`) {
		t.Fatalf("GET hosts: %d %s", list.Code, list.Body.String())
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/hosts/desk/sessions", strings.NewReader(`{}`)))
	if w.Code != http.StatusAccepted || seen != "POST /v1/sessions" {
		t.Fatalf("forwarded %q and answered %d", seen, w.Code)
	}
}

func TestForwardedEventStreamFlushesEachFrame(t *testing.T) {
	release := make(chan struct{})
	wrote := make(chan struct{})
	d := pipeDialer{handlers: map[hostset.HostID]http.Handler{
		"desk": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: event\ndata: {}\n\n")
			w.(http.Flusher).Flush()
			close(wrote)
			<-release
		}),
	}}
	srv := httptest.NewServer(hub.New([]hostset.Host{{ID: "desk"}}, d).Handler())
	defer srv.Close()
	defer close(release)

	type result struct {
		resp *http.Response
		err  error
	}
	resultCh := make(chan result, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/hosts/desk/events", nil)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		resultCh <- result{resp: resp, err: err}
	}()

	<-wrote
	select {
	case got := <-resultCh:
		if got.err != nil {
			t.Fatal(got.err)
		}
		defer got.resp.Body.Close()
		line, err := bufio.NewReader(got.resp.Body).ReadString('\n')
		if err != nil || line != "event: event\n" {
			t.Fatalf("first streamed line = %q, %v", line, err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("the forwarded Frame was not flushed")
	}
}

func TestLeavingForwardedEventStreamClosesHostConnection(t *testing.T) {
	connected := make(chan net.Conn, 1)
	disconnected := make(chan struct{})
	d := dialFunc(func(_ context.Context, _ hostset.HostID) (net.Conn, error) {
		client, server := net.Pipe()
		connected <- server
		go func() {
			defer server.Close()
			if _, err := http.ReadRequest(bufio.NewReader(server)); err != nil {
				return
			}
			frame := []byte("event: event\ndata: {}\n\n")
			fmt.Fprint(server, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n")
			fmt.Fprintf(server, "%x\r\n", len(frame))
			server.Write(frame)
			io.WriteString(server, "\r\n")
			var b [1]byte
			server.Read(b[:])
			close(disconnected)
		}()
		return client, nil
	})
	srv := httptest.NewServer(hub.New([]hostset.Host{{ID: "desk"}}, d).Handler())
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/hosts/desk/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	upstream := <-connected
	defer upstream.Close()
	cancel()
	resp.Body.Close()

	select {
	case <-disconnected:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("the Host connection stayed open after the Client left")
	}
}

func TestAClientCommandAndFrameCrossTheHubAndDaemon(t *testing.T) {
	dir := t.TempDir()
	events, err := eventlog.Open(filepath.Join(dir, "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { events.Close() })
	if _, err := events.Append(event.Event{
		Session: "s-1", At: time.UnixMicro(1).UTC(), Kind: event.KindPromptSubmitted,
		Payload: &event.PromptSubmitted{Text: "hello"},
	}); err != nil {
		t.Fatal(err)
	}
	root, err := workspace.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	d := daemon.New(slog.New(slog.NewTextHandler(io.Discard, nil)), events, root, nil, nil)
	h := hub.New([]hostset.Host{{ID: "desk"}}, pipeDialer{
		handlers: map[hostset.HostID]http.Handler{"desk": d.Handler()},
	}).Handler()

	page := httptest.NewRecorder()
	h.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/v1/hosts/desk/sessions/s-1/events", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `"text":"hello"`) {
		t.Fatalf("Client command: %d %s", page.Code, page.Body.String())
	}

	srv := httptest.NewServer(h)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/events", nil)
	req.Header.Set(protocol.CursorHeader, "desk=0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var frame strings.Builder
	scan := bufio.NewScanner(resp.Body)
	for scan.Scan() {
		frame.WriteString(scan.Text())
		if strings.Contains(frame.String(), `"text":"hello"`) {
			break
		}
	}
	if !strings.Contains(frame.String(), `"host":"desk"`) {
		t.Fatalf("Frame did not return through the Hub: %s", frame.String())
	}
}

func TestAllTenDaemonEndpointsUseTheOneHostHandler(t *testing.T) {
	cases := []struct {
		method, client, daemon string
	}{
		{"GET", "/v1/hosts/desk/events", "/v1/events"},
		{"GET", "/v1/hosts/desk/sessions", "/v1/sessions"},
		{"GET", "/v1/hosts/desk/models", "/v1/models"},
		{"GET", "/v1/hosts/desk/sessions/s-1/events", "/v1/sessions/s-1/events"},
		{"POST", "/v1/hosts/desk/sessions", "/v1/sessions"},
		{"POST", "/v1/hosts/desk/sessions/s-1/prompts", "/v1/sessions/s-1/prompts"},
		{"POST", "/v1/hosts/desk/sessions/s-1/approvals", "/v1/sessions/s-1/approvals"},
		{"POST", "/v1/hosts/desk/sessions/s-1/policy", "/v1/sessions/s-1/policy"},
		{"POST", "/v1/hosts/desk/sessions/s-1/interrupt", "/v1/sessions/s-1/interrupt"},
		{"POST", "/v1/hosts/desk/sessions/s-1/stop", "/v1/sessions/s-1/stop"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.daemon, func(t *testing.T) {
			seen := ""
			d := pipeDialer{handlers: map[hostset.HostID]http.Handler{
				"desk": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					seen = r.Method + " " + r.URL.Path
					w.WriteHeader(http.StatusAccepted)
				}),
			}}
			h := hub.New([]hostset.Host{{ID: "desk"}}, d).Handler()
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(c.method, c.client, nil))
			if want := c.method + " " + c.daemon; seen != want {
				t.Errorf("forwarded %q, want %q", seen, want)
			}
		})
	}
}

func has(body string, wants []string) bool {
	for _, want := range wants {
		if !strings.Contains(body, want) {
			return false
		}
	}
	return true
}

// A Host the Hub cannot reach answers 503 with the dialer's own words in it, so
// a Host that is off and a Daemon that is not running do not read the same.
func TestAHostThatCannotBeReachedNamesWhy(t *testing.T) {
	dialer := dialFunc(func(context.Context, hostset.HostID) (net.Conn, error) {
		return nil, hostset.ErrNoDaemon
	})
	srv := httptest.NewServer(hub.New([]hostset.Host{{ID: "desk"}}, dialer).Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/hosts/desk/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	if !strings.Contains(string(body), hostset.ErrNoDaemon.Error()) {
		t.Errorf("body = %q, want the dialer's error in it", body)
	}
}
