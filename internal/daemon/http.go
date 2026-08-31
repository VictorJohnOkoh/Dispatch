package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/pprof"

	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// handler is the Daemon's mux. Nine of ADR 0009's ten endpoints are not here yet.
func (d *Daemon) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(protocol.ListModels, d.listModels)

	// pprof is the whole of SPEC.md's process-level observability. The five are
	// named one by one rather than by handing the listener http.DefaultServeMux,
	// which pprof's own init writes to and so could anything else.
	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	return mux
}

// listModels answers from the cache the poll fills. It calls no Vendor, so a
// Vendor that is slow or gone cannot make this request slow or gone.
func (d *Daemon) listModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, struct {
		Vendors []CatalogueView `json:"vendors"`
	}{d.vendors.Catalogue()})
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(body)
}
