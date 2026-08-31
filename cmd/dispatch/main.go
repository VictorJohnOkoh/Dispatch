// Command dispatch runs either role of the orchestrator from one binary: the
// Daemon that owns one Host's Sessions, or the Hub that connects to every
// configured Host.
//
// Configuration enters here and goes no deeper. This file reads the one file its
// role uses, checks what the config package cannot see on its own, and builds
// the plain values the packages below take.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"

	"github.com/VictorJohnOkoh/Dispatch/internal/config"
	"github.com/VictorJohnOkoh/Dispatch/internal/daemon"
	"github.com/VictorJohnOkoh/Dispatch/internal/eventlog"
	"github.com/VictorJohnOkoh/Dispatch/internal/harness"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"github.com/VictorJohnOkoh/Dispatch/internal/vendors"
	"github.com/VictorJohnOkoh/Dispatch/internal/workspace"
)

const usage = "usage: dispatch <daemon|hub> [-config path]"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

// run selects the role and returns the process exit code. main is the only
// caller of os.Exit, so a role can defer its shutdown. It returns when ctx is
// done, which is what an interrupt cancels.
func run(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, usage)
		return 2
	}

	// The operational log is stderr. It holds what the Event log cannot: a process,
	// a socket, and a decision that produced no Session.
	log := slog.New(slog.NewTextHandler(errOut, nil))

	role := args[0]
	var path string
	var start func(context.Context, string) error
	switch role {
	case "daemon":
		path = "daemon.json"
		start = func(ctx context.Context, p string) error { return startDaemon(ctx, p, log) }
	case "hub":
		path = "hub.json"
		start = func(_ context.Context, p string) error { return startHub(p, out) }
	default:
		fmt.Fprintf(errOut, "dispatch: unknown role %q\n%s\n", role, usage)
		return 2
	}

	flags := flag.NewFlagSet(role, flag.ContinueOnError)
	flags.SetOutput(errOut)
	flags.StringVar(&path, "config", path, "the configuration file this role reads")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}

	if err := start(ctx, path); err != nil {
		fmt.Fprintf(errOut, "dispatch: %v\n", err)
		return 1
	}
	return 0
}

// startDaemon loads this Host's configuration and resolves the values that
// touch the world. Resolving the Workspace Root at start is deliberate: a Root
// that is not there is better found now than at the first Session.
func startDaemon(ctx context.Context, path string, log *slog.Logger) error {
	cfg, err := config.LoadDaemon(path)
	if err != nil {
		return err
	}
	root, err := workspace.NewRoot(cfg.WorkspaceRoot)
	if err != nil {
		return err
	}
	adapters := make([]vendors.Adapter, len(cfg.Vendors))
	for i, profile := range cfg.Vendors {
		if adapters[i], err = newVendor(profile.Endpoint); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	harnesses, err := newHarnesses(cfg.Harnesses, log)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	events, err := eventlog.Open(cfg.LogPath)
	if err != nil {
		return err
	}
	defer events.Close()

	log.Info("dispatch starting", "role", "daemon", "vendors", len(adapters),
		"harnesses", len(harnesses), "workspaceRoot", root, "logPath", cfg.LogPath)
	return daemon.New(log, events, root, adapters, harnesses).Serve(ctx, cfg.Listen)
}

// newHarnesses builds one Adapter per named Harness this build knows. A Harness
// with no Adapter yet is a milestone that has not landed, so it is a warning and
// not a startup error: the Daemon still serves the ones it does have. A file that
// names none of them is the error, because that Daemon can start no Session.
func newHarnesses(profiles []config.HarnessProfile, log *slog.Logger) ([]daemon.Harness, error) {
	var out []daemon.Harness
	for _, profile := range profiles {
		switch profile.Name {
		case "passthrough":
			out = append(out, daemon.Harness{Name: profile.Name, Adapter: harness.NewPassthrough(nil)})
		default:
			log.Warn("this Harness has no Adapter yet, and no Session may name it", "harness", profile.Name)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no Harness in this file has an Adapter yet")
	}
	return out, nil
}

// newVendor is the one place a Vendor Kind is read. Ollama is the Adapter this
// milestone has, and a configured Vendor with no Adapter is a startup error rather
// than a Vendor that quietly never appears.
func newVendor(endpoint vendors.Endpoint) (vendors.Adapter, error) {
	switch endpoint.Kind {
	case vendors.Ollama:
		return vendors.NewOllama(endpoint.Base, nil), nil
	default:
		return nil, fmt.Errorf("Vendor kind %s has no Adapter yet", endpoint.Kind)
	}
}

// startHub loads the Host list. The Host id rule lives in protocol, which config
// may not import, so the check is here.
func startHub(path string, out io.Writer) error {
	cfg, err := config.LoadHub(path)
	if err != nil {
		return err
	}
	for _, host := range cfg.Hosts {
		if !protocol.ValidHostID(host.ID) {
			return fmt.Errorf("%s: %q is not a Host id", path, host.ID)
		}
	}
	fmt.Fprintf(out, "dispatch: role hub, %d Hosts\n", len(cfg.Hosts))
	return nil
}
