package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/VictorJohnOkoh/Dispatch/internal/config"
	"github.com/VictorJohnOkoh/Dispatch/internal/hub"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"golang.org/x/term"
)

// dispatch host add is the third thing this binary does, and it is a command
// rather than a role: it runs, it registers one Host, and it ends.
//
// Configuration enters and leaves here. The Hub module registers the Host and
// never reads or writes a file; this file reads hub.json, refuses a Host that is
// already in it, and commits the new one.

// The two defaults ADR 0013 names, and the address a Hub listens on when this
// command makes hub.json for the first time.
const (
	defaultSSHPort    = "22"
	defaultDaemonPort = 7717
	defaultHubListen  = "127.0.0.1:7700"
)

const hostUsage = "dispatch host add -id <id> -address <host[:port]> -user <account> [-daemon-port n] [-config hub.json]"

func runHost(ctx context.Context, args []string, out io.Writer) int {
	if len(args) == 0 || args[0] != "add" {
		fmt.Fprintln(out, "usage: "+hostUsage)
		return 2
	}
	flags := flag.NewFlagSet("host add", flag.ContinueOnError)
	flags.SetOutput(out)
	id := flags.String("id", "", "this Host's name on the wire")
	address := flags.String("address", "", "this Host's sshd endpoint, host[:port]")
	user := flags.String("user", "", "the local Windows account the Hub logs in as")
	daemonPort := flags.Int("daemon-port", defaultDaemonPort, "the Daemon's loopback port on this Host")
	path := flags.String("config", "hub.json", "the Hub's configuration file")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if err := addHost(ctx, *path, *id, *address, *user, *daemonPort, out); err != nil {
		fmt.Fprintf(out, "dispatch: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "%s is registered in %s. Restart the Hub to reach it.\n", *id, *path)
	return 0
}

// addHost checks what the module may not see — the Host id's shape and the file
// this Host is going into — and then runs the registration.
func addHost(ctx context.Context, path, id, address, user string, daemonPort int, out io.Writer) error {
	if id == "" || address == "" || user == "" {
		return errors.New("an id, an address and an account are needed\nusage: " + hostUsage)
	}
	if !protocol.ValidHostID(id) {
		return fmt.Errorf("%q is not a Host id", id)
	}
	dir, err := hubSSHDir()
	if err != nil {
		return err
	}
	req := hub.Registration{
		ID: hub.HostID(id), Address: withPort(address), User: user, DaemonPort: daemonPort, Dir: dir,
	}
	// The file is read now as well as at the commit, because registration asks the
	// user for a password and a duplicate Host is worth finding before that.
	if _, err := hubWith(path, hub.Registered{
		ID: req.ID, Address: req.Address, User: user, DaemonPort: daemonPort,
	}); err != nil {
		return err
	}
	return hub.RegisterHost(ctx, req, &console{out: out, in: bufio.NewReader(os.Stdin), user: user},
		func(host hub.Registered) error { return commitHost(path, host) })
}

// commitHost is the callback ADR 0013 gives the module. It reads hub.json again
// rather than holding the copy it read a minute ago, and writes it whole.
func commitHost(path string, host hub.Registered) error {
	cfg, err := hubWith(path, host)
	if err != nil {
		return err
	}
	return config.SaveHub(path, cfg)
}

// hubWith is hub.json with this Host added. A file that is not there is a first
// Host, and the Hub it makes listens where ADR 0013 says. A Host this file
// already holds is refused: registration adds a Host and never replaces one.
func hubWith(path string, host hub.Registered) (config.Hub, error) {
	cfg := config.Hub{Listen: defaultHubListen}
	if _, err := os.Stat(path); err == nil {
		if cfg, err = config.LoadHub(path); err != nil {
			return config.Hub{}, err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return config.Hub{}, err
	}
	for _, have := range cfg.Hosts {
		if have.ID == string(host.ID) {
			return config.Hub{}, fmt.Errorf("%s already names a Host %q", path, host.ID)
		}
		if have.Address == host.Address && have.DaemonPort == host.DaemonPort {
			return config.Hub{}, fmt.Errorf("%s already reaches %s at %s", path, have.ID, host.Address)
		}
	}
	cfg.Hosts = append(cfg.Hosts, config.HostProfile{
		ID: string(host.ID), Address: host.Address, User: host.User,
		KeyPath: host.KeyPath, KnownHosts: host.KnownHosts, DaemonPort: host.DaemonPort,
	})
	return cfg, nil
}

// withPort is ADR 0013's rule that an address with no port is port 22. The Hub
// dials the string as it is written, so the port is added here and not there.
func withPort(address string) string {
	if _, _, err := net.SplitHostPort(address); err == nil {
		return address
	}
	return net.JoinHostPort(address, defaultSSHPort)
}

// hubSSHDir holds the Hub's managed identity and the Hosts it trusts. ADR 0013
// names %LOCALAPPDATA%, which is not what os.UserConfigDir answers on Windows,
// so the variable is read directly and every other system falls back to its
// state directory.
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

// console is the user at the keyboard, and the only place a password is read.
type console struct {
	out  io.Writer
	in   *bufio.Reader
	user string
}

// ConfirmHostKey shows the fingerprint before anything secret is sent. This is
// trust on first use: a later connection refuses a changed Host key, and this
// first look is the only proof there is that this is the right machine.
func (c *console) ConfirmHostKey(fingerprint string) (bool, error) {
	fmt.Fprintf(c.out, "This Host's key fingerprint is %s\n", fingerprint)
	fmt.Fprint(c.out, "Continue only on a network you trust. Type yes to continue: ")
	answer, err := c.in.ReadString('\n')
	if err != nil && answer == "" {
		return false, err
	}
	return strings.TrimSpace(answer) == "yes", nil
}

// Password is read once, without an echo. It goes to one login, stays in memory
// and is written nowhere.
func (c *console) Password() (string, error) {
	// A password is typed and not piped. Saying so here is the whole answer: a
	// redirected handle has no console mode to turn the echo off with, and reading
	// one anyway would put the password in whatever file it came from.
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("a password is typed at a terminal, and this input is not one")
	}
	fmt.Fprintf(c.out, "Password for %s: ", c.user)
	body, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(c.out)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
