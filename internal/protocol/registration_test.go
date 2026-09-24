package protocol

import (
	"encoding/base64"
	"strings"
	"testing"
)

var testFingerprint = "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32))

func TestRegistrationCodeRoundTrips(t *testing.T) {
	for _, address := range []string{"192.168.1.20:22", "[2001:db8::1]:2222", "host.example.com:22", strings.Repeat("a", 252) + ":22"} {
		c := RegistrationCode{Address: address, User: "localuser", DaemonPort: 65535, Fingerprint: testFingerprint}
		raw, err := c.Encode()
		if err != nil {
			t.Fatal(err)
		}
		for _, pasted := range []string{raw, "\n " + raw + " \n"} {
			got, err := ParseRegistrationCode(pasted)
			if err != nil || got != c {
				t.Fatalf("%s: got %+v, %v", address, got, err)
			}
		}
	}
}

func TestRegistrationCodeRejectsCopyErrors(t *testing.T) {
	raw, err := RegistrationCode{Address: "[::1]:2222", User: "localuser", DaemonPort: 7717, Fingerprint: testFingerprint}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", raw + "x", strings.Replace(raw, "dispatch4.", "other.", 1), strings.Repeat("x", RegistrationLimit+1), raw[:len(raw)/2]} {
		if _, err := ParseRegistrationCode(bad); err == nil {
			t.Errorf("accepted a damaged code %q", bad)
		}
	}
}

func TestAnOlderCodeSaysToTakeAFreshOne(t *testing.T) {
	_, err := ParseRegistrationCode("dispatch3.AAAA.0011223344556677")
	if err == nil || !strings.Contains(err.Error(), "older Dispatch") {
		t.Fatalf("error = %v, want it to name an older Dispatch", err)
	}
}

func TestRegistrationCodeRefusesBadFields(t *testing.T) {
	good := RegistrationCode{Address: "host:22", User: "localuser", DaemonPort: 7717, Fingerprint: testFingerprint}
	for name, c := range map[string]RegistrationCode{
		"no port":           {Address: "host", User: good.User, DaemonPort: 7717, Fingerprint: testFingerprint},
		"no account":        {Address: good.Address, DaemonPort: 7717, Fingerprint: testFingerprint},
		"a newline account": {Address: good.Address, User: "a\nb", DaemonPort: 7717, Fingerprint: testFingerprint},
		"no Daemon port":    {Address: good.Address, User: good.User, Fingerprint: testFingerprint},
		"a short print":     {Address: good.Address, User: good.User, DaemonPort: 7717, Fingerprint: "SHA256:AAAA"},
		"an MD5 print":      {Address: good.Address, User: good.User, DaemonPort: 7717, Fingerprint: "MD5:" + testFingerprint[7:]},
	} {
		if _, err := c.Encode(); err == nil {
			t.Errorf("%s: encoded", name)
		}
	}
}
