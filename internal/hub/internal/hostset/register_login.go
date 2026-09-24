package hostset

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Login is an SSH login to a Host that the user already has: an agent or a key
// file that OpenSSH on this machine already uses.
type Login struct {
	Auth []ssh.AuthMethod

	// Known are the known_hosts files that decide trust. A file that is not there
	// is skipped, because a machine with no SSH history has none of them.
	Known []string
}

// RegisterLogin registers a Host over a login the user already has. That login
// can write the account's keys, so the Hub installs its key itself. A Host that
// no known_hosts file names is refused, which is how "I already have an SSH
// connection with this Host" is checked rather than assumed.
func RegisterLogin(ctx context.Context, req Registration, in Login, commit func(Registered) error) error {
	if err := req.validate(); err != nil {
		return err
	}
	if len(in.Auth) == 0 {
		return errors.New("no SSH login was offered for this Host")
	}
	var hostKey ssh.PublicKey
	check := func(address string, remote net.Addr, key ssh.PublicKey) error {
		named, err := namedIn(in.Known, address, remote, key)
		if err != nil {
			return err
		}
		if !named {
			return fmt.Errorf("%w: no known_hosts file on this machine names %s, so there is no SSH connection with it yet; use the registration code", ErrHostKey, address)
		}
		hostKey = key
		return nil
	}
	client, err := connect(ctx, req.Address, &ssh.ClientConfig{User: req.User, Auth: in.Auth, HostKeyCallback: check, Timeout: connectTimeout})
	if err != nil {
		return fmt.Errorf("the SSH login: %w", err)
	}
	defer client.Close()
	return install(ctx, client, req, hostKey, commit)
}

// namedIn answers whether any known_hosts file holds this Host with this key. A
// file that holds it with a different key ends registration, because that is
// either the wrong machine or a key that changed, and neither is decided here.
func namedIn(files []string, address string, remote net.Addr, key ssh.PublicKey) (bool, error) {
	named := false
	for _, file := range files {
		if _, err := os.Stat(file); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return false, err
		}
		verify, err := knownhosts.New(file)
		if err != nil {
			return false, err
		}
		err = verify(address, remote, key)
		if err == nil {
			named = true
			continue
		}
		var missing *knownhosts.KeyError
		if !errors.As(err, &missing) || len(missing.Want) != 0 {
			return false, fmt.Errorf("%w: %s holds a different key for %s; remove that line if this Host's SSH key really changed", ErrHostKey, file, address)
		}
	}
	return named, nil
}
