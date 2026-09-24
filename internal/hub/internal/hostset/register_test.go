package hostset_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const hostPassword = "correct horse"

// testHost is a Host with a password login, SFTP over the account's home, a
// Daemon behind a forwarded channel, and the key options OpenSSH enforces. A key
// login is accepted only for a key the keys file names, so a test sees the
// install work rather than being told it did.
type testHost struct {
	req      hostset.Registration
	key      ssh.PublicKey
	keys     string // the file OpenSSH reads for this account
	userKeys string

	mu        sync.Mutex
	passwords int
	commands  []string
}

type hostOptions struct {
	admin    bool // whoami /groups lists Administrators
	upgrade  bool // the Daemon answers 426
	unixHost bool // whoami /groups fails, as it does off Windows
}

func newTestHost(t *testing.T, opt hostOptions) *testHost {
	t.Helper()
	home := t.TempDir()
	h := &testHost{userKeys: filepath.Join(home, ".ssh", "authorized_keys")}
	h.keys = h.userKeys
	if opt.admin {
		h.keys = filepath.Join(t.TempDir(), "administrators_authorized_keys")
		// SFTP names a Windows file as /C:/..., the same form the real path uses.
		remote := filepath.ToSlash(h.keys)
		if !strings.HasPrefix(remote, "/") {
			remote = "/" + remote
		}
		t.Cleanup(hostset.UseAdminKeys(remote))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		if opt.upgrade {
			w.WriteHeader(http.StatusUpgradeRequired)
			fmt.Fprint(w, `{"speaks":[2]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: hello\ndata: {\"protocol\":1}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	d := httptest.NewServer(mux)
	t.Cleanup(d.Close)
	_, port, _ := net.SplitHostPort(d.Listener.Addr().String())
	var daemonPort int
	fmt.Sscan(port, &daemonPort)

	_, hostPrivate, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, _ := ssh.NewSignerFromKey(hostPrivate)
	h.key = hostSigner.PublicKey()
	config := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, given []byte) (*ssh.Permissions, error) {
			h.mu.Lock()
			h.passwords++
			h.mu.Unlock()
			if string(given) != hostPassword {
				return nil, errors.New("wrong password")
			}
			return &ssh.Permissions{}, nil
		},
		PublicKeyCallback: func(_ ssh.ConnMetadata, offered ssh.PublicKey) (*ssh.Permissions, error) {
			return h.keyLogin(offered)
		},
	}
	config.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go h.serve(ln, config, home, opt)

	h.req = hostset.Registration{ID: "desk", Address: ln.Addr().String(), User: "localuser", DaemonPort: daemonPort, Dir: t.TempDir()}
	return h
}

func (h *testHost) code() protocol.RegistrationCode {
	return protocol.RegistrationCode{Address: h.req.Address, User: h.req.User, DaemonPort: h.req.DaemonPort, Fingerprint: ssh.FingerprintSHA256(h.key)}
}

// keyLogin reads the keys file the way sshd does and carries the line's options
// into the connection, so the channels below can enforce them.
func (h *testHost) keyLogin(offered ssh.PublicKey) (*ssh.Permissions, error) {
	body, err := os.ReadFile(h.keys)
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(body), "\n") {
		key, _, options, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil || string(key.Marshal()) != string(offered.Marshal()) {
			continue
		}
		p := &ssh.Permissions{Extensions: map[string]string{}}
		for _, option := range options {
			name, value, _ := strings.Cut(option, "=")
			p.Extensions[name] = strings.Trim(value, `"`)
		}
		return p, nil
	}
	return nil, errors.New("key refused")
}

func (h *testHost) serve(ln net.Listener, config *ssh.ServerConfig, home string, opt hostOptions) {
	for {
		tcp, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer tcp.Close()
			conn, channels, requests, err := ssh.NewServerConn(tcp, config)
			if err != nil {
				return
			}
			defer conn.Close()
			go ssh.DiscardRequests(requests)
			options := conn.Permissions.Extensions
			_, restricted := options["restrict"]
			for next := range channels {
				switch {
				case next.ChannelType() == "session" && !restricted:
					go h.session(next, home, opt)
				case next.ChannelType() == "direct-tcpip":
					go forward(next, options["permitopen"], restricted)
				default:
					next.Reject(ssh.Prohibited, "restricted")
				}
			}
		}()
	}
}

