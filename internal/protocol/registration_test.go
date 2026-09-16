package protocol

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"reflect"
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
	for _, bad := range []string{"", raw + "x", strings.Replace(raw, "dispatch2.", "dispatch3.", 1), strings.Repeat("x", RegistrationLimit+1), raw[:len(raw)/2]} {
		if _, err := ParseRegistrationCode(bad); err == nil {
			t.Error("accepted damaged code")
		}
	}
	c.Seed = []byte("123456")
	if _, err := c.Encode(); err == nil {
		t.Error("accepted a short key seed")
	}
}

func registrationTestCode(prefix string, payload []byte) string {
	sum := sha256.Sum256(payload)
	return prefix + "." + base64.RawURLEncoding.EncodeToString(payload) + "." + hex.EncodeToString(sum[:8])
}

func TestRegistrationCodeFormats(t *testing.T) {
	for _, address := range []string{"192.168.1.20:22", "[2001:db8::1]:2222", "host.example.com:22", strings.Repeat("a", 252) + ":22"} {
		c, err := NewRegistrationCode(address, "localuser", "SHA256:"+base64.RawStdEncoding.EncodeToString(make([]byte, 32)), 65535, time.Unix(1800000000, 0))
		if err != nil {
			t.Fatal(err)
		}
		legacyPayload, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		legacy := registrationTestCode("dispatch1", legacyPayload)
		compact, err := c.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(compact, "dispatch2.") || len(compact) >= len(legacy) {
			t.Fatal("expected a shorter compact code")
		}
		t.Logf("%d address bytes: %d -> %d characters", len(address), len(legacy), len(compact))
		for _, raw := range []string{legacy, compact, "\n " + compact + " \n"} {
			got, err := ParseRegistrationCode(raw)
			if err != nil || !reflect.DeepEqual(got, c) {
				t.Fatal("registration fields changed", err)
			}
		}
	}
}

func TestCompactRegistrationRejectsMalformedPayload(t *testing.T) {
	c, err := NewRegistrationCode("127.0.0.1:22", "localuser", "SHA256:"+base64.RawStdEncoding.EncodeToString(make([]byte, 32)), 7717, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.Split(raw, ".")[1])
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n <= 91+len(c.Address); n++ {
		if _, err := ParseRegistrationCode(registrationTestCode("dispatch2", payload[:n])); err == nil {
			t.Fatalf("accepted truncated payload of %d bytes", n)
		}
	}
	for _, change := range []func([]byte){
		func(b []byte) { b[90] = 255 },
		func(b []byte) { b[88], b[89] = 0, 0 },
		func(b []byte) { clear(b[80:88]) },
		func(b []byte) { b[80] = 255 },
		func(b []byte) { b[len(b)-1] = '\n' },
	} {
		b := append([]byte(nil), payload...)
		change(b)
		if _, err := ParseRegistrationCode(registrationTestCode("dispatch2", b)); err == nil {
			t.Error("accepted invalid fields with a valid checksum")
		}
	}
}
