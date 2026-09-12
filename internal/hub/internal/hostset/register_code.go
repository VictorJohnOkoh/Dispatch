package hostset

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// RegisterCode prepares durable recovery data before completing authorization.
// After prepare succeeds, cancellation must never revoke the committed intent.
func RegisterCode(ctx context.Context, req Registration, code protocol.RegistrationCode, prepare, commit func(Registered) error) error {
	if err := code.Validate(); err != nil {
		return err
	}
	if err := req.validate(); err != nil {
		return err
	}
	temporary, err := ssh.NewSignerFromKey(ed25519.NewKeyFromSeed(code.Seed))
	if err != nil {
		return err
	}
	var hostKey ssh.PublicKey
	check := func(address string, remote net.Addr, key ssh.PublicKey) error {
		if ssh.FingerprintSHA256(key) != code.Fingerprint {
			return ErrHostKey
		}
		for _, known := range append([]string{filepath.Join(req.Dir, trustFile)}, req.TrustFiles...) {
			if _, err := os.Stat(known); err == nil {
				verify, err := knownhosts.New(known)
				if err != nil {
					return err
				}
				if err := verify(address, remote, key); err != nil {
					var missing *knownhosts.KeyError
					if !errors.As(err, &missing) || len(missing.Want) != 0 {
						return ErrHostKey
					}
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		hostKey = key
		return nil
	}
	client, err := connect(ctx, req.Address, &ssh.ClientConfig{User: req.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(temporary)}, HostKeyCallback: check, HostKeyAlgorithms: []string{ssh.KeyAlgoED25519}, Timeout: connectTimeout})
	if err != nil {
		return fmt.Errorf("temporary SSH login or Host fingerprint check failed: %w", err)
	}
	defer client.Close()
	permanent, err := identity(req.Dir)
	if err != nil {
		return err
	}
	claim, err := signedRegistration(code.ID, "claim", permanent, temporary)
	if err != nil {
		return err
	}
	if err := registrationCommand(ctx, client, claim); err != nil {
		return abortCode(client, code.ID, permanent, temporary, errors.New("the Host refused the registration claim"))
	}
	prepared := false
	var completionErr error
	err = prove(ctx, req, permanent, hostKey, net.JoinHostPort("127.0.0.1", strconv.Itoa(req.DaemonPort)), func(host Registered) error {
		if err := prepare(host); err != nil {
			return err
		}
		prepared = true
		completionErr = RecoverRegistration(ctx, host, code.ID, commit)
		return nil
	})
	if completionErr != nil {
		return errors.New("registration has a saved recovery record; restart the Hub to finish it before registering another Host")
	}
	if err != nil {
		if prepared {
			return errors.New("registration has a saved recovery record; restart the Hub to finish it before registering another Host")
		}
		return abortCode(client, code.ID, permanent, temporary, err)
	}
	return nil
}

func abortCode(client *ssh.Client, id string, permanent, temporary ssh.Signer, why error) error {
	r, err := signedRegistration(id, "abort", permanent, temporary)
	if err == nil {
		err = registrationCommand(context.Background(), client, r)
	}
	if err != nil {
		return fmt.Errorf("%w; cleanup could not be confirmed; cancel registration locally on the Host", why)
	}
	return fmt.Errorf("%w; the attempt's authorization was removed", why)
}

func signedRegistration(id, action string, permanent, signing ssh.Signer) (protocol.RegistrationRequest, error) {
	r := protocol.RegistrationRequest{ID: id, Action: action}
	key, ok := permanent.PublicKey().(ssh.CryptoPublicKey)
	if !ok {
		return r, errors.New("an ed25519 key is required")
	}
	pub, ok := key.CryptoPublicKey().(ed25519.PublicKey)
	if !ok {
		return r, errors.New("an ed25519 key is required")
	}
	r.PublicKey = pub
	sig, err := signing.Sign(rand.Reader, r.SigningBytes())
	if err != nil {
		return r, err
	}
	r.Signature = sig.Blob
	return r, nil
}

func registrationCommand(ctx context.Context, client *ssh.Client, r protocol.RegistrationRequest) error {
	ctx, cancel := context.WithTimeout(ctx, remoteTimeout)
	defer cancel()
	closeClient := context.AfterFunc(ctx, func() { client.Close() })
	defer closeClient()
	s, err := client.NewSession()
	if err != nil {
		return err
	}
	defer s.Close()
	stop := context.AfterFunc(ctx, func() { s.Close() })
	defer stop()
	b, _ := json.Marshal(r)
	s.Stdin = bytes.NewReader(b)
	// Discard output: a remote command must never inject credentials into errors.
	s.Stdout = io.Discard
	s.Stderr = io.Discard
	return s.Run("dispatch-register")
}

func RecoverRegistration(ctx context.Context, host Registered, id string, commit func(Registered) error) error {
	b, err := os.ReadFile(host.KeyPath)
	if err != nil {
		return err
	}
	signer, err := ssh.ParsePrivateKey(b)
	if err != nil {
		return err
	}
	check, err := knownhosts.New(host.KnownHosts)
	if err != nil {
		return err
	}
	client, err := connect(ctx, host.Address, &ssh.ClientConfig{User: host.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: check, Timeout: connectTimeout})
	if err != nil {
		return err
	}
	defer client.Close()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(host.DaemonPort))
	if err := checkDaemon(ctx, client, address); err != nil {
		return err
	}
	r, err := signedRegistration(id, "complete", signer, signer)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(r)
	ctx, cancel := context.WithTimeout(ctx, remoteTimeout)
	defer cancel()
	conn, err := client.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	transport := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) { return conn, nil }, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://daemon/registration", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return errors.New("Host Registration completion refused; check the Host locally")
	}
	return commit(host)
}
