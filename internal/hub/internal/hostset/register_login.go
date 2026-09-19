package hostset

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Login is a way in to a Host that the user already has: the account password,
// or a key OpenSSH on this machine already holds.
type Login struct {
	Auth []ssh.AuthMethod

	// Known are the known_hosts files that decide trust. A file that is not there
	// is skipped, because a machine with no SSH history has none of them.
	Known []string

	// NewHost accepts a Host no known_hosts file names, and records its key.
	// Without it an unnamed Host is refused, which is how "I already have an SSH
	// connection with this Host" is checked rather than assumed.
	NewHost bool
}

// RegisterLogin registers a Host over a login the user already has. That login
// can write the account's own authorized_keys, so the Hub installs its managed
// key itself: no registration code, and nothing asked of the Daemon.
//
// There is no recovery record here. The code flow needs one because it leaves a
// half-finished registration on the Host. This flow only appends one line, and
// appending it twice is the same as appending it once.
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
		if !named && !in.NewHost {
			return fmt.Errorf("%w: no known_hosts file on this machine names %s, so there is no SSH connection with it yet; use the password option to make one", ErrHostKey, address)
		}
		hostKey = key
		return nil
	}
	client, err := connect(ctx, req.Address, &ssh.ClientConfig{User: req.User, Auth: in.Auth, HostKeyCallback: check, Timeout: connectTimeout})
	if err != nil {
		return fmt.Errorf("the SSH login: %w", err)
	}
	defer client.Close()

	permanent, err := identity(req.Dir)
	if err != nil {
		return err
	}
	if err := installKey(client, permanent.PublicKey()); err != nil {
		return fmt.Errorf("installing this Hub's key on the Host: %w", err)
	}
	return prove(ctx, req, permanent, hostKey, net.JoinHostPort("127.0.0.1", strconv.Itoa(req.DaemonPort)), commit)
}

// namedIn answers whether any known_hosts file holds this Host with this key. A
// file that holds it with a different key ends registration, because that is
// either the wrong machine or a key that changed, and neither is decided here.
func namedIn(files []string, address string, remote net.Addr, key ssh.PublicKey) (bool, error) {
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
			return true, nil
		}
		var missing *knownhosts.KeyError
		if !errors.As(err, &missing) || len(missing.Want) != 0 {
			return false, fmt.Errorf("%w: %s holds a different key for %s; remove that line if this Host's SSH key really changed", ErrHostKey, file, address)
		}
	}
	return false, nil
}

// installKey appends the Hub's public key to the account's authorized_keys over
// SFTP. The login used here can already write that file, so this grants the Hub
// nothing the login did not have. It appends rather than rewrites, so an
// interrupted write cannot lose the keys that were already there.
func installKey(client *ssh.Client, key ssh.PublicKey) error {
	remote, err := sftp.NewClient(client)
	if err != nil {
		return fmt.Errorf("the Host does not serve SFTP, which is how the key is written: %w", err)
	}
	defer remote.Close()
	home, err := remote.Getwd()
	if err != nil {
		return err
	}
	dir := path.Join(home, ".ssh")
	if err := remote.MkdirAll(dir); err != nil {
		return err
	}
	// Windows OpenSSH answers no to chmod and reads permissions from the ACL it
	// inherits, so a refusal here is not a failure to install the key.
	_ = remote.Chmod(dir, 0o700)

	name := path.Join(dir, "authorized_keys")
	current, err := readRemote(remote, name)
	if err != nil {
		return err
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	for _, have := range strings.Split(current, "\n") {
		if strings.TrimSpace(have) == line {
			return nil
		}
	}
	add := line + "\n"
	if current != "" && !strings.HasSuffix(current, "\n") {
		add = "\n" + add
	}
	f, err := remote.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_APPEND)
	if err != nil {
		return err
	}
	_, err = f.Write([]byte(add))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	_ = remote.Chmod(name, 0o600)
	return nil
}

// authorizedKeysLimit bounds the file this reads back. authorized_keys holds a
// handful of lines, and a Host is not trusted to send a small one.
const authorizedKeysLimit = 1 << 20

func readRemote(remote *sftp.Client, name string) (string, error) {
	f, err := remote.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, authorizedKeysLimit))
	if err != nil {
		return "", err
	}
	return string(body), nil
}
