package hostset_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestTheDialerReachesTheDaemonPortThroughTheTunnel(t *testing.T) {
	world := newSSHWorld(t)
	daemon := echoListener(t)

	dialer := world.dialer(t, portOf(t, daemon.Addr().String()))
	defer dialer.Close()

	conn, err := dialer.Dial(context.Background(), "desk")
	if err != nil {
		t.Fatalf("Dial = %v, want a channel", err)
	}
	defer conn.Close()
	if got := roundTrip(t, conn, "hello"); got != "hello" {
		t.Errorf("through the tunnel = %q, want %q", got, "hello")
	}

	// A second Dial reuses the one SSH connection rather than handshaking again.
	second, err := dialer.Dial(context.Background(), "desk")
	if err != nil {
		t.Fatalf("second Dial = %v, want a channel", err)
	}
	defer second.Close()
	if got := world.handshakes.Load(); got != 1 {
		t.Errorf("handshakes = %d, want 1", got)
	}
}

func TestAWrongKeyIsNamedAndDoesNotHang(t *testing.T) {
	world := newSSHWorld(t)
	daemon := echoListener(t)

	other := writeKey(t, filepath.Join(t.TempDir(), "other"))
	profile := world.profile(portOf(t, daemon.Addr().String()))
	profile.KeyPath = other
	dialer, err := hostset.NewSSHDialer([]hostset.SSHProfile{profile}, time.Second)
	if err != nil {
		t.Fatalf("NewSSHDialer = %v", err)
	}
	defer dialer.Close()

	done := make(chan error, 1)
	go func() {
		_, err := dialer.Dial(context.Background(), "desk")
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, hostset.ErrAuth) {
			t.Errorf("Dial = %v, want ErrAuth", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Dial hung on a wrong key")
	}
}

func TestAHostKeyThatIsNotInKnownHostsIsRefused(t *testing.T) {
	world := newSSHWorld(t)
	daemon := echoListener(t)

	profile := world.profile(portOf(t, daemon.Addr().String()))
	profile.KnownHosts = emptyKnownHosts(t)
	dialer, err := hostset.NewSSHDialer([]hostset.SSHProfile{profile}, time.Second)
	if err != nil {
		t.Fatalf("NewSSHDialer = %v", err)
	}
	defer dialer.Close()

	if _, err := dialer.Dial(context.Background(), "desk"); !errors.Is(err, hostset.ErrHostKey) {
		t.Errorf("Dial = %v, want ErrHostKey", err)
	}
}

func TestNothingBehindTheTunnelIsNoDaemon(t *testing.T) {
	world := newSSHWorld(t)
	closed := echoListener(t)
	port := portOf(t, closed.Addr().String())
	closed.Close()

	dialer := world.dialer(t, port)
	defer dialer.Close()

	if _, err := dialer.Dial(context.Background(), "desk"); !errors.Is(err, hostset.ErrNoDaemon) {
		t.Errorf("Dial = %v, want ErrNoDaemon", err)
	}
}

func TestAHostThatWillNotForwardIsNamedApartFromANoDaemon(t *testing.T) {
	world := newSSHWorld(t)
	world.forbidden = true
	daemon := echoListener(t)

	dialer := world.dialer(t, portOf(t, daemon.Addr().String()))
	defer dialer.Close()

	_, err := dialer.Dial(context.Background(), "desk")
	if !errors.Is(err, hostset.ErrForwarding) {
		t.Fatalf("Dial = %v, want ErrForwarding", err)
	}
	if errors.Is(err, hostset.ErrNoDaemon) {
		t.Error("a Host that will not forward reads as a Daemon that is not running")
	}
}

// The key is the whole security boundary, so the Hub takes one kind of key and
// says so at start rather than falling back to whatever the file holds.
func TestAKeyThatIsNotEd25519IsRefusedAtStart(t *testing.T) {
	dir := t.TempDir()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "id_rsa")
	writeFile(t, path, string(pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey),
	})))

	_, err = hostset.NewSSHDialer([]hostset.SSHProfile{{
		ID: "desk", Address: "127.0.0.1:22", User: "victor",
		KeyPath: path, KnownHosts: emptyKnownHosts(t), DaemonPort: 7777,
	}}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "ed25519") {
		t.Fatalf("NewSSHDialer = %v, want an error naming ed25519", err)
	}
}

