package hostset

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// The four ways reaching a Daemon fails. ADR 0004 turns the first two into a
// Host State cause, so they stay apart rather than collapsing into one refusal.
var (
	ErrUnreachable = errors.New("the Host does not answer")
	ErrNoDaemon    = errors.New("the Host answers but no Daemon is listening")
	ErrAuth        = errors.New("the Host refused this key")
	ErrHostKey     = errors.New("the Host's key is not the one in known_hosts")

	// ErrForwarding is sshd saying no to the channel itself, which is what
	// AllowTcpForwarding off looks like. It is apart from ErrNoDaemon because the
	// two send the reader to different machines: this one is sshd_config.
	ErrForwarding = errors.New("the Host will not forward a channel")
)

// SSHProfile is what the Hub needs to reach one Host. A Host is the machine,
// and this is the way in, so the two are named apart.
type SSHProfile struct {
	ID HostID

	// Address is the sshd endpoint, host:port.
	Address string

	User string

	// An ed25519 private key with no passphrase. Key auth is the whole
	// security boundary, so there is no password fallback and no other key type.
	KeyPath string

	// The file the Host's key is checked against
	KnownHosts string

	// DaemonPort is the loopback port the direct-tcpip channel opens onto.
	DaemonPort int
}

// SSHDialer reaches each Host over SSH and opens a direct-tcpip channel to its
// Daemon, which is what `ssh -L` does without publishing a local port.
//
// One SSH connection per Host is kept and reused, because the Client's leg makes
// one request per command on top of the long-lived Event stream, and a handshake
// per request would be paid on every one of them. A second caller waits for a
// connect that is already running and then reuses it, rather than opening a
// second connection that one of the two would throw away.
type SSHDialer struct {
	targets map[HostID]*sshTarget
}

type sshTarget struct {
	address string
	daemon  string
	config  *ssh.ClientConfig

	mu     sync.Mutex
	client *ssh.Client
}

// NewSSHDialer reads every key and known_hosts file now rather than at the first
// Dial, so a mistyped path is a startup error and not a Host that is Down for a
// reason nobody can see.
func NewSSHDialer(hosts []SSHProfile, timeout time.Duration) (*SSHDialer, error) {
	dialer := &SSHDialer{targets: make(map[HostID]*sshTarget, len(hosts))}
	for _, host := range hosts {
		pem, err := os.ReadFile(host.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("Host %s: %w", host.ID, err)
		}
		key, err := ssh.ParsePrivateKey(pem)
		if err != nil {
			return nil, fmt.Errorf("Host %s: %s: %w", host.ID, host.KeyPath, err)
		}
		if got := key.PublicKey().Type(); got != ssh.KeyAlgoED25519 {
			return nil, fmt.Errorf("Host %s: %s is %s, and this Hub uses ed25519", host.ID, host.KeyPath, got)
		}
		check, err := knownhosts.New(host.KnownHosts)
		if err != nil {
			return nil, fmt.Errorf("Host %s: %w", host.ID, err)
		}
		dialer.targets[host.ID] = &sshTarget{
			address: host.Address,
			daemon:  net.JoinHostPort("127.0.0.1", fmt.Sprint(host.DaemonPort)),
			config: &ssh.ClientConfig{
				User:            host.User,
				Auth:            []ssh.AuthMethod{ssh.PublicKeys(key)},
				HostKeyCallback: check,
				Timeout:         timeout,
			},
		}
	}
	return dialer, nil
}

func (d *SSHDialer) Dial(ctx context.Context, id HostID) (net.Conn, error) {
	target, ok := d.targets[id]
	if !ok {
		return nil, fmt.Errorf("no such Host %q", id)
	}
	return target.dial(ctx)
}

// Close hangs up on every Host. The Hub owns the dialer for the life of the
// process, so this runs at shutdown and nowhere else.
func (d *SSHDialer) Close() error {
	for _, target := range d.targets {
		target.mu.Lock()
		if target.client != nil {
			target.client.Close()
			target.client = nil
		}
		target.mu.Unlock()
	}
	return nil
}

func (t *sshTarget) dial(ctx context.Context) (net.Conn, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.client != nil {
		conn, err := t.client.DialContext(ctx, "tcp", t.daemon)
		if err == nil {
			return conn, nil
		}
		// sshd rejecting the channel means the connection is alive and the far end
		// is not. Any other failure is the connection itself, so it is replaced.
		var rejected *ssh.OpenChannelError
		if errors.As(err, &rejected) {
			return nil, t.channelCause(err, rejected)
		}
		t.client.Close()
		t.client = nil
	}

	client, err := t.connect(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := client.DialContext(ctx, "tcp", t.daemon)
	if err != nil {
		var rejected *ssh.OpenChannelError
		if !errors.As(err, &rejected) {
			client.Close()
			return nil, fmt.Errorf("%w at %s: %w", ErrUnreachable, t.address, err)
		}
		t.client = client
		return nil, t.channelCause(err, rejected)
	}
	t.client = client
	return conn, nil
}

// channelCause splits a refused channel in two. sshd reports a closed port as
// ConnectionFailed, and everything else it refuses is its own policy, which is
// AllowTcpForwarding in practice.
func (t *sshTarget) channelCause(err error, rejected *ssh.OpenChannelError) error {
	if rejected.Reason == ssh.ConnectionFailed {
		return fmt.Errorf("%w on %s: %w", ErrNoDaemon, t.daemon, err)
	}
	return fmt.Errorf("%w at %s: %w", ErrForwarding, t.address, err)
}

// connect makes the SSH connection and names why it failed. A wrong key ends
// here with ErrAuth rather than a retry, because no amount of waiting fixes it.
func (t *sshTarget) connect(ctx context.Context) (*ssh.Client, error) {
	tcp, err := (&net.Dialer{Timeout: t.config.Timeout}).DialContext(ctx, "tcp", t.address)
	if err != nil {
		return nil, fmt.Errorf("%w at %s: %w", ErrUnreachable, t.address, err)
	}
	tcp.SetDeadline(time.Now().Add(t.config.Timeout))
	conn, channels, requests, err := ssh.NewClientConn(tcp, t.address, t.config)
	if err != nil {
		tcp.Close()
		return nil, fmt.Errorf("%w at %s: %w", handshakeCause(err), t.address, err)
	}
	tcp.SetDeadline(time.Time{})
	return ssh.NewClient(conn, channels, requests), nil
}

// handshakeCause sorts one failed handshake into the three causes the Hub tells
// apart. x/crypto/ssh types the host key failure and describes the auth failure
// in prose, so the second is matched on that prose.
func handshakeCause(err error) error {
	var mismatch *knownhosts.KeyError
	if errors.As(err, &mismatch) {
		return ErrHostKey
	}
	if strings.Contains(err.Error(), "unable to authenticate") {
		return ErrAuth
	}
	return ErrUnreachable
}
