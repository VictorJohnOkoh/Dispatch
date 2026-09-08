package hub

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmptyHubServesRegistrationAndRejectsOtherOrigins(t *testing.T) {
	h := New(nil, nil)
	calls := 0
	h.WithRegistration(func(context.Context, RegistrationInput) error { calls++; return nil })
	page := httptest.NewRecorder()
	h.Handler().ServeHTTP(page, httptest.NewRequest("GET", "http://127.0.0.1:7700/hosts", nil))
	if page.Code != 200 || !strings.Contains(page.Body.String(), "host-registration") {
		t.Fatal("empty Hub did not serve registration", page.Code)
	}
	for _, test := range []struct {
		origin, host, media, body string
		want                      int
	}{
		{"http://127.0.0.1:7700", "127.0.0.1:7700", "application/json", `{"id":"desk","code":"test"}`, 204},
		{"https://evil.example", "127.0.0.1:7700", "application/json", `{"id":"desk"}`, 403},
		{"null", "127.0.0.1:7700", "application/json", `{"id":"desk"}`, 403},
		{"http://evil.example:7700", "evil.example:7700", "application/json", `{"id":"desk"}`, 403},
		{"http://127.0.0.1:7700", "127.0.0.1:7700", "text/plain", `{"id":"desk"}`, 403},
		{"http://127.0.0.1:7700", "127.0.0.1:7700", "application/json", `{"id":"desk"} {}`, 400},
		{"http://127.0.0.1:7700", "127.0.0.1:7700", "application/json", `{"id":"desk","code":"` + strings.Repeat("x", 6000) + `"}`, 400},
	} {
		r := httptest.NewRequest("POST", "http://"+test.host+"/registration", strings.NewReader(test.body))
		r.RemoteAddr = "127.0.0.1:32123"
		r.Header.Set("Origin", test.origin)
		r.Header.Set("Content-Type", test.media)
		w := httptest.NewRecorder()
		h.Handler().ServeHTTP(w, r)
		if w.Code != test.want {
			t.Errorf("%s %s: got %d want %d", test.origin, test.media, w.Code, test.want)
		}
	}
	if calls != 1 {
		t.Fatalf("%d registration callbacks, want 1", calls)
	}
}

func TestAddingAHostEndsTheOldMergedStreamForReconnect(t *testing.T) {
	h := New(nil, nil)
	server := httptest.NewServer(h.Handler())
	defer server.Close()
	r, err := http.NewRequestWithContext(t.Context(), "GET", server.URL+"/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	h.hosts.Add(Host{ID: "desk"})
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatal(err)
	}
	if len(h.All()) != 1 || h.All()[0] != "desk" {
		t.Fatal("Host was not attached")
	}
}
