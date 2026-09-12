package hostset_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
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
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/daemon"
	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type codeKeys struct {
	mu                   sync.Mutex
	temporary, permanent []byte
	completed            bool
	authenticated        int
}

func (k *codeKeys) Temporary(c protocol.RegistrationCode) error {
	k.temporary = ed25519.NewKeyFromSeed(c.Seed).Public().(ed25519.PublicKey)
	return nil
}
func (k *codeKeys) Claim(_ string, p []byte, _ time.Time) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.permanent = append([]byte(nil), p...)
	return nil
}
func (k *codeKeys) Complete(_ string, p []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.completed = true
	k.temporary = nil
	return nil
}
func (k *codeKeys) Remove(string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.temporary = nil
	if !k.completed {
		k.permanent = nil
	}
	return nil
}
func (k *codeKeys) Completed(_ string, p []byte) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.completed && string(p) == string(k.permanent)
}

func codeRig(t *testing.T, failure ...int) (hostset.Registration, protocol.RegistrationCode, *codeKeys) {
	t.Helper()
	keys := &codeKeys{}
	manager := daemon.NewHostRegistration(keys)
	mux := http.NewServeMux()
	mux.Handle("/registration", manager)
	mux.HandleFunc("/v1/events", func(w http.ResponseWriter, r *http.Request) {
		if len(failure) > 0 && failure[0] == 426 {
			w.WriteHeader(426)
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
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	raw, err := manager.Begin(ln.Addr().String(), "localuser", ssh.FingerprintSHA256(hostSigner.PublicKey()), daemonPort)
	if err != nil {
		t.Fatal(err)
	}
	c, err := protocol.ParseRegistrationCode(raw)
	if err != nil {
		t.Fatal(err)
	}
	serverConfig := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, p ssh.PublicKey) (*ssh.Permissions, error) {
		key := p.(ssh.CryptoPublicKey).CryptoPublicKey().(ed25519.PublicKey)
		keys.mu.Lock()
		defer keys.mu.Unlock()
		keys.authenticated++
		role := ""
		if string(key) == string(keys.temporary) {
			role = "temporary"
		} else if string(key) == string(keys.permanent) {
			role = "permanent"
		} else {
			return nil, errors.New("key refused")
		}
		return &ssh.Permissions{Extensions: map[string]string{"role": role}}, nil
	}}
	serverConfig.AddHostKey(hostSigner)
	go func() {
		for {
			tcp, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer tcp.Close()
				conn, channels, requests, err := ssh.NewServerConn(tcp, serverConfig)
				if err != nil {
					return
				}
				defer conn.Close()
				go ssh.DiscardRequests(requests)
				for next := range channels {
					if next.ChannelType() == "direct-tcpip" && conn.Permissions.Extensions["role"] == "permanent" {
						if len(failure) > 0 && failure[0] == 403 {
							next.Reject(ssh.Prohibited, "forwarding denied")
							continue
						}
						var target struct {
							Host       string
							Port       uint32
							Origin     string
							OriginPort uint32
						}
						if ssh.Unmarshal(next.ExtraData(), &target) != nil {
							next.Reject(ssh.Prohibited, "invalid")
							continue
						}
						remote, err := net.Dial("tcp", net.JoinHostPort(target.Host, fmt.Sprint(target.Port)))
						if err != nil {
							next.Reject(ssh.ConnectionFailed, "unreachable")
							continue
						}
						channel, reqs, err := next.Accept()
						if err != nil {
							remote.Close()
							continue
						}
						go ssh.DiscardRequests(reqs)
						go func() {
							defer remote.Close()
							defer channel.Close()
							go func() { io.Copy(remote, channel); remote.Close() }()
							io.Copy(channel, remote)
						}()
					} else if next.ChannelType() == "session" && conn.Permissions.Extensions["role"] == "temporary" {
						channel, reqs, err := next.Accept()
						if err != nil {
							continue
						}
						go func() {
							defer channel.Close()
							for req := range reqs {
								var command struct{ Command string }
								if req.Type != "exec" || ssh.Unmarshal(req.Payload, &command) != nil || command.Command != "dispatch-register" {
									req.Reply(false, nil)
									continue
								}
								req.Reply(true, nil)
								var r protocol.RegistrationRequest
								status := uint32(1)
								if json.NewDecoder(io.LimitReader(channel, protocol.RegistrationLimit)).Decode(&r) == nil && manager.Apply(r) == nil {
									status = 0
								}
								channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
								return
							}
						}()
					} else {
						next.Reject(ssh.Prohibited, "restricted")
					}
				}
			}()
		}
	}()
	return hostset.Registration{ID: "desk", Address: c.Address, User: c.User, DaemonPort: daemonPort, Dir: t.TempDir()}, c, keys
}

