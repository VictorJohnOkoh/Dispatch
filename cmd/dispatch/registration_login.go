package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// The private keys OpenSSH looks for by default. A key with a passphrase is
// skipped rather than asked about, because the Hub has no one at the keyboard;
// an agent holding that key answers for it instead.
var clientKeyFiles = []string{"id_ed25519", "id_ecdsa", "id_rsa"}

func registerLoginHost(ctx context.Context, path string, h *hub.Hub, in hub.RegistrationInput) error {
	address, err := normalizeRegistrationAddress(withPort(in.Address))
	if err != nil {
		return err
	}
	var login hub.Login
	if login.Auth, err = clientLogins(); err != nil {
		return err
	}
	if known, err := defaultKnownHosts(); err == nil {
		login.Known = append(login.Known, known)
	}
	dir, err := hubSSHDir()
	if err != nil {
		return err
	}
	login.Known = append(login.Known, filepath.Join(dir, "known_hosts"))
	req := hub.Registration{ID: hub.HostID(in.ID), Address: address, User: in.User, DaemonPort: in.DaemonPort, Dir: dir}
	if _, err := hubWith(path, hub.Registered{ID: req.ID, Address: address, User: in.User, DaemonPort: in.DaemonPort}); err != nil {
		return err
	}
	var registered hub.Registered
	err = hub.RegisterLogin(ctx, req, login, func(host hub.Registered) error {
		if err := commitHost(path, host); err != nil {
			return err
		}
		registered = host
		return nil
	})
	if err != nil {
		return err
	}
	if err := h.Attach(registered); err != nil {
		return fmt.Errorf("Host saved; restart the Hub to attach it: %w", err)
	}
	return nil
}

// clientLogins collects the SSH credentials this account already has: the agent
// first, then the key files OpenSSH reads by default. These are what the user
// set up by hand, and the Hub borrows them once to install its own key.
func clientLogins() ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	if pipe, err := sshAgentPipe(); err == nil {
		if signers, err := agent.NewClient(pipe).Signers(); err == nil && len(signers) > 0 {
			methods = append(methods, ssh.PublicKeys(signers...))
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	for _, name := range clientKeyFiles {
		b, err := os.ReadFile(filepath.Join(home, ".ssh", name))
		if err != nil {
			continue
		}
		if signer, err := ssh.ParsePrivateKey(b); err == nil {
			methods = append(methods, ssh.PublicKeys(signer))
		}
	}
	if len(methods) == 0 {
		return nil, errors.New("this account has no SSH agent and no usable key in ~/.ssh, so it has no existing login to borrow; use the registration code")
	}
	return methods, nil
}
