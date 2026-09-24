package hostset

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"

	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"golang.org/x/crypto/ssh"
)

// RegisterCode registers a Host with the code it printed and the password of the
// account the code names. The code proves the machine: the Host key must match
// it before the password is sent, so a password never reaches the wrong Host.
func RegisterCode(ctx context.Context, req Registration, code protocol.RegistrationCode, password string, commit func(Registered) error) error {
	if err := code.Validate(); err != nil {
		return err
	}
	if err := req.validate(); err != nil {
		return err
	}
	if password == "" {
		return errors.New("enter the password of the account on the Host")
	}
	var hostKey ssh.PublicKey
	// The two ways trust can fail read the same to a machine and not at all the
	// same to a human, so each says which one it was and names the key it saw.
	check := func(address string, remote net.Addr, key ssh.PublicKey) error {
		if live := ssh.FingerprintSHA256(key); live != code.Fingerprint {
			return fmt.Errorf("%w: %s presents %s, which the code does not name; the code is from another Host, or from before this Host's SSH key changed, so take a fresh code from the Host", ErrHostKey, address, live)
		}
		if _, err := namedIn(append([]string{filepath.Join(req.Dir, trustFile)}, req.TrustFiles...), address, remote, key); err != nil {
			return err
		}
		hostKey = key
		return nil
	}
	answer := func(_, _ string, questions []string, _ []bool) ([]string, error) {
		answers := make([]string, len(questions))
		for i := range answers {
			answers[i] = password
		}
		return answers, nil
	}
	client, err := connect(ctx, req.Address, &ssh.ClientConfig{
		User: req.User,
		// Linux often turns password off and keyboard-interactive on, and asks the
		// same question through it.
		Auth:              []ssh.AuthMethod{ssh.Password(password), ssh.KeyboardInteractive(answer)},
		HostKeyCallback:   check,
		HostKeyAlgorithms: []string{ssh.KeyAlgoED25519},
		Timeout:           connectTimeout,
	})
	if err != nil {
		return fmt.Errorf("the password login: %w", err)
	}
	defer client.Close()
	return install(ctx, client, req, hostKey, commit)
}