func TestAHostThatDoesNotAnswerIsUnreachable(t *testing.T) {
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := dead.Addr().String()
	dead.Close()

	dialer, err := hostset.NewSSHDialer([]hostset.SSHProfile{{
		ID: "desk", Address: address, User: "victor",
		KeyPath:    writeKey(t, filepath.Join(t.TempDir(), "id")),
		KnownHosts: emptyKnownHosts(t), DaemonPort: 7777,
	}}, time.Second)
	if err != nil {
		t.Fatalf("NewSSHDialer = %v", err)
	}
	defer dialer.Close()

	if _, err := dialer.Dial(context.Background(), "desk"); !errors.Is(err, hostset.ErrUnreachable) {
		t.Errorf("Dial = %v, want ErrUnreachable", err)
	}
}

func TestAKeyThatIsNotThereFailsAtStart(t *testing.T) {
	_, err := hostset.NewSSHDialer([]hostset.SSHProfile{{
		ID: "desk", Address: "127.0.0.1:22", User: "victor",
		KeyPath:    filepath.Join(t.TempDir(), "absent"),
		KnownHosts: emptyKnownHosts(t), DaemonPort: 7777,
	}}, time.Second)
	if err == nil {
		t.Fatal("NewSSHDialer = nil, want an error naming the missing key")
	}
}

// sshWorld is an in-process sshd. It accepts the keys in one authorized_keys
// file, accepts one password, serves direct-tcpip, and runs the two scripts Host
// Registration sends.
type sshWorld struct {
	address    string
	keyPath    string
	knownHosts string
	hostKey    ssh.PublicKey
	handshakes atomic.Int64

	// password is the account's password. It is empty for a world no password
	// reaches, which is every test of the dialer.
	password string

	// forbidden is sshd with AllowTcpForwarding off, which refuses the channel
	// for its own reasons rather than because nothing is listening.
	forbidden bool

	// silent is a Host that runs the script and answers nothing the caller can
	// read, which is what an interrupt inside that window looks like from here.
	silent bool

	mu sync.Mutex

	// authorized is the account's own authorized_keys file, one line per key.
	authorized []string

	// steps is what this Host was asked to do, in order. It is the only way to see
	// that the password came after the fingerprint and the key-only login after
	// the authorization.
	steps []string
}

func newSSHWorld(t *testing.T) *sshWorld {
	t.Helper()
	dir := t.TempDir()
	world := &sshWorld{keyPath: writeKey(t, filepath.Join(dir, "id_ed25519"))}

	authorized, err := ssh.ParsePrivateKey(readFile(t, world.keyPath))
	if err != nil {
		t.Fatal(err)
	}
	world.authorized = []string{strings.TrimSpace(string(ssh.MarshalAuthorizedKey(authorized.PublicKey())))}
	hostKey, err := ssh.ParsePrivateKey(readFile(t, writeKey(t, filepath.Join(dir, "host"))))
	if err != nil {
		t.Fatal(err)
	}
	world.hostKey = hostKey.PublicKey()
	world.knownHosts = filepath.Join(dir, "known_hosts")
	config := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if !world.accepts(key) {
				return nil, fmt.Errorf("no")
			}
			world.did("key login")
			return nil, nil
		},
		PasswordCallback: func(_ ssh.ConnMetadata, given []byte) (*ssh.Permissions, error) {
			if world.password == "" || string(given) != world.password {
				return nil, fmt.Errorf("no")
			}
			world.did("password login")
			return nil, nil
		},
	}
	config.AddHostKey(hostKey)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	world.address = listener.Addr().String()
	writeFile(t, world.knownHosts, knownhosts.Line([]string{world.address}, hostKey.PublicKey()))
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			tcp, err := listener.Accept()
			if err != nil {
				return
			}
			go world.serve(tcp, config)
		}
	}()
	return world
}

func (w *sshWorld) serve(tcp net.Conn, config *ssh.ServerConfig) {
	conn, channels, requests, err := ssh.NewServerConn(tcp, config)
	if err != nil {
		tcp.Close()
		return
	}
	w.handshakes.Add(1)
	defer conn.Close()
	go ssh.DiscardRequests(requests)
	for newChannel := range channels {
		if newChannel.ChannelType() == "session" {
			go w.session(newChannel)
			continue
		}
		if newChannel.ChannelType() != "direct-tcpip" {
			newChannel.Reject(ssh.UnknownChannelType, "not direct-tcpip")
			continue
		}
		var request struct {
			DestAddr string
			DestPort uint32
			SrcAddr  string
			SrcPort  uint32
		}
		if err := ssh.Unmarshal(newChannel.ExtraData(), &request); err != nil {
			newChannel.Reject(ssh.ConnectionFailed, "bad payload")
			continue
		}
		if w.forbidden {
			newChannel.Reject(ssh.Prohibited, "administratively prohibited")
			continue
		}
		target, err := net.Dial("tcp", net.JoinHostPort(request.DestAddr, fmt.Sprint(request.DestPort)))
		if err != nil {
			newChannel.Reject(ssh.ConnectionFailed, "connection refused")
			continue
		}
		channel, channelRequests, err := newChannel.Accept()
		if err != nil {
			target.Close()
			continue
		}
		w.did("forward")
		go ssh.DiscardRequests(channelRequests)
		go func() { io.Copy(target, channel); target.Close() }()
		go func() { io.Copy(channel, target); channel.Close() }()
	}
}

