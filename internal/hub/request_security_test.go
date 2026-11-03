package hub_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub"
	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

func clientRequest(method, target string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, target, body)
	if r.Host == "example.com" {
		r.Host = "127.0.0.1:7700"
	}
	local := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 7700}
	return r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, local))
}

func TestHubRefusesForeignClientRequests(t *testing.T) {
	for _, test := range []struct{ method, path, host, origin string }{
		{"POST", "/v1/hosts/desk/sessions", "127.0.0.1:7700", "http://attacker.invalid"},
		{"POST", "/v1/hosts/desk/sessions/s-1/interrupt", "127.0.0.1:7700", "http://attacker.invalid"},
		{"POST", "/v1/hosts/desk/sessions/s-1/stop", "127.0.0.1:7700", "null"},
		{"POST", "/v1/hosts/desk/retry", "127.0.0.1:7700", "http://attacker.invalid"},
		{"POST", "/start", "127.0.0.1:7700", "http://attacker.invalid"},
		{"POST", "/registration", "127.0.0.1:7700", "http://attacker.invalid"},
		{"GET", "/v1/hosts", "attacker.invalid:7700", ""},
		{"GET", "/v1/hosts/desk/sessions", "attacker.invalid:7700", ""},
		{"GET", "/v1/events", "attacker.invalid:7700", ""},
		{"GET", "/", "localhost:7701", ""},
		{"HEAD", "/hosts", "localhost:7701", ""},
		{"POST", "/v1/hosts/desk/sessions", "localhost:7700", "https://localhost:7700"},
		{"POST", "/v1/hosts/desk/sessions", "localhost:7700", "http://localhost:7700/path"},
		{"DELETE", "/v1/hosts/desk/sessions/s-1", "localhost:7700", "http://attacker.invalid"},
	} {
		t.Run(test.method+test.path+test.host+test.origin, func(t *testing.T) {
			r := clientRequest(test.method, test.path, strings.NewReader(`{"harness":"passthrough","model":"test"}`))
			r.Host = test.host
			r.Header.Set("Origin", test.origin)
			r.Header.Set("Content-Type", "text/plain")
			ctx, cancel := context.WithCancel(r.Context())
			cancel() // A missing stream check must fail without leaving the test waiting.
			w := httptest.NewRecorder()
			calls := 0
			dialer := dialFunc(func(context.Context, hostset.HostID) (net.Conn, error) {
				calls++
				return nil, errors.New("unexpected Daemon connection")
			})
			hub.New([]hub.Host{{ID: "desk"}}, dialer).Handler().ServeHTTP(w, r.WithContext(ctx))
			var refusal protocol.Refusal
			if w.Code != http.StatusForbidden || json.Unmarshal(w.Body.Bytes(), &refusal) != nil || refusal.Reason != protocol.ReasonForbidden || refusal.Detail == "" {
				t.Fatalf("got %d %s, want a JSON refusal with status 403", w.Code, w.Body.String())
			}
			if calls != 0 {
				t.Fatalf("refused request made %d Daemon connections", calls)
			}
		})
	}
}

func TestHubAllowsClientReadsAndStream(t *testing.T) {
	for _, host := range []string{"127.0.0.1:7700", "localhost:7700", "[::1]:7700"} {
		for _, path := range []string{"/", "/hosts", "/new", "/v1/hosts", "/v1/events"} {
			r := clientRequest("GET", path, nil)
			r.Host = host
			ctx, cancel := context.WithCancel(r.Context())
			cancel()
			w := httptest.NewRecorder()
			hub.New(nil, nil).Handler().ServeHTTP(w, r.WithContext(ctx))
			if w.Code != http.StatusOK {
				t.Errorf("%s %s: got %d", host, path, w.Code)
			}
		}
	}
}

func TestHubAllowsClientCommands(t *testing.T) {
	for _, origin := range []string{"", "http://127.0.0.1:7700"} {
		for _, command := range []string{"", "/s-1/prompts", "/s-1/approvals", "/s-1/policy", "/s-1/interrupt", "/s-1/stop"} {
			calls := 0
			h := hub.New([]hub.Host{{ID: "desk"}}, pipeDialer{handlers: map[hostset.HostID]http.Handler{"desk": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(protocol.StatusAccepted)
			})}}).Handler()
			r := clientRequest("POST", "/v1/hosts/desk/sessions"+command, strings.NewReader(`{}`))
			if origin != "" {
				r.Header.Set("Origin", origin)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != protocol.StatusAccepted || calls != 1 {
				t.Errorf("%s %s: got %d, calls %d", origin, command, w.Code, calls)
			}
		}
	}
}

func TestHubAllowsSameOriginRetry(t *testing.T) {
	r := clientRequest("POST", "/v1/hosts/desk/retry", nil)
	r.Host = "localhost:7700"
	r.Header.Set("Origin", "http://localhost:7700")
	w := httptest.NewRecorder()
	hub.New([]hub.Host{{ID: "desk"}}, nil).Handler().ServeHTTP(w, r)
	if w.Code != protocol.StatusAccepted {
		t.Fatalf("retry returned %d: %s", w.Code, w.Body.String())
	}
}

func TestHubRequiresListeningAddress(t *testing.T) {
	r := httptest.NewRequest("GET", "http://127.0.0.1:7700/v1/hosts", nil)
	w := httptest.NewRecorder()
	hub.New(nil, nil).Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("request without listening address returned %d", w.Code)
	}
}
