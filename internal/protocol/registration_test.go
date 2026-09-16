package protocol

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
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
	for _, bad := range []string{"", raw + "x", strings.Replace(raw, "dispatch3.", "dispatch4.", 1), strings.Repeat("x", RegistrationLimit+1), raw[:len(raw)/2]} {
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
		id, _ := hex.DecodeString(c.ID)
		v2 := append(id, c.Seed...)
		v2 = append(v2, make([]byte, 32)...)
		v2 = binary.BigEndian.AppendUint64(v2, uint64(c.Expires))
		v2 = binary.BigEndian.AppendUint16(v2, uint16(c.DaemonPort))
		v2 = append(v2, byte(len(address)))
		v2 = append(v2, address...)
		v2 = append(v2, c.User...)
		compact, err := c.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(compact, "dispatch3.") || len(compact) >= len(legacy) {
			t.Fatal("expected a shorter compact code")
		}
		t.Logf("%d address bytes: %d -> %d characters", len(address), len(legacy), len(compact))
		for _, raw := range []string{legacy, registrationTestCode("dispatch2", v2), compact, "\n " + compact + " \n"} {
			want := c
			if strings.HasPrefix(strings.TrimSpace(raw), "dispatch3.") {
				want.Fingerprint = "SHA256-128:" + base64.RawStdEncoding.EncodeToString(make([]byte, 16))
				want.Expires = 0
			}
			got, err := ParseRegistrationCode(raw)
			if err != nil || !reflect.DeepEqual(got, want) {
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
	for n := 0; n <= 67+len(c.Address); n++ {
		if _, err := ParseRegistrationCode(registrationTestCode("dispatch3", payload[:n])); err == nil {
			t.Fatalf("accepted truncated payload of %d bytes", n)
		}
	}
	for _, change := range []func([]byte){
		func(b []byte) { b[66] = 255 },
		func(b []byte) { b[64], b[65] = 0, 0 },
		func(b []byte) { b[len(b)-1] = '\n' },
	} {
		b := append([]byte(nil), payload...)
		change(b)
		if _, err := ParseRegistrationCode(registrationTestCode("dispatch3", b)); err == nil {
			t.Error("accepted invalid fields with a valid checksum")
		}
	}
}

func TestRegistrationFingerprintMatching(t *testing.T) {
	fingerprint := sha256.Sum256([]byte("Host public key"))
	full := "SHA256:" + base64.RawStdEncoding.EncodeToString(fingerprint[:])
	c, err := NewRegistrationCode("127.0.0.1:22", "localuser", full, 7717, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	compact, err := ParseRegistrationCode(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []RegistrationCode{c, compact} {
		if !code.MatchesFingerprint(full) || code.MatchesFingerprint("") || code.MatchesFingerprint(compact.Fingerprint) {
			t.Fatal("incorrect fingerprint match")
		}
		for i := 0; i < 16; i++ {
			other := fingerprint
			other[i] ^= 1
			if code.MatchesFingerprint("SHA256:" + base64.RawStdEncoding.EncodeToString(other[:])) {
				t.Fatal("accepted a changed fingerprint byte")
			}
		}
	}
	other := fingerprint
	other[31] ^= 1
	changed := "SHA256:" + base64.RawStdEncoding.EncodeToString(other[:])
	if c.MatchesFingerprint(changed) || !compact.MatchesFingerprint(changed) {
		t.Fatal("incorrect full or truncated fingerprint comparison")
	}
	c.Expires += 3600
	later, err := c.Encode()
	if err != nil || later != raw || compact.Expires != 0 {
		t.Fatal("expiry leaked into the compact code", err)
	}
}
