package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"golang.org/x/crypto/ssh"
)

// hostRegistration makes the code that names this Host. It reads the public Host
// key and changes nothing, so the code is safe to show and to use twice.
func hostRegistration(address string, port int) (string, bool, error) {
	user, admin, err := registrationAccount()
	if err != nil {
		return "", false, err
	}
	keyPath := registrationHostKeyPath()
	pub, err := os.ReadFile(keyPath)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return "", false, fmt.Errorf("read the OpenSSH ed25519 Host public key at %s: %s", keyPath, registrationHostKeyAdvice(keyPath, user))
		}
		return "", false, fmt.Errorf("read the OpenSSH ed25519 Host public key: %w", err)
	}
	key, _, _, _, err := ssh.ParseAuthorizedKey(pub)
	if err != nil || key.Type() != ssh.KeyAlgoED25519 {
		return "", false, errors.New("OpenSSH must have an ed25519 Host key")
	}
	c := protocol.RegistrationCode{Address: withPort(address), User: user, DaemonPort: port, Fingerprint: ssh.FingerprintSHA256(key)}
	if err := checkAdvertisedAddress(c, key); err != nil {
		return "", false, err
	}
	code, err := c.Encode()
	return code, admin, err
}

// The Host proves the SSH on its own loopback, but the code names the address a
// Client will dial. One machine can serve one SSH on loopback and another on
// that address: a Daemon inside WSL beside the Windows OpenSSH is the usual way,
// and a wrong -host-reg address is the other. The Client sees only a key that
// does not match the code, so the Host says it here instead.
func checkAdvertisedAddress(c protocol.RegistrationCode, hostKey ssh.PublicKey) error {
	conn, err := net.DialTimeout("tcp", c.Address, 10*time.Second)
	if err != nil {
		// A Host that cannot reach its own advertised address proves nothing about
		// it. The Client may still reach it, so this is not a refusal.
		return nil
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(20 * time.Second))
	var served ssh.PublicKey
	keep := func(_ string, _ net.Addr, key ssh.PublicKey) error { served = key; return nil }
	// Authentication is offered nothing and fails. The host key arrives first,
	// which is the whole of what this asks for.
	sshConn, channels, requests, _ := ssh.NewClientConn(conn, c.Address, &ssh.ClientConfig{User: c.User, HostKeyCallback: keep, HostKeyAlgorithms: []string{ssh.KeyAlgoED25519}})
	if sshConn != nil {
		ssh.NewClient(sshConn, channels, requests).Close()
	}
	if served == nil || bytes.Equal(served.Marshal(), hostKey.Marshal()) {
		return nil
	}
	return fmt.Errorf("%s is served by a different SSH than this one: it presents %s and this Daemon's SSH is %s; register the address of the SSH this Daemon uses, and run the Daemon on the machine that serves it", c.Address, ssh.FingerprintSHA256(served), ssh.FingerprintSHA256(hostKey))
}
