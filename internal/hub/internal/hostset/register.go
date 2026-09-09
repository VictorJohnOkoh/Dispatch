package hostset

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// These are the shared SSH, managed identity and trust-file operations used by
// code registration. Configuration stays at the command boundary.

// ErrIncompatible requires a Daemon update rather than a connection retry.
var ErrIncompatible = errors.New("this Host's Daemon serves another protocol version")

// The two limits ADR 0013 sets. The first covers a connection and its SSH
// handshake, the second covers work on the Host: one remote command, or the
// Daemon answering through the tunnel.
const (
	connectTimeout = 10 * time.Second
	remoteTimeout  = 30 * time.Second
)

// The two files the Hub keeps under its own directory. One Hub has one identity,
// and it is the same key on every Host it registers.
const (
	keyFile   = "id_ed25519"
	trustFile = "known_hosts"
)

// refusalLimit bounds the body a Daemon answers the Handshake with. It is a
// handful of version numbers.
const refusalLimit = 4096

// Registration is one Host as the user named it.
type Registration struct {
	ID HostID

	// Address is the sshd endpoint, host:port. The caller supplies port 22 when
	// the user typed none, because this package dials the string as it is written.
	Address string

	// User is the standard local Windows account running the Daemon and SSH.
	User string

	DaemonPort int

	// Dir holds the Hub's managed identity and the Hosts it trusts. ADR 0013 puts
	// it under %LOCALAPPDATA%, which the command layer resolves.
	Dir        string
	TrustFiles []string
}

// Registered is the Host as hub.json will hold it. This package reads and writes
// no configuration file, so the caller commits this.
type Registered struct {
	ID         HostID
	Address    string
	User       string
	KeyPath    string
	KnownHosts string
	DaemonPort int
}

// prove is everything after the Host has been changed: the key-only login, the
// Handshake on it, the trusted Host key and the commit.
func prove(ctx context.Context, req Registration, signer ssh.Signer, hostKey ssh.PublicKey, daemon string, commit func(Registered) error) error {
	client, err := connect(ctx, req.Address, &ssh.ClientConfig{
		User:            req.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.FixedHostKey(hostKey),
		Timeout:         connectTimeout,
	})
	if err != nil {
		return fmt.Errorf("the key-only login: %w", err)
	}
	defer client.Close()
	if err := checkDaemon(ctx, client, daemon); err != nil {
		return fmt.Errorf("the key-only login: %w", err)
	}

	known := filepath.Join(req.Dir, trustFile)
	added, err := trust(known, req.Address, hostKey)
	if err != nil {
		return fmt.Errorf("trusting this Host's key: %w", err)
	}
	if err := commit(Registered{
		ID: req.ID, Address: req.Address, User: req.User,
		KeyPath: filepath.Join(req.Dir, keyFile), KnownHosts: known, DaemonPort: req.DaemonPort,
	}); err != nil {
		if added {
			if undo := untrust(known, req.Address, hostKey); undo != nil {
				return fmt.Errorf("the configuration: %w (this Host stays in %s: %w)", err, known, undo)
			}
		}
		return fmt.Errorf("the configuration: %w", err)
	}
	return nil
}

// checkDaemon is the normal Handshake, run through this connection: the same
// request the Hub's Event stream opens with, and the same first Frame. Anything
// can answer on a port, and only a Daemon opens with a Hello naming the version.
func checkDaemon(ctx context.Context, client *ssh.Client, daemon string) error {
	ctx, cancel := context.WithTimeout(ctx, remoteTimeout)
	defer cancel()

	conn, err := client.DialContext(ctx, "tcp", daemon)
	if err != nil {
		var rejected *ssh.OpenChannelError
		if !errors.As(err, &rejected) {
			return fmt.Errorf("%w on %s: %w", ErrNoDaemon, daemon, err)
		}
		if rejected.Reason == ssh.ConnectionFailed {
			return fmt.Errorf("%w on %s: %w", ErrNoDaemon, daemon, err)
		}
		return fmt.Errorf("%w: %w", ErrForwarding, err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://daemon/v1/events", nil)
	request.Header.Set(protocol.VersionHeader, strconv.Itoa(protocol.Version))
	if err := request.Write(conn); err != nil {
		return fmt.Errorf("%w on %s: %w", ErrNoDaemon, daemon, err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), request)
	if err != nil {
		return fmt.Errorf("%w on %s: %w", ErrNoDaemon, daemon, err)
	}
	// The body is an Event stream and it does not end. Closing the connection ends
	// it; closing the body would read it to the end first.

	if response.StatusCode == protocol.StatusUpgradeRequired {
		var refusal protocol.Refusal
		_ = json.NewDecoder(io.LimitReader(response.Body, refusalLimit)).Decode(&refusal)
		return fmt.Errorf("%w, and it serves %v", ErrIncompatible, refusal.Speaks)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w on %s: it answered %s", ErrNoDaemon, daemon, response.Status)
	}
	if !opensWithHello(response.Body) {
		return fmt.Errorf("%w on %s: the stream did not open with a Hello", ErrNoDaemon, daemon)
	}
	return nil
}

// opensWithHello reads the stream's first Frame and reports whether it is a
// Hello this build can work with.
func opensWithHello(body io.Reader) bool {
	reader := bufio.NewReader(io.LimitReader(body, refusalLimit))
	var name string
	var data []byte
	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		case line == "" && name != "":
			var hello protocol.Hello
			return name == string(protocol.FrameHello) &&
				json.Unmarshal(data, &hello) == nil && hello.Protocol == protocol.Version
		}
		if err != nil {
			return false
		}
	}
}

