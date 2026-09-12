package protocol

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestRegistrationCodeRejectsCopyErrors(t *testing.T) {
	c, err := NewRegistrationCode("[::1]:2222", "localuser", "SHA256:"+base64.RawStdEncoding.EncodeToString(make([]byte, 32)), 7717, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseRegistrationCode(raw)
	if err != nil || got.ID != c.ID || string(got.Seed) != string(c.Seed) {
		t.Fatal("code did not round trip", err)
	}
	for _, bad := range []string{"", raw + "x", strings.Replace(raw, "dispatch1.", "dispatch2.", 1), strings.Repeat("x", RegistrationLimit+1), raw[:len(raw)/2]} {
		if _, err := ParseRegistrationCode(bad); err == nil {
			t.Error("accepted damaged code")
		}
	}
	c.Seed = []byte("123456")
	if _, err := c.Encode(); err == nil {
		t.Error("accepted a short key seed")
	}
}
