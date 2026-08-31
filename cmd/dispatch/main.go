// Command dispatch runs either role of the orchestrator from one binary: the
// Daemon that owns one Host's Sessions, or the Hub that connects to every
// configured Host.
//
// Configuration enters here and goes no deeper. This file reads the one file its
// role uses, checks what the config package cannot see on its own, and builds
// the plain values the packages below take.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/VictorJohnOkoh/Dispatch/internal/config"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"github.com/VictorJohnOkoh/Dispatch/internal/workspace"
)

const usage = "usage: dispatch <daemon|hub> [-config path]"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run selects the role and returns the process exit code. main is the only
// caller of os.Exit, so a role can defer its shutdown.
func run(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, usage)
		return 2
	}

	role := args[0]
	var path string
	var start func(string, io.Writer) error
	switch role {
	case "daemon":
		path, start = "daemon.json", startDaemon
	case "hub":
		path, start = "hub.json", startHub
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

	if err := start(path, out); err != nil {
		fmt.Fprintf(errOut, "dispatch: %v\n", err)
		return 1
	}
	return 0
}

// startDaemon loads this Host's configuration and resolves the values that
// touch the world. Resolving the Workspace Root at start is deliberate: a Root
// that is not there is better found now than at the first Session.
func startDaemon(path string, out io.Writer) error {
	cfg, err := config.LoadDaemon(path)
	if err != nil {
		return err
	}
	root, err := workspace.NewRoot(cfg.WorkspaceRoot)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "dispatch: role daemon, %d Vendors, %d Harnesses, Workspace Root %s\n",
		len(cfg.Vendors), len(cfg.Harnesses), root)
	return nil
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
