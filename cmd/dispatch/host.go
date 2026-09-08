package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/VictorJohnOkoh/Dispatch/internal/config"
	"github.com/VictorJohnOkoh/Dispatch/internal/hub"
)

const (
	defaultSSHPort    = "22"
	defaultDaemonPort = 7717
	defaultHubListen  = "127.0.0.1:7700"
	hostUsage         = "Host Registration: start dispatch hub, then open /hosts in the Client"
)

func runHost(_ context.Context, _ []string, out io.Writer) int {
	fmt.Fprintln(out, "dispatch host add was replaced by Host Registration in the Client. Start dispatch hub and open http://127.0.0.1:7700/hosts.")
	return 2
}

func commitHost(path string, host hub.Registered) error {
	cfg, err := hubWith(path, host)
	if err != nil {
		return err
	}
	return config.SaveHub(path, cfg)
}

func hubWith(path string, host hub.Registered) (config.Hub, error) {
	cfg, err := loadHubOrEmpty(path)
	if err != nil {
		return config.Hub{}, err
	}
	for _, have := range cfg.Hosts {
		if have.ID == string(host.ID) {
			return config.Hub{}, fmt.Errorf("%s already names a Host %q", path, host.ID)
		}
		haveAddress, _ := normalizeRegistrationAddress(have.Address)
		newAddress, _ := normalizeRegistrationAddress(host.Address)
		if haveAddress == newAddress && have.DaemonPort == host.DaemonPort {
			return config.Hub{}, fmt.Errorf("%s already reaches %s at %s", path, have.ID, host.Address)
		}
	}
	cfg.Hosts = append(cfg.Hosts, config.HostProfile{ID: string(host.ID), Address: host.Address, User: host.User, KeyPath: host.KeyPath, KnownHosts: host.KnownHosts, DaemonPort: host.DaemonPort})
	return cfg, nil
}

func withPort(address string) string {
	if _, _, err := net.SplitHostPort(address); err == nil {
		return address
	}
	return net.JoinHostPort(strings.TrimSuffix(strings.TrimPrefix(address, "["), "]"), defaultSSHPort)
}

func hubSSHDir() (string, error) {
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		return filepath.Join(local, "Dispatch", "ssh"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("this user has no home directory to keep the Hub's key in: %w", err)
	}
	return filepath.Join(home, ".local", "state", "dispatch", "ssh"), nil
}
