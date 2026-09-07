package hostset

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
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
	"unicode/utf16"

	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Host Registration is ADR 0013's one password login. It shows the Host key
// before it sends anything secret, installs the Hub's own key for one account,
// and proves key-only SSH and the normal Handshake before the caller writes a
// line of configuration.
//
// Every check runs before the commit, so a Host that half worked leaves nothing
// behind: the Hub's key is taken off the Host again and the file is untouched.

// The two failures registration adds to the four the dialer already names.
var (
	// ErrDeclined is the user saying that is not the machine, which ends the
	// attempt with the password unread.
	ErrDeclined = errors.New("the Host key was not confirmed")

	// ErrIncompatible is the Handshake failing, which is a Host to update rather
	// than a Host to retry.
	ErrIncompatible = errors.New("this Host's Daemon serves another protocol version")
)

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

// keyComment marks the Hub's line in an account's authorized_keys, so a user
// reading that file can tell which key is this program's.
const keyComment = "dispatch-hub"

// refusalLimit bounds the body a Daemon answers the Handshake with. It is a
// handful of version numbers.
const refusalLimit = 4096

// Registration is one Host as the user named it.
type Registration struct {
	ID HostID

	// Address is the sshd endpoint, host:port. The caller supplies port 22 when
	// the user typed none, because this package dials the string as it is written.
	Address string

	// User is the local Windows account. It does not have to be the account the
	// Daemon runs as: the channel reaches the Daemon over the Host's loopback.
	User string

	DaemonPort int

	// Dir holds the Hub's managed identity and the Hosts it trusts. ADR 0013 puts
	// it under %LOCALAPPDATA%, which the command layer resolves.
	Dir string
}

// Interaction is the user. Registration asks for the two things it may not
// decide on its own: whether this is the right machine, and the password.
type Interaction interface {
	// ConfirmHostKey runs before anything secret is sent, and a false answer ends
	// the attempt. This is trust on first use: a later connection refuses a
	// changed Host key, and the first confirmation is the user's alone.
	ConfirmHostKey(fingerprint string) (bool, error)

	// Password is read for one login. It is never logged and never stored, and it
	// is never read at all when the fingerprint is declined.
	Password() (string, error)
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

// RegisterHost runs the whole of ADR 0013's flow and answers when the Host is in
// the caller's configuration. An error names the stage that failed and, when the
// Host was already changed, whether the change was taken back.
func RegisterHost(ctx context.Context, req Registration, ask Interaction, commit func(Registered) error) error {
	if err := req.validate(); err != nil {
		return err
	}
	client, hostKey, err := login(ctx, req, ask)
	if err != nil {
		return err
	}
	defer client.Close()

	daemon := net.JoinHostPort("127.0.0.1", strconv.Itoa(req.DaemonPort))
	if err := checkDaemon(ctx, client, daemon); err != nil {
		return fmt.Errorf("the password login: %w", err)
	}

	signer, err := identity(req.Dir)
	if err != nil {
		return fmt.Errorf("the Hub's managed identity: %w", err)
	}
	// The Host is changed from here, so anything that fails from here takes the
	// authorization off again. The managed key stays: it is the Hub's identity and
	// the next attempt uses the same one.
	line := authorizedLine(signer)
	if err := remote(ctx, client, fmt.Sprintf(authorizeScript, line)); err != nil {
		// A failed authorization rolls back like the later stages, because the script
		// may have run and its answer been lost. An interrupt between the two is the
		// easy way for that to happen.
		return rollback(ctx, client, req.User, line, fmt.Errorf("authorizing the Hub's key for %s: %w", req.User, err))
	}
	if err := prove(ctx, req, signer, hostKey, daemon, commit); err != nil {
		return rollback(ctx, client, req.User, line, err)
	}
	return nil
}

// rollback takes the Hub's key off the Host again and names what happened. It
// does not take the cancellation that caused it: an interrupt is one of the ways
// registration fails, and a rollback that ended with it would leave the Hub's
// key on a Host nobody asked to trust it.
func rollback(ctx context.Context, client *ssh.Client, user, line string, why error) error {
	if undo := remote(context.WithoutCancel(ctx), client, fmt.Sprintf(deauthorizeScript, line)); undo != nil {
		return fmt.Errorf("%w (the Hub's key may still be authorized for %s: %w)", why, user, undo)
	}
	return fmt.Errorf("%w (the Hub's key was taken off this Host again)", why)
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

// login is the one password connection. The fingerprint is confirmed inside the
// SSH handshake and the password is read inside authentication, which is what
// puts them in that order: a declined Host never sees a password at all.
func login(ctx context.Context, req Registration, ask Interaction) (*ssh.Client, ssh.PublicKey, error) {
	var hostKey ssh.PublicKey
	var asked error
	config := &ssh.ClientConfig{
		User:    req.User,
		Auth:    []ssh.AuthMethod{ssh.PasswordCallback(ask.Password)},
		Timeout: connectTimeout,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			ok, err := ask.ConfirmHostKey(ssh.FingerprintSHA256(key))
			if err != nil {
				asked = err
				return err
			}
			if !ok {
				asked = ErrDeclined
				return ErrDeclined
			}
			hostKey = key
			return nil
		},
	}
	client, err := connect(ctx, req.Address, config)
	if err != nil {
		// What the user answered is the reason, and SSH's own words for a refused
		// handshake would hide it.
		if asked != nil {
			return nil, nil, asked
		}
		return nil, nil, fmt.Errorf("the password login: %w", err)
	}
	return client, hostKey, nil
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
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, err
	}
	return ssh.NewSignerFromKey(private)
}