// identity loads the Hub's managed key and makes it the first time. A key that
// is already there is kept, because every Host registered before this one
// trusts it.
func identity(dir string) (ssh.Signer, error) {
	path := filepath.Join(dir, keyFile)
	body, err := os.ReadFile(path)
	switch {
	case err == nil:
		signer, err := ssh.ParsePrivateKey(body)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if got := signer.PublicKey().Type(); got != ssh.KeyAlgoED25519 {
			return nil, fmt.Errorf("%s is %s, and this Hub uses ed25519", path, got)
		}
		return signer, nil
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}

	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	// The key has no passphrase, because the Hub starts without a user at the
	// keyboard and a passphrase it holds beside the key protects nothing.
	block, err := ssh.MarshalPrivateKey(private, "")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := atomicSSHFile(path, pem.EncodeToMemory(block)); err != nil {
		return nil, err
	}
	return ssh.NewSignerFromKey(private)
}

// trust adds this Host's key to the Hub's known_hosts and answers whether this
// attempt was the one that added it, so a rollback removes only its own line.
func trust(path, address string, key ssh.PublicKey) (bool, error) {
	line := knownhosts.Line([]string{address}, key)
	lines, err := readLines(path)
	if err != nil {
		return false, err
	}
	for _, have := range lines {
		if have == line {
			return false, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	return true, writeLines(path, append(lines, line))
}

func untrust(path, address string, key ssh.PublicKey) error {
	line := knownhosts.Line([]string{address}, key)
	lines, err := readLines(path)
	if err != nil {
		return err
	}
	kept := make([]string, 0, len(lines))
	for _, have := range lines {
		if have != line {
			kept = append(kept, have)
		}
	}
	return writeLines(path, kept)
}

// readLines answers no lines for a file that is not there, because the first
// Host registered on a new Hub has no known_hosts yet.
func readLines(path string) ([]string, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, line := range strings.Split(string(body), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines, nil
}

// writeLines replaces known_hosts whole. That file holds every Host this Hub
// has ever registered, and a write that truncated it first would lose all of
// them to one interrupted write.
func writeLines(path string, lines []string) error {
	var body strings.Builder
	for _, line := range lines {
		body.WriteString(line)
		body.WriteString("\n")
	}
	return atomicSSHFile(path, []byte(body.String()))
}

func atomicSSHFile(path string, body []byte) error {
	temp := path + ".new"
	f, err := os.OpenFile(temp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(body)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(temp)
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		os.Remove(temp)
		return err
	}
	return nil
}

func (r Registration) validate() error {
	if !protocol.ValidHostID(string(r.ID)) {
		return errors.New("invalid Host id")
	}
	if _, port, err := net.SplitHostPort(r.Address); err != nil || port == "" {
		return fmt.Errorf("address %q needs a port, as in %q", r.Address, r.Address+":22")
	}
	if r.User == "" {
		return fmt.Errorf("Host %s names no account", r.ID)
	}
	if r.DaemonPort < 1 || r.DaemonPort > 65535 {
		return fmt.Errorf("daemonPort %d is not a port", r.DaemonPort)
	}
	if r.Dir == "" {
		return errors.New("no directory holds the Hub's identity")
	}
	return nil
}