func TestCodeRegistrationChecksSSHAndRecoversLostCommit(t *testing.T) {
	req, c, keys := codeRig(t)
	var saved hostset.Registered
	prepared := false
	err := hostset.RegisterCode(t.Context(), req, c, func(host hostset.Registered) error {
		if _, err := os.Stat(host.KnownHosts); err != nil {
			t.Error("intent preceded trust", err)
		}
		saved = host
		prepared = true
		return nil
	}, func(hostset.Registered) error { return errors.New("config write failed") })
	if err == nil || !prepared {
		t.Fatal("expected saved recovery intent", err)
	}
	if _, err := os.Stat(saved.KnownHosts); err != nil {
		t.Fatal("failed commit removed recovery trust", err)
	}
	if err := hostset.RecoverRegistration(t.Context(), saved, c.ID, func(hostset.Registered) error { return nil }); err != nil {
		t.Fatal(err)
	}
	keys.mu.Lock()
	defer keys.mu.Unlock()
	if !keys.completed || keys.temporary != nil {
		t.Fatal("registration did not complete")
	}
}

func TestCodeRegistrationRefusesFingerprintBeforeAuthentication(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprint(conflict), func(t *testing.T) {
			req, c, keys := codeRig(t)
			_, other, _ := ed25519.GenerateKey(rand.Reader)
			signer, _ := ssh.NewSignerFromKey(other)
			if conflict {
				os.WriteFile(filepath.Join(req.Dir, "known_hosts"), []byte(knownhosts.Line([]string{req.Address}, signer.PublicKey())+"\n"), 0600)
			} else {
				c.Fingerprint = ssh.FingerprintSHA256(signer.PublicKey())
			}
			commit := func(hostset.Registered) error { t.Error("untrusted Host reached persistence"); return nil }
			if hostset.RegisterCode(t.Context(), req, c, commit, commit) == nil {
				t.Fatal("untrusted Host accepted")
			}
			keys.mu.Lock()
			defer keys.mu.Unlock()
			if keys.authenticated != 0 {
				t.Fatal("authentication happened before trust validation")
			}
		})
	}
}

func TestCodeRegistrationRollsBackBeforeIntent(t *testing.T) {
	req, c, keys := codeRig(t)
	err := hostset.RegisterCode(context.Background(), req, c, func(hostset.Registered) error { return errors.New("intent write failed") }, func(hostset.Registered) error { t.Error("commit called"); return nil })
	if err == nil || !strings.Contains(err.Error(), "removed") {
		t.Fatal("rollback not confirmed", err)
	}
	keys.mu.Lock()
	defer keys.mu.Unlock()
	if keys.permanent != nil {
		t.Fatal("failed registration retained new access")
	}
}

func TestCodeRegistrationRollsBackForwardingHandshakeAndTrustFailures(t *testing.T) {
	for _, failure := range []int{403, 426, 500} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			req, c, keys := codeRig(t, failure)
			if failure == 500 {
				if err := os.Mkdir(filepath.Join(req.Dir, "known_hosts.new"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			commit := func(hostset.Registered) error { t.Error("failed checks reached persistence"); return nil }
			if err := hostset.RegisterCode(t.Context(), req, c, commit, commit); err == nil {
				t.Fatal("failed stage succeeded")
			}
			keys.mu.Lock()
			defer keys.mu.Unlock()
			if keys.permanent != nil {
				t.Fatal("failed check retained authorization")
			}
		})
	}
}

func TestCodeRegistrationReusesTheHubIdentity(t *testing.T) {
	first, c, _ := codeRig(t)
	commit := func(hostset.Registered) error { return nil }
	if err := hostset.RegisterCode(t.Context(), first, c, commit, commit); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(first.Dir, "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	second, c, _ := codeRig(t)
	second.Dir = first.Dir
	second.ID = "other"
	if err := hostset.RegisterCode(t.Context(), second, c, commit, commit); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(first.Dir, "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("registering a second Host replaced the Hub identity")
	}
}