// session runs one exec request. It reads the base64 UTF-16 PowerShell the
// module sends, and keeps the account's authorized_keys the way the script would.
func (w *sshWorld) session(newChannel ssh.NewChannel) {
	channel, requests, err := newChannel.Accept()
	if err != nil {
		return
	}
	defer channel.Close()
	for request := range requests {
		if request.Type != "exec" {
			request.Reply(false, nil)
			continue
		}
		var payload struct{ Command string }
		ssh.Unmarshal(request.Payload, &payload)
		request.Reply(true, nil)
		w.run(payload.Command)
		if w.silent {
			return
		}
		channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
		return
	}
}

// run does what the script says. It reads the key out of the script's own $key
// line and the operation out of the line that names it.
func (w *sshWorld) run(command string) {
	script := decodeCommand(command)
	_, rest, ok := strings.Cut(script, "$key = '")
	if !ok {
		return
	}
	key, _, _ := strings.Cut(rest, "'")

	w.mu.Lock()
	defer w.mu.Unlock()
	switch {
	case strings.HasPrefix(script, "# dispatch: authorize"):
		w.record("authorize")
		for _, have := range w.authorized {
			if have == key {
				return
			}
		}
		w.authorized = append(w.authorized, key)
	case strings.HasPrefix(script, "# dispatch: deauthorize"):
		w.record("deauthorize")
		kept := w.authorized[:0]
		for _, have := range w.authorized {
			if have != key {
				kept = append(kept, have)
			}
		}
		w.authorized = kept
	}
}

// decodeCommand turns -EncodedCommand back into the script.
func decodeCommand(command string) string {
	_, encoded, _ := strings.Cut(command, "-EncodedCommand ")
	body, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return ""
	}
	units := make([]uint16, len(body)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(body[2*i:])
	}
	return string(utf16.Decode(units))
}

func (w *sshWorld) accepts(offered ssh.PublicKey) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, line := range w.authorized {
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err == nil && string(key.Marshal()) == string(offered.Marshal()) {
			return true
		}
	}
	return false
}

func (w *sshWorld) keys() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.authorized...)
}

func (w *sshWorld) did(step string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.record(step)
}

// record keeps one step. A repeat of the step before it is dropped, because a
// key login asks once to see whether the key is wanted and once with a
// signature, and that is one login.
func (w *sshWorld) record(step string) {
	if len(w.steps) > 0 && w.steps[len(w.steps)-1] == step {
		return
	}
	w.steps = append(w.steps, step)
}

func (w *sshWorld) done() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.steps...)
}

func (w *sshWorld) profile(daemonPort int) hostset.SSHProfile {
	return hostset.SSHProfile{
		ID: "desk", Address: w.address, User: "victor",
		KeyPath: w.keyPath, KnownHosts: w.knownHosts, DaemonPort: daemonPort,
	}
}

func (w *sshWorld) dialer(t *testing.T, daemonPort int) *hostset.SSHDialer {
	t.Helper()
	dialer, err := hostset.NewSSHDialer([]hostset.SSHProfile{w.profile(daemonPort)}, time.Second)
	if err != nil {
		t.Fatalf("NewSSHDialer = %v", err)
	}
	return dialer
}

// echoListener stands in for the Daemon's loopback port.
func echoListener(t *testing.T) net.Listener {
	t.Helper()
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
			go func() { io.Copy(conn, conn); conn.Close() }()
		}
	}()
	return listener
}

func roundTrip(t *testing.T, conn net.Conn, text string) string {
	t.Helper()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(conn, text); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(text))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	return string(buf)
}

// emptyKnownHosts is the file a Host the Hub has never met is checked against.
func emptyKnownHosts(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "known_hosts")
	writeFile(t, path, "")
	return path
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeKey(t *testing.T, path string) string {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(private, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func portOf(t *testing.T, address string) int {
	t.Helper()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	fmt.Sscan(port, &n)
	return n
}
