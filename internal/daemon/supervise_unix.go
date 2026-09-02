//go:build !windows

package daemon

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
)

// group is the Harness's own process group. Setpgid puts the child at the head of
// one before its first instruction runs, so the kill reaches a grandchild the
// Harness spawned, which is where the Hermes hang lived.
type group struct{}

func newGroup() (*group, error) { return &group{}, nil }

func (g *group) prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// claim has nothing to do. The group existed before the process did.
func (g *group) claim(*exec.Cmd) error { return nil }

// kill takes the group, not the process. A negative pid names the group whose
// leader is that pid, and the leader is the Harness.
func (g *group) kill(cmd *exec.Cmd) error {
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("daemon: the Harness process group would not die: %w", err)
	}
	return nil
}

func (g *group) close() error { return nil }
