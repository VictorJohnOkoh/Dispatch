package hub_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
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

func (d pipeDialer) Dial(_ context.Context, id hostset.HostID) (net.Conn, error) {
	client, server := net.Pipe()
	go serveOne(server, d.handlers[id])
	return client, nil
}

func TestTheMergedStreamCarriesEveryHostAndSplitsAReconnectCursor(t *testing.T) {
	seen := &sync.Map{}
	d := pipeDialer{seen: seen, handlers: map[hostset.HostID]http.Handler{
		"desk": streamHost("1", "FutureKind", seen, "desk"),
		"pi":   streamHost("7", "PromptSubmitted", seen, "pi"),
	}}
	h := hub.New([]hostset.Host{{ID: "desk"}, {ID: "pi"}}, d).Handler()
	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	req.Header.Set(protocol.CursorHeader, "desk=0,pi=6")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	body := w.Body.String()
	for _, want := range []string{`"host":"desk"`, `"host":"pi"`, `"kind":"FutureKind"`, "id: desk=1,pi=7"} {
		if !strings.Contains(body, want) {
			t.Errorf("stream does not contain %q:\n%s", want, body)
		}
	}
	desk, _ := seen.Load(hostset.HostID("desk"))
	pi, _ := seen.Load(hostset.HostID("pi"))
	if desk != "0|" || pi != "6|" {
		t.Errorf("Daemon cursors = %v and %v, want desk 0 and pi 6", desk, pi)
	}

	reconnect := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	reconnect.Header.Set(protocol.CursorHeader, "desk=1,pi=7")
	h.ServeHTTP(httptest.NewRecorder(), reconnect)
	desk, _ = seen.Load(hostset.HostID("desk"))
	pi, _ = seen.Load(hostset.HostID("pi"))
	if desk != "1|desk-log" || pi != "7|pi-log" {
		t.Errorf("reconnect = %v and %v", desk, pi)
	}
}

func streamHost(id, kind string, seen *sync.Map, host hostset.HostID) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store(host, r.Header.Get(protocol.CursorHeader)+"|"+r.Header.Get(protocol.LogHeader))
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: hello\ndata: {\"protocol\":1,\"logId\":%q,\"latest\":%s}\n\n", host+"-log", id)
		fmt.Fprintf(w, "id: %s\nevent: event\ndata: {\"seq\":%s,\"session\":\"s\",\"at\":1,\"kind\":\"%s\",\"payload\":{}}\n\n", id, id, kind)
	})
}

func serveOne(conn net.Conn, handler http.Handler) {
	defer conn.Close()
	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return
	}
	w := &pipeResponse{header: make(http.Header), conn: conn}
	handler.ServeHTTP(w, req)
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
