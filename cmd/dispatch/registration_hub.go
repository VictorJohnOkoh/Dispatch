package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// This is committed intent, containing only public connection data. Once saved,
// retries finish it instead of revoking authorization after a lost reply.
type registrationIntent struct {
	ID   string         `json:"registrationId"`
	Host hub.Registered `json:"host"`
}

func registerClientHost(ctx context.Context, path string, h *hub.Hub, in hub.RegistrationInput) error {
	code, err := protocol.ParseRegistrationCode(in.Code)
	if err != nil {
		return err
	}
	address := code.Address
	if in.Address != "" {
		address = withPort(in.Address)
	}
	address, err = normalizeRegistrationAddress(address)
	if err != nil {
		return err
	}
	replacing := false
	if b, readErr := os.ReadFile(path + ".registration"); readErr == nil {
		var saved registrationIntent
		if json.Unmarshal(b, &saved) != nil || saved.Host.ID != hub.HostID(in.ID) || saved.Host.Address != address || saved.Host.User != code.User || saved.Host.DaemonPort != code.DaemonPort {
			return errors.New("a different Host Registration needs recovery; use its Host id and connection profile")
		}
		if err := recoverClientRegistration(ctx, path); err == nil {
			for _, id := range h.All() {
				if id == in.ID {
					return nil
				}
			}
			return h.Attach(saved.Host)
		}
		replacing = true
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	dir, err := hubSSHDir()
	if err != nil {
		return err
	}
	req := hub.Registration{ID: hub.HostID(in.ID), Address: address, User: code.User, DaemonPort: code.DaemonPort, Dir: dir}
	if known, err := defaultKnownHosts(); err == nil {
		req.TrustFiles = append(req.TrustFiles, known)
	}
	if cfg, err := loadHubOrEmpty(path); err == nil {
		for _, profile := range cfg.Hosts {
			if profile.KnownHosts != "" {
				req.TrustFiles = append(req.TrustFiles, profile.KnownHosts)
			}
		}
	}
	if _, err := hubWith(path, hub.Registered{ID: req.ID, Address: address, User: code.User, DaemonPort: code.DaemonPort}); err != nil {
		return err
	}
	var registered hub.Registered
	err = hub.RegisterCode(ctx, req, code, func(host hub.Registered) error {
		if _, err := hubWith(path, host); err != nil {
			return err
		}
		b, err := json.Marshal(registrationIntent{ID: code.ID, Host: host})
		if err != nil {
			return err
		}
		if replacing {
			return writeRegistrationFile(path+".registration", b)
		}
		if _, err := os.Stat(path + ".registration"); !errors.Is(err, os.ErrNotExist) {
			return errors.New("a registration recovery record already exists")
		}
		return writeRegistrationFile(path+".registration", b)
	}, func(host hub.Registered) error {
		if err := commitHost(path, host); err != nil {
			return err
		}
		registered = host
		return nil
	})
	if err != nil {
		return err
	}
	if err := h.Attach(registered); err != nil {
		return fmt.Errorf("Host saved; restart the Hub to attach it: %w", err)
	}
	if err := os.Remove(path + ".registration"); err != nil {
		return errors.New("Host registered; the recovery record could not be removed")
	}
	return nil
}

func recoverClientRegistration(ctx context.Context, path string) error {
	b, err := os.ReadFile(path + ".registration")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var intent registrationIntent
	if len(b) > 8192 || json.Unmarshal(b, &intent) != nil || len(intent.ID) != 32 || !protocol.ValidHostID(string(intent.Host.ID)) {
		return errors.New("invalid Host Registration recovery record; inspect it locally")
	}
	err = hub.RecoverRegistration(ctx, intent.Host, intent.ID, func(host hub.Registered) error {
		// A crash after the config rename must not append the same Host again.
		cfg, loadErr := loadHubOrEmpty(path)
		if loadErr != nil {
			return loadErr
		}
		for _, have := range cfg.Hosts {
			if have.ID == string(host.ID) {
				if have.Address == host.Address && have.User == host.User && have.KeyPath == host.KeyPath && have.KnownHosts == host.KnownHosts && have.DaemonPort == host.DaemonPort {
					return nil
				}
				return errors.New("recovery would replace an existing Host")
			}
		}
		return commitHost(path, host)
	})
	if err != nil {
		return fmt.Errorf("Host Registration needs recovery on the Host; keep %s.registration: %w", path, err)
	}
	return os.Remove(path + ".registration")
}

func normalizeRegistrationAddress(address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return "", errors.New("SSH address must be host:port")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return "", errors.New("SSH port must be between 1 and 65535")
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	} else {
		host = strings.TrimSuffix(strings.ToLower(host), ".")
	}
	return net.JoinHostPort(host, strconv.Itoa(p)), nil
}
