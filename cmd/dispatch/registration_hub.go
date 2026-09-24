package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// registerClientHost sends one registration down the way the Client chose.
func registerClientHost(ctx context.Context, path string, h *hub.Hub, in hub.RegistrationInput) error {
	if in.Method == hub.RegisterByLogin {
		return registerLoginHost(ctx, path, h, in)
	}
	return registerCodeHost(ctx, path, h, in)
}

func registerCodeHost(ctx context.Context, path string, h *hub.Hub, in hub.RegistrationInput) error {
	code, err := protocol.ParseRegistrationCode(in.Code)
	if err != nil {
		return err
	}
	address, err := resolveRegistrationAddress(code.Address, strings.TrimSpace(in.Address))
	if err != nil {
		return err
	}
	user := code.User
	if typed := strings.TrimSpace(in.User); typed != "" {
		user = typed
	}
	dir, err := hubSSHDir()
	if err != nil {
		return err
	}
	req := hub.Registration{ID: hub.HostID(in.ID), Address: address, User: user, DaemonPort: code.DaemonPort, Dir: dir}
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
	if _, err := hubWith(path, hub.Registered{ID: req.ID, Address: address, User: user, DaemonPort: code.DaemonPort}); err != nil {
		return err
	}
	var registered hub.Registered
	err = hub.RegisterCode(ctx, req, code, in.Password, func(host hub.Registered) error {
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
	return nil
}

// registrationAddress resolves what the Client typed against the address the
// code carries. An entry of only digits is a port, because a host name cannot
// be one, so correcting the port alone keeps the code's host.
func resolveRegistrationAddress(coded, typed string) (string, error) {
	if typed == "" {
		return normalizeRegistrationAddress(coded)
	}
	if strings.IndexFunc(typed, func(r rune) bool { return r < '0' || r > '9' }) < 0 {
		host, _, err := net.SplitHostPort(coded)
		if err != nil {
			return "", err
		}
		return normalizeRegistrationAddress(net.JoinHostPort(host, typed))
	}
	return normalizeRegistrationAddress(withPort(typed))
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
