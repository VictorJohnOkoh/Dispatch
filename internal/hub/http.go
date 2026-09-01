package hub

import (
	"bufio"
	"context"
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
		// The dialer names why it could not reach the Host, and the answer carries
		// that name. Until Host State lands, this is the only place the user sees
		// the difference between a Host that is off and a Daemon that is not running.
		http.Error(w, "the Host is not Ready: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer conn.Close()
	stop := context.AfterFunc(r.Context(), func() { conn.Close() })
	defer stop()

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
	var destination io.Writer = w
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		flush, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "this server cannot stream", http.StatusInternalServerError)
			return
		}
		destination = flushWriter{Writer: w, flush: flush}
	}
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	io.Copy(destination, resp.Body)
}

type flushWriter struct {
	io.Writer
	flush http.Flusher
}

func (w flushWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	w.flush.Flush()
	return n, err
}

func copyHeaders(to, from http.Header) {
	for name, values := range from {
		to[name] = append([]string(nil), values...)
	}
}
