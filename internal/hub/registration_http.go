package hub

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

type RegistrationInput struct {
	ID      string `json:"id"`
	Address string `json:"address"`
	Code    string `json:"code"`
}

func (h *Hub) WithRegistration(register func(context.Context, RegistrationInput) error) *Hub {
	h.register = register
	return h
}

func (h *Hub) Attach(host Registered) error {
	d, ok := h.dialer.(*SSHDialer)
	if !ok {
		return errNoHost
	}
	if err := d.Add(SSHProfile{ID: host.ID, Address: host.Address, User: host.User, KeyPath: host.KeyPath, KnownHosts: host.KnownHosts, DaemonPort: host.DaemonPort}, 10*time.Second); err != nil {
		return err
	}
	h.hosts.Add(Host{ID: host.ID})
	return nil
}

func (h *Hub) registerHost(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	peer, _, _ := net.SplitHostPort(r.RemoteAddr)
	host, _, err := net.SplitHostPort(r.Host)
	ip := net.ParseIP(host)
	origin, originErr := url.Parse(r.Header.Get("Origin"))
	media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || ip == nil || !ip.IsLoopback() || net.ParseIP(peer) == nil || !net.ParseIP(peer).IsLoopback() || originErr != nil || origin.Scheme != "http" || origin.Host != r.Host || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || origin.User != nil || media != "application/json" {
		http.Error(w, "open the Client using the Hub's loopback address", http.StatusForbidden)
		return
	}
	if h.register == nil {
		http.Error(w, "Host Registration is unavailable", 503)
		return
	}
	if !h.registerMu.TryLock() {
		http.Error(w, "another Host Registration is in progress", 409)
		return
	}
	defer h.registerMu.Unlock()
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(10 * time.Second))
	r.Body = http.MaxBytesReader(w, r.Body, protocol.RegistrationLimit+1024)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	var in RegistrationInput
	if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid registration input", 400)
		return
	}
	if !protocol.ValidHostID(in.ID) || len(in.Address) > 255 {
		http.Error(w, "invalid Host id or address", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	if err := h.register(ctx, in); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