func (h *testHost) session(next ssh.NewChannel, home string, opt hostOptions) {
	channel, requests, err := next.Accept()
	if err != nil {
		return
	}
	defer channel.Close()
	for req := range requests {
		var payload struct{ Text string }
		ssh.Unmarshal(req.Payload, &payload)
		switch {
		case req.Type == "subsystem" && payload.Text == "sftp":
			req.Reply(true, nil)
			if server, err := sftp.NewServer(channel, sftp.WithServerWorkingDirectory(home)); err == nil {
				server.Serve()
			}
			return
		case req.Type == "exec":
			req.Reply(true, nil)
			h.mu.Lock()
			h.commands = append(h.commands, payload.Text)
			h.mu.Unlock()
			status := uint32(0)
			switch {
			case payload.Text == "whoami /groups" && opt.admin:
				io.WriteString(channel, "BUILTIN\\Administrators  Alias  S-1-5-32-544  Mandatory group, Enabled group\r\n")
			case payload.Text == "whoami /groups" && opt.unixHost:
				status = 1
			case payload.Text == "whoami /groups":
				io.WriteString(channel, "BUILTIN\\Users  Alias  S-1-5-32-545  Mandatory group, Enabled group\r\n")
			case strings.HasPrefix(payload.Text, "icacls "):
			default:
				status = 127
			}
			channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
			return
		default:
			req.Reply(false, nil)
		}
	}
}

// forward opens a channel to the Daemon. A restricted key may open only the
// target its permitopen names, which is the rule OpenSSH applies.
func forward(next ssh.NewChannel, permit string, restricted bool) {
	var target struct {
		Host       string
		Port       uint32
		Origin     string
		OriginPort uint32
	}
	if ssh.Unmarshal(next.ExtraData(), &target) != nil {
		next.Reject(ssh.Prohibited, "invalid")
		return
	}
	address := net.JoinHostPort(target.Host, fmt.Sprint(target.Port))
	if restricted && address != permit {
		next.Reject(ssh.Prohibited, "not permitted")
		return
	}
	remote, err := net.Dial("tcp", address)
	if err != nil {
		next.Reject(ssh.ConnectionFailed, "unreachable")
		return
	}
	channel, requests, err := next.Accept()
	if err != nil {
		remote.Close()
		return
	}
	go ssh.DiscardRequests(requests)
	defer remote.Close()
	defer channel.Close()
	go func() { io.Copy(remote, channel); remote.Close() }()
	io.Copy(channel, remote)
}

func hubKey(t *testing.T, saved hostset.Registered) ssh.PublicKey {
	t.Helper()
	body, err := os.ReadFile(saved.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(body)
	if err != nil {
		t.Fatal(err)
	}
	return signer.PublicKey()
}

func restrictedLine(key ssh.PublicKey, daemonPort int) string {
	return fmt.Sprintf(`restrict,port-forwarding,permitopen="127.0.0.1:%d",command="exit 1" %s dispatch-hub`, daemonPort, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))))
}

func readHostFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return string(body)
}

func writeHostFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func register(t *testing.T, h *testHost, password string) (hostset.Registered, error) {
	t.Helper()
	var saved hostset.Registered
	err := hostset.RegisterCode(t.Context(), h.req, h.code(), password, func(host hostset.Registered) error {
		saved = host
		return nil
	})
	return saved, err
}

