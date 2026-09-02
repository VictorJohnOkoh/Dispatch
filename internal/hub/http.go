package hub

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// The four ways a request to a Host fails, kept apart because a Host that is not
// configured, a Host that is off, and a Daemon that took the request but answered
// badly are different things for the user to do something about.
var (
	errNoHost    = errors.New("no such Host")
	errNotReady  = errors.New("the Host is not Ready")
	errSendFail  = errors.New("the Host connection failed")
	errReplyFail = errors.New("the Host response failed")
)

func (h *Hub) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(protocol.ListHosts, h.listHosts)
	mux.HandleFunc(protocol.StreamEvents, h.stream)
	for _, route := range protocol.Routes {
		mux.HandleFunc(protocol.OnHost(route), h.forward)
	}
	// The Client is everything the protocol does not claim. It is last because a
	// pattern of "GET /" matches whatever the ten above did not.
	mux.Handle("GET /", h.page)
	return mux
}

// All is the Hosts this Hub is configured with, by id. It is what the Client
// builds its rail from, and it is the only way that package learns a second Host
// exists.
func (h *Hub) All() []string {
	hosts := h.hosts.All()
	out := make([]string, len(hosts))
	for i, host := range hosts {
		out[i] = string(host.ID)
	}
	return out
}

func (h *Hub) listHosts(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Hosts []hostset.Host `json:"hosts"`
	}{Hosts: h.hosts.All()})
}

// Get runs one GET against a named Host's Daemon and answers as that Daemon
// answered. It is what the Client's first paint reads the transcript with, and
// closing the answer's body releases the connection it came on.
//
// A Host that did not answer comes back as an answer too, carrying the same status
// forward serves, so the one place that decides what a failed Host looks like is
// statusOf and the caller reads one shape.
func (h *Hub) Get(ctx context.Context, host, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://daemon"+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.roundTrip(ctx, hostset.HostID(host), req)
	if err != nil {
		return refused(statusOf(err), err.Error()), nil
	}
	return resp, nil
}

// refused is a Host failure written as the answer it would have had.
func refused(status int, text string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}},
		Body:       io.NopCloser(strings.NewReader(text)),
	}
}

// forward serves every Daemon endpoint. The route table selects the Host and this
// function removes only that Host segment before it writes the same request.
func (h *Hub) forward(w http.ResponseWriter, r *http.Request) {
	id := hostset.HostID(r.PathValue("host"))
	out := r.Clone(r.Context())
	out.URL.Path = "/v1" + strings.TrimPrefix(r.URL.Path, "/v1/hosts/"+string(id))

	resp, err := h.roundTrip(r.Context(), id, out)
	if err != nil {
		// The answer carries the dialer's own words. Until Host State lands, this is
		// the only place the user sees the difference between a Host that is off and
		// a Daemon that is not running.
		http.Error(w, err.Error(), statusOf(err))
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

// roundTrip writes one request to a Host's Daemon and reads its answer. The
// request is rewritten for a connection that is already this Host's, so it carries
// no scheme and no address.
func (h *Hub) roundTrip(ctx context.Context, id hostset.HostID, req *http.Request) (*http.Response, error) {
	if _, ok := h.hosts.Find(id); !ok {
		return nil, errNoHost
	}
	conn, err := h.dialer.Dial(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errNotReady, err)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	release := func() {
		stop()
		conn.Close()
	}

	req.RequestURI = ""
	req.URL.Scheme = ""
	req.URL.Host = ""
	req.Host = "daemon"
	if err := req.Write(conn); err != nil {
		release()
		return nil, fmt.Errorf("%w: %w", errSendFail, err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		release()
		return nil, fmt.Errorf("%w: %w", errReplyFail, err)
	}
	resp.Body = hostBody{ReadCloser: resp.Body, conn: conn, stop: stop}
	return resp, nil
}

// statusOf is what the Client is told about a Host that did not answer.
func statusOf(err error) int {
	switch {
	case errors.Is(err, errNoHost):
		return http.StatusNotFound
	case errors.Is(err, errNotReady):
		return protocol.StatusHostNotReady
	default:
		return http.StatusBadGateway
	}
}

// hostBody ties an answer to the connection it arrived on, so one Close releases
// both and no caller has to know a Host connection serves one request.
type hostBody struct {
	io.ReadCloser
	conn net.Conn
	stop func() bool
}

func (b hostBody) Close() error {
	b.stop()
	b.ReadCloser.Close()
	return b.conn.Close()
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
