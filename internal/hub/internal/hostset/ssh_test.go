package hostset_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	if world.handshakes() != 1 {
		t.Errorf("handshakes = %d, want 1", world.handshakes())
	}
}

func TestAWrongKeyIsNamedAndDoesNotHang(t *testing.T) {
	world := newSSHWorld(t)
	daemon := echoListener(t)

	other := writeKey(t, filepath.Join(t.TempDir(), "other"))
	profile := world.profile(portOf(t, daemon.Addr().String()))
	profile.KeyPath = other
	dialer, err := hostset.NewSSHDialer([]hostset.SSHHost{profile}, time.Second)
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
	dialer, err := hostset.NewSSHDialer([]hostset.SSHHost{profile}, time.Second)
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

func TestAHostThatDoesNotAnswerIsUnreachable(t *testing.T) {
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := dead.Addr().String()
	dead.Close()

	dialer, err := hostset.NewSSHDialer([]hostset.SSHHost{{
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
	_, err := hostset.NewSSHDialer([]hostset.SSHHost{{
		ID: "desk", Address: "127.0.0.1:22", User: "victor",
		KeyPath:    filepath.Join(t.TempDir(), "absent"),
		KnownHosts: emptyKnownHosts(t), DaemonPort: 7777,
	}}, time.Second)
	if err == nil {
		t.Fatal("NewSSHDialer = nil, want an error naming the missing key")
	}
}

// sshWorld is an in-process sshd that accepts one key and serves direct-tcpip.
type sshWorld struct {
	address    string
	keyPath    string
	knownHosts string
	tries      chan struct{}
}

func newSSHWorld(t *testing.T) *sshWorld {
	t.Helper()
	dir := t.TempDir()
	world := &sshWorld{keyPath: writeKey(t, filepath.Join(dir, "id_ed25519")), tries: make(chan struct{}, 32)}

	authorized, err := ssh.ParsePrivateKey(readFile(t, world.keyPath))
	if err != nil {
		t.Fatal(err)
	}
	hostKey, err := ssh.ParsePrivateKey(readFile(t, writeKey(t, filepath.Join(dir, "host"))))
	if err != nil {
		t.Fatal(err)
	}
	world.knownHosts = filepath.Join(dir, "known_hosts")
	config := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if string(key.Marshal()) != string(authorized.PublicKey().Marshal()) {
			return nil, fmt.Errorf("no")
		}
		return nil, nil
	}}
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
	w.tries <- struct{}{}
	defer conn.Close()
	go ssh.DiscardRequests(requests)
	for newChannel := range channels {
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
		go ssh.DiscardRequests(channelRequests)
		go func() { io.Copy(target, channel); target.Close() }()
		go func() { io.Copy(channel, target); channel.Close() }()
	}
}

func (w *sshWorld) handshakes() int { return len(w.tries) }

func (w *sshWorld) profile(daemonPort int) hostset.SSHHost {
	return hostset.SSHHost{
		ID: "desk", Address: w.address, User: "victor",
		KeyPath: w.keyPath, KnownHosts: w.knownHosts, DaemonPort: daemonPort,
	}
}

func (w *sshWorld) dialer(t *testing.T, daemonPort int) *hostset.SSHDialer {
	t.Helper()
	dialer, err := hostset.NewSSHDialer([]hostset.SSHHost{w.profile(daemonPort)}, time.Second)
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
