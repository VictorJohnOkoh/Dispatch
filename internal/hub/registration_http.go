package hub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// The two ways a Host is registered. Code is the zero value, so a caller that
// names no Method gets the code and password way.
const (
	RegisterByCode  = "code"
	RegisterByLogin = "existing"
)

type RegistrationInput struct {
	ID      string `json:"id"`
	Address string `json:"address"`

	// Method selects the way. Code carries the account and the Daemon port, so
	// the existing-login way asks for them instead.
	Method string `json:"method"`

	Code string `json:"code"`

	// User replaces the account the code names, when SSH and the Daemon run as
	// two accounts.
	User       string `json:"user"`
	DaemonPort int    `json:"daemonPort"`

	// Password is the Host account's password, used once to install this Hub's
	// key. It is never written to the configuration or to the log.
	Password string `json:"password"`
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
	if err := in.check(); err != nil {
		http.Error(w, err.Error(), 400)
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

// check asks for what the selected way needs and refuses what it does not use,
// so a password sent to a way that never reads one is an error and not a secret
// that travelled for nothing.
func (in RegistrationInput) check() error {
	if len(in.User) > 64 {
		return errors.New("the account name is too long")
	}
	switch in.Method {
	case "", RegisterByCode:
		if in.Code == "" || in.Password == "" {
			return errors.New("enter the code from the Host and the password of its account")
		}
		if in.DaemonPort != 0 {
			return errors.New("a registration code carries the Daemon port")
		}
		return nil
	case RegisterByLogin:
		if in.Code != "" || in.Password != "" {
			return errors.New("the existing-login way takes no code and no password")
		}
		if in.Address == "" {
			return errors.New("this way of registering needs the Host's SSH address")
		}
		if in.User == "" {
			return errors.New("name the account on the Host")
		}
		if in.DaemonPort < 1 || in.DaemonPort > 65535 {
			return errors.New("name the port the Daemon listens on")
		}
		return nil
	default:
		return errors.New("unknown way of registering a Host")
	}
}
