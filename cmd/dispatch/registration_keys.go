package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"golang.org/x/crypto/ssh"
)

type registrationKeys struct {
	path, exe string
	port      int
	lock      *os.File
}

func (k *registrationKeys) Close() error {
	if k.lock != nil {
		return k.lock.Close()
	}
	return nil
}

func registrationMark(id string) string { return " dispatch-registration-" + id }

func publicLine(pub []byte) string {
	k, _ := ssh.NewPublicKey(ed25519.PublicKey(pub))
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
}

func (k *registrationKeys) Temporary(c protocol.RegistrationCode) error {
	pub := ed25519.NewKeyFromSeed(c.Seed).Public().(ed25519.PublicKey)
	script := "& '" + strings.ReplaceAll(k.exe, "'", "''") + "' registration-relay -port " + fmt.Sprint(k.port)
	units := utf16.Encode([]rune(script))
	b := make([]byte, len(units)*2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(b[i*2:], u)
	}
	command := "powershell.exe -NoProfile -NonInteractive -EncodedCommand " + base64.StdEncoding.EncodeToString(b)
	line := fmt.Sprintf("restrict,command=\"%s\",expiry-time=\"%s\" %s%s-temporary", command, time.Unix(c.Expires, 0).UTC().Format("20060102150405Z"), publicLine(pub), registrationMark(c.ID))
	return k.edit(func(lines []string) ([]string, error) { return append(lines, line), nil })
}

func (k *registrationKeys) Claim(id string, pub []byte, until time.Time) error {
	return k.edit(func(lines []string) ([]string, error) {
		for _, line := range lines {
			key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
			if err == nil && bytes.Equal(key.Marshal(), mustPublic(pub).Marshal()) && !strings.HasSuffix(line, registrationMark(id)+"-pending") {
				return nil, errors.New("this Hub key already has authorization; use the existing Host profile")
			}
		}
		lines = without(lines, registrationMark(id)+"-pending")
		line := fmt.Sprintf("expiry-time=\"%s\" %s%s-pending", until.UTC().Format("20060102150405Z"), publicLine(pub), registrationMark(id))
		return append(lines, line), nil
	})
}

func mustPublic(pub []byte) ssh.PublicKey { k, _ := ssh.NewPublicKey(ed25519.PublicKey(pub)); return k }

func (k *registrationKeys) Complete(id string, pub []byte) error {
	return k.edit(func(lines []string) ([]string, error) {
		found := false
		for _, line := range lines {
			if strings.HasSuffix(line, registrationMark(id)+"-pending") {
				found = true
			}
		}
		if !found {
			return nil, errors.New("no pending authorization")
		}
		lines = without(without(lines, registrationMark(id)+"-temporary"), registrationMark(id)+"-pending")
		return append(lines, publicLine(pub)+registrationMark(id)+"-complete"), nil
	})
}

func (k *registrationKeys) Remove(id string) error {
	return k.edit(func(lines []string) ([]string, error) {
		return without(without(lines, registrationMark(id)+"-temporary"), registrationMark(id)+"-pending"), nil
	})
}

func (k *registrationKeys) Completed(id string, pub []byte) bool {
	if len(id) != 32 {
		return false
	}
	b, err := os.ReadFile(k.path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == publicLine(pub)+registrationMark(id)+"-complete" {
			return true
		}
	}
	return false
}

func without(lines []string, suffix string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if !strings.HasSuffix(strings.TrimSpace(line), suffix) {
			out = append(out, line)
		}
	}
	return out
}

func (k *registrationKeys) sweep() error {
	return k.edit(func(lines []string) ([]string, error) {
		out := make([]string, 0, len(lines))
		for _, line := range lines {
			if strings.Contains(line, " dispatch-registration-") && (strings.HasSuffix(strings.TrimSpace(line), "-temporary") || strings.HasSuffix(strings.TrimSpace(line), "-pending")) {
				continue
			}
			out = append(out, line)
		}
		return out, nil
	})
}

func (k *registrationKeys) edit(change func([]string) ([]string, error)) error {
	b, err := os.ReadFile(k.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if bytes.HasPrefix(b, []byte{0xff, 0xfe}) || bytes.Contains(b, []byte{0}) {
		return errors.New("authorized_keys must use UTF-8")
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	updated, err := change(lines)
	if err != nil {
		return err
	}
	// Re-read before replacement so an observed external edit is never overwritten.
	now, readErr := os.ReadFile(k.path)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	if !bytes.Equal(now, b) {
		return errors.New("authorized_keys changed during registration; retry")
	}
	return writeRegistrationFile(k.path, []byte(strings.Join(updated, "\n")+"\n"))
}

func writeRegistrationFile(path string, body []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".dispatch-registration-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
