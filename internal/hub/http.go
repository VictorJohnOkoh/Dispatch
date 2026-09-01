package hub

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

func (h *Hub) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(protocol.ListHosts, h.listHosts)
	mux.HandleFunc(protocol.StreamEvents, h.stream)
	for _, route := range protocol.Routes {
		mux.HandleFunc(protocol.OnHost(route), h.forward)
	}
	return mux
}

func (h *Hub) listHosts(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Hosts []hostset.Host `json:"hosts"`
	}{Hosts: h.hosts.All()})
}

// forward serves every Daemon endpoint. The route table selects the Host and this
// function removes only that Host segment before it writes the same request.
func (h *Hub) forward(w http.ResponseWriter, r *http.Request) {
	id := hostset.HostID(r.PathValue("host"))
	if _, ok := h.hosts.Find(id); !ok {
		http.Error(w, "no such Host", http.StatusNotFound)
		return
	}
	conn, err := h.dialer.Dial(r.Context(), id)
	if err != nil {
		http.Error(w, "the Host is not Ready", http.StatusServiceUnavailable)
		return
	}
	defer conn.Close()

	out := r.Clone(r.Context())
	out.RequestURI = ""
	out.URL.Scheme = ""
	out.URL.Host = ""
	out.URL.Path = "/v1" + strings.TrimPrefix(r.URL.Path, "/v1/hosts/"+string(id))
	out.Host = "daemon"
	if err := out.Write(conn); err != nil {
		http.Error(w, "the Host connection failed", http.StatusBadGateway)
		return
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), out)
	if err != nil {
		http.Error(w, "the Host response failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func copyHeaders(to, from http.Header) {
	for name, values := range from {
		to[name] = append([]string(nil), values...)
	}
}
