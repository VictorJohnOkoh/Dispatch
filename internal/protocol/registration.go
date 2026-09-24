package protocol

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"strconv"
	"strings"
)

const RegistrationLimit = 4096

// codePrefix names this layout. Codes from before the password step carried a
// key seed and are refused by name, so the user knows to take a fresh one.
const codePrefix = "dispatch4"

// RegistrationCode names one Host: where its SSH answers, which account to log in
// as, where its Daemon listens, and the Host key that proves the machine. It
// holds no secret. The password that goes with it does.
type RegistrationCode struct {
	Address     string
	User        string
	DaemonPort  int
	Fingerprint string // OpenSSH SHA256 form, as in "SHA256:..."
}

func (c RegistrationCode) Encode() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	fingerprint, _ := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(c.Fingerprint, "SHA256:"))
	b := binary.BigEndian.AppendUint16(fingerprint, uint16(c.DaemonPort))
	b = append(b, byte(len(c.Address)))
	b = append(b, c.Address...)
	b = append(b, c.User...)
	sum := sha256.Sum256(b)
	return codePrefix + "." + base64.RawURLEncoding.EncodeToString(b) + "." + hex.EncodeToString(sum[:8]), nil
}

// ParseRegistrationCode reads the layout Encode writes: 32 fingerprint bytes, the
// Daemon port, the address length, the address and the account. The checksum
// finds copy errors. It proves nothing, because the code is not a secret.
func ParseRegistrationCode(raw string) (RegistrationCode, error) {
	bad := errors.New("invalid registration code; copy the code from the Host again")
	if len(raw) > RegistrationLimit {
		return RegistrationCode{}, bad
	}
	parts := strings.Split(strings.TrimSpace(raw), ".")
	if len(parts) != 3 {
		return RegistrationCode{}, bad
	}
	if parts[0] != codePrefix {
		if strings.HasPrefix(parts[0], "dispatch") {
			return RegistrationCode{}, errors.New("this registration code is from an older Dispatch; start the Daemon with this build and take its code")
		}
		return RegistrationCode{}, bad
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return RegistrationCode{}, bad
	}
	sum := sha256.Sum256(b)
	if parts[2] != hex.EncodeToString(sum[:8]) || len(b) < 35 || len(b) < 35+int(b[34]) {
		return RegistrationCode{}, bad
	}
	addressEnd := 35 + int(b[34])
	c := RegistrationCode{
		Fingerprint: "SHA256:" + base64.RawStdEncoding.EncodeToString(b[:32]),
		DaemonPort:  int(binary.BigEndian.Uint16(b[32:34])),
		Address:     string(b[35:addressEnd]),
		User:        string(b[addressEnd:]),
	}
	if c.Validate() != nil {
		return RegistrationCode{}, bad
	}
	return c, nil
}

func (c RegistrationCode) Validate() error {
	host, port, addrErr := net.SplitHostPort(c.Address)
	p, portErr := strconv.Atoi(port)
	encoded, isSHA256 := strings.CutPrefix(c.Fingerprint, "SHA256:")
	fp, fpErr := base64.RawStdEncoding.DecodeString(encoded)
	if host == "" || len(c.Address) > 255 || addrErr != nil || portErr != nil || p < 1 || p > 65535 || c.DaemonPort < 1 || c.DaemonPort > 65535 || c.User == "" || len(c.User) > 128 || strings.ContainsAny(c.User, "\r\n\x00") || !isSHA256 || fpErr != nil || len(fp) != 32 {
		return errors.New("invalid registration code fields")
	}
	return nil
}
