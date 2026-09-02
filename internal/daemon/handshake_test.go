package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// The Daemon's half of the Handshake. It serves the version it can, or it names
// the set it does serve and refuses, and it says so in the operational log so a
// human can see that the Hub asked once and stopped.

func (h *host) handshake(t *testing.T, asked string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	if asked != "" {
		r.Header.Set(protocol.VersionHeader, asked)
	}
	w := httptest.NewRecorder()
	h.handler().ServeHTTP(w, r)
	return w
}

// A Hub that names a version this Daemon cannot serve is refused with the set it
// does serve, which is {1} today.
func TestAVersionThisDaemonCannotServeIsRefusedWithTheSetItServes(t *testing.T) {
	h := newHost(t)

	w := h.handshake(t, "2")
	if w.Code != protocol.StatusUpgradeRequired {
		t.Fatalf("status = %d, want %d", w.Code, protocol.StatusUpgradeRequired)
	}
	var refusal protocol.Refusal
	if err := json.Unmarshal(w.Body.Bytes(), &refusal); err != nil {
		t.Fatalf("the refusal body: %v", err)
	}
	if refusal.Reason != protocol.ReasonProtocol {
		t.Errorf("reason = %q", refusal.Reason)
	}
	if len(refusal.Speaks) != 1 || refusal.Speaks[0] != protocol.Version {
		t.Errorf("this Daemon says it speaks %v", refusal.Speaks)
	}
	if refusal.Detail == "" {
		t.Error("the refusal carries no sentence to show")
	}

	// The Hub marks that Host Incompatible and never dials it again, so this line
	// is the only evidence the check ran. A human reading the Daemon's log sees one
	// of them and no more.
	if !strings.Contains(h.lines.String(), "the Handshake was refused") {
		t.Errorf("the operational log = %q", h.lines.String())
	}
}

// The version this build speaks is served, and a caller that names none is served
// too, because curl names none. The Handshake runs on the stream, so passing it is
// the hello frame arriving.
func TestTheVersionThisBuildSpeaksIsServed(t *testing.T) {
	for _, asked := range []string{"1", ""} {
		h := newHost(t)
		srv := httptest.NewServer(h.handler())
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/events", nil)
		if asked != "" {
			req.Header.Set(protocol.VersionHeader, asked)
		}
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			t.Fatalf("GET /v1/events: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("a Hub asking for %q got %d", asked, resp.StatusCode)
		}
		hello := newReader(t, resp).next(t)
		if hello.name != string(protocol.FrameHello) {
			t.Errorf("the first Frame is %q, and the Handshake is the hello", hello.name)
		}
		if !strings.Contains(hello.data, `"protocol":1`) {
			t.Errorf("the hello says %q", hello.data)
		}
		if strings.Contains(h.lines.String(), "the Handshake was refused") {
			t.Errorf("a Handshake that passed was logged as refused: %q", h.lines.String())
		}
		resp.Body.Close()
		srv.Close()
	}
}
