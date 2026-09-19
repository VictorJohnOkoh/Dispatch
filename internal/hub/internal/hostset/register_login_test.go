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
	"testing"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const loginPassword = "correct horse"

// loginRig is a Host with a password login, an SFTP subsystem over the account's
// home, and a Daemon behind a forwarded channel. It answers a key login only
// with a key that authorized_keys already names, so the test sees the install
// work rather than being told it did.
func loginRig(t *testing.T) (hostset.Registration, ssh.PublicKey, string) {
	t.Helper()
	home := t.TempDir()
	authorized := filepath.Join(home, ".ssh", "authorized_keys")

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
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
	config := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, given []byte) (*ssh.Permissions, error) {
			if string(given) != loginPassword {
				return nil, errors.New("wrong password")
			}
			return &ssh.Permissions{}, nil
		},
		PublicKeyCallback: func(_ ssh.ConnMetadata, offered ssh.PublicKey) (*ssh.Permissions, error) {
			body, err := os.ReadFile(authorized)
			if err != nil {
				return nil, err
			}
			line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(offered)))
			for _, have := range strings.Split(string(body), "\n") {
				if strings.TrimSpace(have) == line {
					return &ssh.Permissions{}, nil
				}
			}
			return nil, errors.New("key refused")
		},
	}
	config.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go serveLoginHost(ln, config, home)

	return hostset.Registration{ID: "desk", Address: ln.Addr().String(), User: "localuser", DaemonPort: daemonPort, Dir: t.TempDir()}, hostSigner.PublicKey(), authorized
}

func serveLoginHost(ln net.Listener, config *ssh.ServerConfig, home string) {
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
			for next := range channels {
				switch next.ChannelType() {
				case "session":
					go serveSFTP(next, home)
				case "direct-tcpip":
					go forwardChannel(next)
				default:
					next.Reject(ssh.Prohibited, "restricted")
				}
			}
		}()
	}
}

func serveSFTP(next ssh.NewChannel, home string) {
	channel, requests, err := next.Accept()
	if err != nil {
		return
	}
	defer channel.Close()
	for req := range requests {
		var subsystem struct{ Name string }
		if req.Type != "subsystem" || ssh.Unmarshal(req.Payload, &subsystem) != nil || subsystem.Name != "sftp" {
			req.Reply(false, nil)
			continue
		}
		req.Reply(true, nil)
		server, err := sftp.NewServer(channel, sftp.WithServerWorkingDirectory(home))
		if err != nil {
			return
		}
		server.Serve()
		return
	}
}

func forwardChannel(next ssh.NewChannel) {
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
	remote, err := net.Dial("tcp", net.JoinHostPort(target.Host, fmt.Sprint(target.Port)))
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

func TestAPasswordLoginInstallsTheHubKeyAndProvesTheDaemon(t *testing.T) {
	req, hostKey, authorized := loginRig(t)
	var saved hostset.Registered
	login := hostset.Login{Auth: []ssh.AuthMethod{ssh.Password(loginPassword)}, NewHost: true}
	if err := hostset.RegisterLogin(t.Context(), req, login, func(host hostset.Registered) error {
		saved = host
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(authorized)
	if err != nil {
		t.Fatal(err)
	}
	managed, err := os.ReadFile(saved.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(managed)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	if !strings.Contains(string(body), line) {
		t.Fatal("authorized_keys does not hold the Hub's key")
	}
	check, err := knownhosts.New(saved.KnownHosts)
	if err != nil {
		t.Fatal(err)
	}
	address, _ := net.ResolveTCPAddr("tcp", req.Address)
	if err := check(req.Address, address, hostKey); err != nil {
		t.Fatal("the Host was not trusted", err)
	}
}

func TestAPasswordLoginWithTheWrongPasswordRegistersNothing(t *testing.T) {
	req, _, authorized := loginRig(t)
	login := hostset.Login{Auth: []ssh.AuthMethod{ssh.Password("guess")}, NewHost: true}
	err := hostset.RegisterLogin(t.Context(), req, login, func(hostset.Registered) error {
		t.Error("a refused login committed a Host")
		return nil
	})
	if err == nil {
		t.Fatal("a wrong password registered a Host")
	}
	if _, err := os.Stat(authorized); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused login wrote authorized_keys", err)
	}
}

func TestAnExistingLoginRefusesAHostNoKnownHostsFileNames(t *testing.T) {
	req, _, _ := loginRig(t)
	login := hostset.Login{Auth: []ssh.AuthMethod{ssh.Password(loginPassword)}, Known: []string{filepath.Join(t.TempDir(), "known_hosts")}}
	err := hostset.RegisterLogin(t.Context(), req, login, func(hostset.Registered) error {
		t.Error("an unknown Host was registered")
		return nil
	})
	if !errors.Is(err, hostset.ErrHostKey) {
		t.Fatalf("error = %v, want ErrHostKey", err)
	}
}

func TestAnExistingLoginAcceptsAHostKnownHostsAlreadyNames(t *testing.T) {
	req, hostKey, _ := loginRig(t)
	known := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(known, []byte(knownhosts.Line([]string{req.Address}, hostKey)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	login := hostset.Login{Auth: []ssh.AuthMethod{ssh.Password(loginPassword)}, Known: []string{known}}
	committed := false
	if err := hostset.RegisterLogin(t.Context(), req, login, func(hostset.Registered) error {
		committed = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !committed {
		t.Fatal("a known Host was not committed")
	}
}