func TestACodeAndPasswordInstallTheRestrictedHubKey(t *testing.T) {
	h := newTestHost(t, hostOptions{})
	const unrelated = "# a key the user added\nssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl other\n"
	writeHostFile(t, h.keys, unrelated)

	saved, err := register(t, h, hostPassword)
	if err != nil {
		t.Fatal(err)
	}
	want := unrelated + restrictedLine(hubKey(t, saved), h.req.DaemonPort) + "\n"
	if got := readHostFile(t, h.keys); got != want {
		t.Fatalf("authorized_keys =\n%s\nwant\n%s", got, want)
	}
	check, err := knownhosts.New(saved.KnownHosts)
	if err != nil {
		t.Fatal(err)
	}
	address, _ := net.ResolveTCPAddr("tcp", h.req.Address)
	if err := check(h.req.Address, address, h.key); err != nil {
		t.Fatal("the Host was not trusted", err)
	}
}

func TestAWrongFingerprintSendsNoPassword(t *testing.T) {
	h := newTestHost(t, hostOptions{})
	c := h.code()
	c.Fingerprint = "SHA256:" + strings.Repeat("A", 43)
	err := hostset.RegisterCode(t.Context(), h.req, c, hostPassword, func(hostset.Registered) error {
		t.Error("a Host the code does not name was committed")
		return nil
	})
	if !errors.Is(err, hostset.ErrHostKey) {
		t.Fatalf("error = %v, want ErrHostKey", err)
	}
	if h.passwords != 0 {
		t.Fatal("the password went to a Host the code does not name")
	}
}

func TestAConflictingKnownHostsLineStopsRegistration(t *testing.T) {
	h := newTestHost(t, hostOptions{})
	_, otherPrivate, _ := ed25519.GenerateKey(rand.Reader)
	other, _ := ssh.NewSignerFromKey(otherPrivate)
	known := filepath.Join(t.TempDir(), "known_hosts")
	writeHostFile(t, known, knownhosts.Line([]string{h.req.Address}, other.PublicKey())+"\n")
	h.req.TrustFiles = []string{known}

	if _, err := register(t, h, hostPassword); !errors.Is(err, hostset.ErrHostKey) {
		t.Fatalf("error = %v, want ErrHostKey", err)
	}
	if h.passwords != 0 {
		t.Fatal("the password went out before the conflict was found")
	}
}

func TestAWrongPasswordRegistersNothing(t *testing.T) {
	h := newTestHost(t, hostOptions{})
	if _, err := register(t, h, "guess"); !errors.Is(err, hostset.ErrAuth) {
		t.Fatalf("error = %v, want ErrAuth", err)
	}
	if _, err := os.Stat(h.keys); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused login wrote authorized_keys", err)
	}
}

func TestAFailedHandshakeTakesTheKeyBack(t *testing.T) {
	h := newTestHost(t, hostOptions{upgrade: true})
	const before = "# the file as the user left it\n"
	writeHostFile(t, h.keys, before)

	_, err := register(t, h, hostPassword)
	if !errors.Is(err, hostset.ErrIncompatible) {
		t.Fatalf("error = %v, want ErrIncompatible", err)
	}
	if got := readHostFile(t, h.keys); got != before {
		t.Fatalf("authorized_keys = %q, want it back as %q", got, before)
	}
}

func TestAFailedCommitTakesTheKeyAndTrustBack(t *testing.T) {
	h := newTestHost(t, hostOptions{})
	refused := errors.New("hub.json is read-only")
	err := hostset.RegisterCode(t.Context(), h.req, h.code(), hostPassword, func(hostset.Registered) error { return refused })
	if !errors.Is(err, refused) {
		t.Fatalf("error = %v, want the commit error", err)
	}
	if got := readHostFile(t, h.keys); got != "" {
		t.Fatalf("authorized_keys = %q, want it empty again", got)
	}
	if got := readHostFile(t, filepath.Join(h.req.Dir, "known_hosts")); got != "" {
		t.Fatalf("known_hosts = %q, want no trust left behind", got)
	}
}

