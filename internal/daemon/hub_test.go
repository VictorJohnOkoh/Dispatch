package daemon

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

type hubPipes map[hub.HostID]http.Handler

func (p hubPipes) Dial(_ context.Context, id hub.HostID) (net.Conn, error) {
	client, server := net.Pipe()
	go http.Serve(&oneConnection{conn: server}, p[id])
	return client, nil
}

type oneConnection struct {
	conn net.Conn
	used bool
}

func (l *oneConnection) Accept() (net.Conn, error) {
	if l.used {
		return nil, errors.New("closed")
	}
	l.used = true
	return l.conn, nil
}
func (l *oneConnection) Close() error   { return nil }
func (l *oneConnection) Addr() net.Addr { return l.conn.LocalAddr() }

func TestClientCommandsReachTwoDaemonsAndBothReturnFrames(t *testing.T) {
	desk, pi := newHost(t), newHost(t)
	handler := hub.New([]hub.Host{{ID: "desk"}, {ID: "pi"}}, hubPipes{
		"desk": desk.Handler(), "pi": pi.Handler(),
	}).Handler()

	for _, item := range []struct {
		id   string
		host *host
	}{{"desk", desk}, {"pi", pi}} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/hosts/"+item.id+"/sessions", strings.NewReader(startBody)))
		answer := item.host.started(t, w)
		item.host.waitState(t, answer.Session, "Idle")
	}

	srv := httptest.NewServer(handler)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/events", nil)
	req.Header.Set(protocol.CursorHeader, "desk=0,pi=0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	seen := map[string]bool{}
	scan := bufio.NewScanner(resp.Body)
	for scan.Scan() && len(seen) < 2 {
		line := scan.Text()
		if strings.Contains(line, `"host":"desk"`) {
			seen["desk"] = true
		}
		if strings.Contains(line, `"host":"pi"`) {
			seen["pi"] = true
		}
	}
	if !seen["desk"] || !seen["pi"] {
		t.Fatalf("merged stream reached Hosts %v, want both", seen)
	}
}
