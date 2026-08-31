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

func get(t *testing.T, d *Daemon, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	d.handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestListModelsAnswersFromTheCache(t *testing.T) {
	f := ollamaFake()
	d := plain([]vendors.Adapter{f}, quiet())
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
	d := plain([]vendors.Adapter{ollamaFake()}, quiet())

	w := get(t, d, "/v1/models")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if got := w.Body.String(); !strings.Contains(got, `"at":0`) {
		t.Errorf("body = %s", got)
	}
}

func TestPprofAnswersOnTheDaemonsListener(t *testing.T) {
	d := plain(nil, quiet())
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
