package main

import (
	"strings"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

func testRegistrationCode(t *testing.T) string {
	t.Helper()
	c, err := protocol.NewRegistrationCode("192.168.1.10:22", "victor", "SHA256:"+strings.Repeat("A", 43), 7777, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	code, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestRegistrationQRIsSquareAndBordered(t *testing.T) {
	art, err := registrationQR(testRegistrationCode(t))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(art, "\n"), "\n")
	width := strings.Count(lines[0], "▀")
	for i, line := range lines {
		if strings.Count(line, "▀") != width {
			t.Fatalf("line %d has a different width", i)
		}
	}
	if len(lines) != (width+1)/2 {
		t.Fatalf("got %d rows for %d columns", len(lines), width)
	}
	// The quiet zone is four light modules, so the first two rows are blank.
	if strings.Count(lines[0], qrBothLight) != width || strings.Count(lines[1], qrBothLight) != width {
		t.Fatal("the quiet zone is not blank")
	}
}

func TestRegistrationQRSkipsARedirectedFile(t *testing.T) {
	var b strings.Builder
	writeRegistrationQR(&b, testRegistrationCode(t))
	if b.Len() != 0 {
		t.Fatal("a writer that is not a terminal must get no escapes")
	}
}
