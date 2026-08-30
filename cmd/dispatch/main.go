// Command dispatch runs either role of the orchestrator from one binary: the
// Daemon that owns one Host's Sessions, or the Hub that connects to every
// configured Host.
package main

import (
	"fmt"
	"os"
)

const usage = "usage: dispatch <daemon|hub>"

func main() {
	os.Exit(run(os.Args[1:]))
}

// run selects the role and returns the process exit code. main is the only
// caller of os.Exit, so a role can defer its shutdown.
func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}

	switch role := args[0]; role {
	case "daemon":
		fmt.Println("dispatch: role daemon")
		return 0
	case "hub":
		fmt.Println("dispatch: role hub")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "dispatch: unknown role %q\n%s\n", role, usage)
		return 2
	}
}