func TestAnAdministratorGetsTheAdministratorsFileAndItsACL(t *testing.T) {
	h := newTestHost(t, hostOptions{admin: true})
	saved, err := register(t, h, hostPassword)
	if err != nil {
		t.Fatal(err)
	}
	if got := readHostFile(t, h.keys); got != restrictedLine(hubKey(t, saved), h.req.DaemonPort)+"\n" {
		t.Fatalf("administrators_authorized_keys = %q", got)
	}
	if got := readHostFile(t, h.userKeys); got != "" {
		t.Fatalf("the account's own authorized_keys was written: %q", got)
	}
	acl := false
	for _, command := range h.commands {
		acl = acl || (strings.HasPrefix(command, "icacls ") && strings.Contains(command, "*S-1-5-18:F") && strings.Contains(command, "*S-1-5-32-544:F") && strings.Contains(command, "/inheritance:r"))
	}
	if !acl {
		t.Fatalf("no ACL for SYSTEM and Administrators was set; commands: %q", h.commands)
	}
}

func TestAHostOffWindowsUsesTheAccountsOwnFile(t *testing.T) {
	h := newTestHost(t, hostOptions{unixHost: true})
	if _, err := register(t, h, hostPassword); err != nil {
		t.Fatal(err)
	}
	if readHostFile(t, h.userKeys) == "" {
		t.Fatal("the Hub key is not in the account's authorized_keys")
	}
}

// A Host registered before the key was restricted holds the bare key. Registering
// it again swaps that line for the restricted one, and the Hub keeps one identity.
func TestRegisteringAgainRestrictsAnOlderLine(t *testing.T) {
	h := newTestHost(t, hostOptions{})
	saved, err := register(t, h, hostPassword)
	if err != nil {
		t.Fatal(err)
	}
	key := hubKey(t, saved)
	const unrelated = "# a key the user added\n"
	writeHostFile(t, h.keys, unrelated+strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))+" old-registration\n")

	again, err := register(t, h, hostPassword)
	if err != nil {
		t.Fatal(err)
	}
	if string(hubKey(t, again).Marshal()) != string(key.Marshal()) {
		t.Fatal("the second registration made a new Hub identity")
	}
	if got, want := readHostFile(t, h.keys), unrelated+restrictedLine(key, h.req.DaemonPort)+"\n"; got != want {
		t.Fatalf("authorized_keys =\n%s\nwant\n%s", got, want)
	}
}

func TestAnExistingLoginRefusesAHostNoKnownHostsFileNames(t *testing.T) {
	h := newTestHost(t, hostOptions{})
	login := hostset.Login{Auth: []ssh.AuthMethod{ssh.Password(hostPassword)}, Known: []string{filepath.Join(t.TempDir(), "known_hosts")}}
	err := hostset.RegisterLogin(t.Context(), h.req, login, func(hostset.Registered) error {
		t.Error("an unknown Host was registered")
		return nil
	})
	if !errors.Is(err, hostset.ErrHostKey) {
		t.Fatalf("error = %v, want ErrHostKey", err)
	}
}

func TestAnExistingLoginInstallsTheRestrictedHubKey(t *testing.T) {
	h := newTestHost(t, hostOptions{admin: true})
	known := filepath.Join(t.TempDir(), "known_hosts")
	writeHostFile(t, known, knownhosts.Line([]string{h.req.Address}, h.key)+"\n")
	login := hostset.Login{Auth: []ssh.AuthMethod{ssh.Password(hostPassword)}, Known: []string{known}}
	var saved hostset.Registered
	if err := hostset.RegisterLogin(t.Context(), h.req, login, func(host hostset.Registered) error {
		saved = host
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := readHostFile(t, h.keys); got != restrictedLine(hubKey(t, saved), h.req.DaemonPort)+"\n" {
		t.Fatalf("administrators_authorized_keys = %q", got)
	}
}
