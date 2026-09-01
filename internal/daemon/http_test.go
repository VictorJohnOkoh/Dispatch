package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"github.com/VictorJohnOkoh/Dispatch/internal/vendors"
)

// decodeGet reads one GET answer into a body a test names for itself, which is
// how a test reads one field of an answer without repeating the whole shape.
func decodeGet(t *testing.T, d *Daemon, path string, into any) {
	t.Helper()
	w := get(t, d, path)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d: %s", path, w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), into); err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
}

func get(t *testing.T, d *Daemon, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	d.handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestListModelsAnswersFromTheCache(t *testing.T) {
	f := ollamaFake()
	d := plain(t, []vendors.Adapter{f}, quiet())
	d.vendors.pollAll(t.Context())
	before := f.calls

	w := get(t, d, "/v1/models")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if f.calls != before {
		t.Errorf("the request called the Vendor %d more times", f.calls-before)
	}

	var body struct{ Vendors []CatalogueView }
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Vendors) != 1 || len(body.Vendors[0].Models) != 1 {
		t.Fatalf("body = %+v", body)
	}
	if got := body.Vendors[0].Models[0].ID; got != "qwen3:8b" {
		t.Errorf("id = %q", got)
	}
}

// A Daemon whose Vendor has never answered still has a line for it, with the stamp
// at zero, so the Client draws an empty Vendor row rather than nothing.
func TestListModelsAnswersBeforeTheFirstBeat(t *testing.T) {
	d := plain(t, []vendors.Adapter{ollamaFake()}, quiet())

	w := get(t, d, "/v1/models")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if got := w.Body.String(); !strings.Contains(got, `"at":0`) {
		t.Errorf("body = %s", got)
	}
}

func TestPprofAnswersOnTheDaemonsListener(t *testing.T) {
	d := plain(t, nil, quiet())
	for _, path := range []string{"/debug/pprof/", "/debug/pprof/goroutine?debug=1", "/debug/pprof/cmdline"} {
		if w := get(t, d, path); w.Code != http.StatusOK {
			t.Errorf("%s: status %d", path, w.Code)
		}
	}
}

// The mux registers the route as protocol spells it, so the two cannot drift.
func TestModelsIsTheRouteProtocolNames(t *testing.T) {
	if protocol.ListModels != "GET /v1/models" {
		t.Fatalf("ListModels = %q", protocol.ListModels)
	}
}
