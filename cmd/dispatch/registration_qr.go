package main

import (
	"io"
	"os"
	"strings"

	"golang.org/x/term"
	"rsc.io/qr"
)

// One character is two module rows. The upper half block takes its top colour
// from the foreground and its bottom colour from the background. A QR reader
// needs dark on light, so both colours are named here and neither is taken
// from the terminal theme.
const (
	qrBothDark  = "\x1b[30;40m▀"
	qrTopDark   = "\x1b[30;107m▀"
	qrBotDark   = "\x1b[97;40m▀"
	qrBothLight = "\x1b[97;107m▀"
	qrReset     = "\x1b[0m"
)

// The quiet zone is the blank border a QR reader needs to find the code.
const qrQuietZone = 4

func registrationQR(code string) (string, error) {
	c, err := qr.Encode(code, qr.M)
	if err != nil {
		return "", err
	}
	size := c.Size + 2*qrQuietZone
	dark := func(x, y int) bool { return c.Black(x-qrQuietZone, y-qrQuietZone) }
	var b strings.Builder
	for y := 0; y < size; y += 2 {
		for x := 0; x < size; x++ {
			top, bottom := dark(x, y), y+1 < size && dark(x, y+1)
			switch {
			case top && bottom:
				b.WriteString(qrBothDark)
			case top:
				b.WriteString(qrTopDark)
			case bottom:
				b.WriteString(qrBotDark)
			default:
				b.WriteString(qrBothLight)
			}
		}
		b.WriteString(qrReset + "\n")
	}
	return b.String(), nil
}

// writeRegistrationQR draws the code only on a terminal. Colour escapes in a
// redirected file would damage the text code a script reads.
func writeRegistrationQR(out io.Writer, code string) {
	f, ok := out.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return
	}
	if art, err := registrationQR(code); err == nil {
		io.WriteString(out, art)
	}
}
