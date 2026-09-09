package protocol

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

const RegistrationLimit = 4096

// RegistrationCode is a bearer credential. It must never be logged or saved.
type RegistrationCode struct {
	Version     int    `json:"version"`
	ID          string `json:"id"`
	Address     string `json:"address"`
	User        string `json:"user"`
	DaemonPort  int    `json:"daemonPort"`
	Fingerprint string `json:"fingerprint"`
	Seed        []byte `json:"seed"`
	Expires     int64  `json:"expires"`
}

func NewRegistrationCode(address, user, fingerprint string, port int, now time.Time) (RegistrationCode, error) {
	c := RegistrationCode{Version: 1, Address: address, User: user, Fingerprint: fingerprint, DaemonPort: port, Expires: now.Add(5 * time.Minute).Unix(), Seed: make([]byte, ed25519.SeedSize)}
	if _, err := rand.Read(c.Seed); err != nil {
		return RegistrationCode{}, err
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return RegistrationCode{}, err
	}
	c.ID = hex.EncodeToString(id)
	return c, c.Validate()
}

func (c RegistrationCode) Encode() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "dispatch1." + base64.RawURLEncoding.EncodeToString(b) + "." + hex.EncodeToString(sum[:8]), nil
}

func ParseRegistrationCode(raw string) (RegistrationCode, error) {
	bad := errors.New("invalid registration code; copy a fresh code from the Host")
	if len(raw) > RegistrationLimit {
		return RegistrationCode{}, bad
	}
	parts := strings.Split(strings.TrimSpace(raw), ".")
	if len(parts) != 3 || parts[0] != "dispatch1" {
		return RegistrationCode{}, bad
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return RegistrationCode{}, bad
	}
	sum := sha256.Sum256(b)
	if parts[2] != hex.EncodeToString(sum[:8]) {
		return RegistrationCode{}, bad
	}
	var c RegistrationCode
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Validate() != nil {
		return RegistrationCode{}, bad
	}
	return c, nil
}

func (c RegistrationCode) Validate() error {
	id, err := hex.DecodeString(c.ID)
	host, port, addrErr := net.SplitHostPort(c.Address)
	p, portErr := strconv.Atoi(port)
	fp, fpErr := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(c.Fingerprint, "SHA256:"))
	if c.Version != 1 || err != nil || len(id) != 16 || len(c.Seed) != ed25519.SeedSize || host == "" || len(c.Address) > 255 || addrErr != nil || portErr != nil || p < 1 || p > 65535 || c.DaemonPort < 1 || c.DaemonPort > 65535 || c.User == "" || len(c.User) > 128 || strings.ContainsAny(c.User, "\r\n\x00") || !strings.HasPrefix(c.Fingerprint, "SHA256:") || fpErr != nil || len(fp) != 32 || c.Expires <= 0 {
		return errors.New("invalid registration code fields")
	}
	return nil
}

// RegistrationRequest is signed by the temporary key for claim/abort and by
// the permanent key for completion. No private key crosses this interface.
type RegistrationRequest struct {
	ID        string `json:"id"`
	Action    string `json:"action"`
	PublicKey []byte `json:"publicKey"`
	Signature []byte `json:"signature"`
}

func (r RegistrationRequest) SigningBytes() []byte {
	return []byte("dispatch-registration-v1\n" + r.ID + "\n" + r.Action + "\n" + base64.StdEncoding.EncodeToString(r.PublicKey))
}
