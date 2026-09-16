package protocol

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
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
	id, _ := hex.DecodeString(c.ID)
	fingerprint, _ := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(c.Fingerprint, "SHA256:"))
	b := append(id, c.Seed...)
	b = append(b, fingerprint...)
	b = binary.BigEndian.AppendUint64(b, uint64(c.Expires))
	b = binary.BigEndian.AppendUint16(b, uint16(c.DaemonPort))
	b = append(b, byte(len(c.Address)))
	b = append(b, c.Address...)
	b = append(b, c.User...)
	sum := sha256.Sum256(b)
	return "dispatch2." + base64.RawURLEncoding.EncodeToString(b) + "." + hex.EncodeToString(sum[:8]), nil
}

func ParseRegistrationCode(raw string) (RegistrationCode, error) {
	bad := errors.New("invalid registration code; copy a fresh code from the Host")
	if len(raw) > RegistrationLimit {
		return RegistrationCode{}, bad
	}
	parts := strings.Split(strings.TrimSpace(raw), ".")
	if len(parts) != 3 || (parts[0] != "dispatch1" && parts[0] != "dispatch2") {
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
	if parts[0] == "dispatch2" {
		// Fixed fields occupy 91 bytes, followed by the address and account.
		if len(b) < 91 || len(b) < 91+int(b[90]) {
			return RegistrationCode{}, bad
		}
		addressEnd := 91 + int(b[90])
		c = RegistrationCode{
			Version:     1,
			ID:          hex.EncodeToString(b[:16]),
			Seed:        b[16:48],
			Fingerprint: "SHA256:" + base64.RawStdEncoding.EncodeToString(b[48:80]),
			Expires:     int64(binary.BigEndian.Uint64(b[80:88])),
			DaemonPort:  int(binary.BigEndian.Uint16(b[88:90])),
			Address:     string(b[91:addressEnd]),
			User:        string(b[addressEnd:]),
		}
	} else {
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF {
			return RegistrationCode{}, bad
		}
	}
	if c.Validate() != nil {
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
