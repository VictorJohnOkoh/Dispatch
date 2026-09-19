package daemon

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// RegistrationKeys changes only the authorization owned by one registration.
// The command boundary selects and checks the Windows account and file.
type RegistrationKeys interface {
	Temporary(protocol.RegistrationCode) error
	Claim(string, []byte, time.Time) error
	Complete(string, []byte) error
	Remove(string) error
	Completed(string, []byte) bool
}

type HostRegistration struct {
	mu       sync.Mutex
	keys     RegistrationKeys
	pending  *protocol.RegistrationCode
	claimed  []byte
	deadline time.Time
	now      func() time.Time
}

func NewHostRegistration(keys RegistrationKeys) *HostRegistration {
	return &HostRegistration{keys: keys, now: time.Now}
}

func (s *HostRegistration) Begin(address, user, fingerprint string, port int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending != nil {
		return "", errors.New("Host Registration is already active")
	}
	c, err := protocol.NewRegistrationCode(address, user, fingerprint, port, s.now())
	if err != nil {
		return "", err
	}
	if err := s.keys.Temporary(c); err != nil {
		return "", err
	}
	s.pending = &c
	s.deadline = time.Unix(c.Expires, 0)
	return c.Encode()
}

// Cancel runs locally and on expiry. A failed file cleanup keeps the record so
// the next sweep can try again; the deadline still refuses remote requests.
func (s *HostRegistration) Cancel() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancel()
}

func (s *HostRegistration) Close() error {
	err := s.Cancel()
	if closer, ok := s.keys.(io.Closer); ok {
		return errors.Join(err, closer.Close())
	}
	return err
}

func (s *HostRegistration) cancel() error {
	if s.pending == nil {
		return nil
	}
	s.deadline = time.Time{}
	if err := s.keys.Remove(s.pending.ID); err != nil {
		return err
	}
	s.pending = nil
	s.claimed = nil
	return nil
}

func (s *HostRegistration) Expire() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending != nil && !s.now().Before(s.deadline) {
		return s.cancel()
	}
	return nil
}

func (s *HostRegistration) Apply(r protocol.RegistrationRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	refused := errors.New("registration is expired, cancelled, or claimed by another key")
	if len(r.PublicKey) != ed25519.PublicKeySize {
		return refused
	}
	if r.Action == "complete" && ed25519.Verify(r.PublicKey, r.SigningBytes(), r.Signature) && s.keys.Completed(r.ID, r.PublicKey) {
		return nil
	}
	if s.pending == nil || r.ID != s.pending.ID || !s.now().Before(s.deadline) {
		return refused
	}
	pub := ed25519.NewKeyFromSeed(s.pending.Seed).Public().(ed25519.PublicKey)
	if r.Action == "complete" {
		pub = r.PublicKey
	}
	if !ed25519.Verify(pub, r.SigningBytes(), r.Signature) {
		return refused
	}
	if s.claimed != nil && !equalKey(s.claimed, r.PublicKey) {
		return refused
	}
	switch r.Action {
	case "claim":
		if s.claimed != nil {
			return s.keys.Claim(r.ID, r.PublicKey, s.deadline)
		}
		// Bind before a file write, including a write whose outcome is uncertain.
		s.claimed = append([]byte(nil), r.PublicKey...)
		s.deadline = s.now().Add(2 * time.Minute)
		return s.keys.Claim(r.ID, r.PublicKey, s.deadline)
	case "complete":
		if s.claimed == nil {
			return refused
		}
		if err := s.keys.Complete(r.ID, r.PublicKey); err != nil {
			return err
		}
		s.pending = nil
		s.claimed = nil
		return nil
	case "abort":
		return s.cancel()
	default:
		return refused
	}
}

func equalKey(a, b []byte) bool { return string(a) == string(b) }

func (s *HostRegistration) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(10 * time.Second))
	r.Body = http.MaxBytesReader(w, r.Body, protocol.RegistrationLimit)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	var req protocol.RegistrationRequest
	if d.Decode(&req) != nil || d.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid registration request", 400)
		return
	}
	if s.Apply(req) != nil {
		http.Error(w, "registration refused; check the Host and use a fresh code", 409)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
