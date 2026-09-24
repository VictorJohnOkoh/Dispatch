package hostset

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// adminKeys is where Windows OpenSSH reads keys for every member of
// Administrators. It ignores the account's own authorized_keys for them.
var adminKeys = "/C:/ProgramData/ssh/administrators_authorized_keys"

// administratorsSID is the built-in Administrators group. whoami prints the SID
// in every Windows language, and the group name is translated.
const administratorsSID = "S-1-5-32-544"

// adminACL is the ACL OpenSSH requires on adminKeys: SYSTEM and Administrators
// only, named by SID for the same reason.
const adminACL = `icacls "C:\ProgramData\ssh\administrators_authorized_keys" /inheritance:r /grant *S-1-5-18:F /grant *S-1-5-32-544:F`

// authorizedKeysLimit bounds the file this reads back. authorized_keys holds a
// handful of lines, and a Host is not trusted to send a small one.
const authorizedKeysLimit = 1 << 20

// hubLine is the Hub key as the Host holds it. The key opens the tunnel to the
// Daemon and nothing else: restrict turns off every other forwarding, the PTY
// and the agent, and the forced command answers any shell or command request.
func hubLine(key ssh.PublicKey, daemonPort int) string {
	return fmt.Sprintf(`restrict,port-forwarding,permitopen="127.0.0.1:%d",command="exit 1" %s dispatch-hub`,
		daemonPort, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))))
}

// authorize writes the Hub key into the file OpenSSH reads for this account, over
// a login that can already write it. It answers an undo that puts the file back
// the way this attempt found it, and a line that was already there is not undone.
func authorize(ctx context.Context, client *ssh.Client, remote *sftp.Client, key ssh.PublicKey, daemonPort int) (func() error, error) {
	name, admin, err := keysFile(ctx, client, remote)
	if err != nil {
		return nil, err
	}
	before, err := readRemote(remote, name)
	if err != nil {
		return nil, err
	}
	line := hubLine(key, daemonPort)
	after, changed := withHubLine(before, key, line)
	if !changed {
		return func() error { return nil }, nil
	}
	if err := writeRemote(remote, name, after); err != nil {
		return nil, err
	}
	undo := func() error {
		now, err := readRemote(remote, name)
		if err != nil {
			return err
		}
		if now != after {
			return fmt.Errorf("%s changed during registration, so the Hub key line stays; remove the line ending in dispatch-hub by hand", name)
		}
		return writeRemote(remote, name, before)
	}
	if admin {
		if err := run(ctx, client, adminACL); err != nil {
			return nil, errors.Join(fmt.Errorf("setting the ACL on %s: %w", name, err), undo())
		}
	}
	return undo, nil
}

// withHubLine adds the Hub line and removes any other line for the same key, so
// a Host registered before the key was restricted gets the restricted line.
func withHubLine(body string, key ssh.PublicKey, line string) (string, bool) {
	var kept []string
	found, others := false, 0
	if trimmed := strings.TrimRight(body, "\r\n"); trimmed != "" {
		for _, have := range strings.Split(trimmed, "\n") {
			switch text := strings.TrimSpace(have); {
			case text == line:
				found = true
			case text != "" && sameKey(text, key):
				others++
			default:
				kept = append(kept, have)
			}
		}
	}
	if found && others == 0 {
		return body, false
	}
	return strings.Join(append(kept, line), "\n") + "\n", true
}

func sameKey(line string, key ssh.PublicKey) bool {
	have, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	return err == nil && string(have.Marshal()) == string(key.Marshal())
}

// keysFile asks the Host which file OpenSSH reads for this login. Only Windows
// answers whoami /groups, and only an administrator lists the group's SID.
func keysFile(ctx context.Context, client *ssh.Client, remote *sftp.Client) (string, bool, error) {
	if groups, err := output(ctx, client, "whoami /groups"); err == nil && strings.Contains(groups, administratorsSID) {
		return adminKeys, true, nil
	}
	home, err := remote.Getwd()
	if err != nil {
		return "", false, err
	}
	dir := path.Join(home, ".ssh")
	if err := remote.MkdirAll(dir); err != nil {
		return "", false, err
	}
	// Windows OpenSSH answers no to chmod and reads permissions from the ACL the
	// directory inherits, so a refusal here is not a failure.
	_ = remote.Chmod(dir, 0o700)
	return path.Join(dir, "authorized_keys"), false, nil
}

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

// writeRemote writes the file in place, because an SFTP rename cannot replace a
// file on Windows. The file keeps its ACL that way, which OpenSSH checks.
func writeRemote(remote *sftp.Client, name, body string) error {
	f, err := remote.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	_, err = f.Write([]byte(body))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	_ = remote.Chmod(name, 0o600)
	return nil
}

func run(ctx context.Context, client *ssh.Client, command string) error {
	_, err := output(ctx, client, command)
	return err
}

func output(ctx context.Context, client *ssh.Client, command string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, remoteTimeout)
	defer cancel()
	s, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer s.Close()
	stop := context.AfterFunc(ctx, func() { s.Close() })
	defer stop()
	b, err := s.Output(command)
	return string(b), err
}
