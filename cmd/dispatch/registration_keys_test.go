package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

func TestRegistrationFilesPreserveUnrelatedAuthorization(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "authorized_keys")
	original := "# user comment\n\nssh-ed25519 unrelated-key user-comment\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	k := &registrationKeys{path: path, exe: `C:\Program Files\Dispatch\dispatch.exe`, port: 7717}
	c, err := protocol.NewRegistrationCode("127.0.0.1:22", "localuser", "SHA256:"+base64.RawStdEncoding.EncodeToString(make([]byte, 32)), 7717, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	if err := k.Temporary(c); err != nil {
		t.Fatal(err)
	}
	if err := k.Claim(c.ID, pub, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := k.Claim(c.ID, pub, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := k.Complete(c.ID, pub); err != nil {
		t.Fatal(err)
	}
	if err := k.Remove(c.ID); err != nil {
		t.Fatal(err)
	}
	if err := k.sweep(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), original) || strings.Contains(string(b), "-temporary") || strings.Contains(string(b), "-pending") || !k.Completed(c.ID, pub) {
		t.Fatal("cleanup changed unrelated or completed authorization")
	}
}

func TestRegistrationLockIsReleasedWhenHandleCloses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	first, err := lockRegistration(path)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := lockRegistration(path); err == nil {
		second.Close()
		t.Fatal("second owner acquired lock")
	}
	first.Close()
	third, err := lockRegistration(path)
	if err != nil {
		t.Fatal(err)
	}
	third.Close()
}
