package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"golang.org/x/crypto/ssh"
)

// sshHostKey serves SSH on loopback with one host key and refuses every login.
// Only the host key matters to the check under test.
func sshHostKey(t *testing.T) (string, ssh.PublicKey) {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	// Authentication always fails. The host key is sent during key exchange,
	// before that, which is what the check under test reads.
	config := &ssh.ServerConfig{PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
		return nil, errors.New("no login here")
	}}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				ssh.NewServerConn(conn, config)
			}()
		}
	}()
	return listener.Addr().String(), signer.PublicKey()
}

func addressCode(t *testing.T, address string, key ssh.PublicKey) protocol.RegistrationCode {
	t.Helper()
	c, err := protocol.NewRegistrationCode(address, "victor", ssh.FingerprintSHA256(key), 7700, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAnAddressServedByAnotherSSHIsRefused(t *testing.T) {
	address, _ := sshHostKey(t)
	_, other := sshHostKey(t)
	err := checkAdvertisedAddress(addressCode(t, address, other), other)
	if err == nil {
		t.Fatal("a code naming another machine's SSH was accepted")
	}
	if !strings.Contains(err.Error(), ssh.FingerprintSHA256(other)) {
		t.Errorf("error = %v, want it to name the Daemon's own key", err)
	}
}

func TestAnAddressServedByThisSSHIsAccepted(t *testing.T) {
	address, key := sshHostKey(t)
	if err := checkAdvertisedAddress(addressCode(t, address, key), key); err != nil {
		t.Fatal(err)
	}
}

// A Host that cannot reach its own advertised address proves nothing about it,
// and the Client may still reach it.
func TestAnAddressThisHostCannotReachIsAccepted(t *testing.T) {
	_, key := sshHostKey(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	unreachable := listener.Addr().String()
	listener.Close()
	if err := checkAdvertisedAddress(addressCode(t, unreachable, key), key); err != nil {
		t.Fatal(err)
	}
}