func authorizedLine(signer ssh.Signer) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " " + keyComment
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
	temp := path + ".new"
	if err := os.WriteFile(temp, []byte(body.String()), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		os.Remove(temp)
		return err
	}
	return nil
}

// The two scripts registration runs on the Host. The account's own
// authorized_keys is the standard-account location; an administrator account
// uses a shared file with an ACL of its own, and that is not this one.
//
// Each starts with a line naming what it does, because a user reading the Host's
// sshd log sees the command and should not have to decode it to know.
const authorizeScript = `# dispatch: authorize
$ErrorActionPreference = 'Stop'
$key = '%s'
$dir = Join-Path $env:USERPROFILE '.ssh'
if (-not (Test-Path -LiteralPath $dir)) { New-Item -ItemType Directory -Path $dir | Out-Null }
$file = Join-Path $dir 'authorized_keys'
$lines = @()
if (Test-Path -LiteralPath $file) { $lines = @(Get-Content -LiteralPath $file) }
if ($lines -notcontains $key) { Set-Content -LiteralPath $file -Value ($lines + $key) -Encoding ascii }
`

const deauthorizeScript = `# dispatch: deauthorize
$ErrorActionPreference = 'Stop'
$key = '%s'
$file = Join-Path (Join-Path $env:USERPROFILE '.ssh') 'authorized_keys'
if (Test-Path -LiteralPath $file) {
  $lines = @(Get-Content -LiteralPath $file | Where-Object { $_ -ne $key })
  Set-Content -LiteralPath $file -Value $lines -Encoding ascii
}
`

// remote runs one script on the Host and waits for it.
func remote(ctx context.Context, client *ssh.Client, script string) error {
	ctx, cancel := context.WithTimeout(ctx, remoteTimeout)
	defer cancel()
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	stop := context.AfterFunc(ctx, func() { session.Close() })
	defer stop()

	out, err := session.CombinedOutput(encodedCommand(script))
	if err != nil {
		if said := strings.TrimSpace(string(out)); said != "" {
			return fmt.Errorf("%w: %s", err, said)
		}
		return err
	}
	return nil
}

// encodedCommand is the command line the Host's shell receives. PowerShell reads
// -EncodedCommand as UTF-16 base64, which leaves no quoting rule between here and
// the script, and cmd.exe and PowerShell start it the same way, so the Host's
// default shell does not have to be known.
func encodedCommand(script string) string {
	units := utf16.Encode([]rune(script))
	body := make([]byte, 2*len(units))
	for i, unit := range units {
		binary.LittleEndian.PutUint16(body[2*i:], unit)
	}
	return "powershell -NoProfile -NonInteractive -EncodedCommand " + base64.StdEncoding.EncodeToString(body)
}

func (r Registration) validate() error {
	if r.ID == "" {
		return errors.New("this Host has no id")
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
