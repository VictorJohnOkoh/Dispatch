package daemon

import (
	"fmt"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// group is a Job Object, because Windows has no process group a kill can take.
// CREATE_NEW_PROCESS_GROUP changes only where a Ctrl+Break is delivered, and
// TerminateProcess takes one handle, so descendants survive it as orphans.
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE makes closing the handle take the whole tree,
// including a process the Harness spawns later, and OpenCode resolves to a package
// binary that spawns its own child.
//
// This is process-tree lifetime and not sandboxing. Nothing here limits what the
// Harness may do.
type group struct {
	job    windows.Handle
	closed sync.Once
}

func newGroup() (*group, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("daemon: the Harness gets no Job Object: %w", err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("daemon: the Job Object would not take kill-on-close: %w", err)
	}
	return &group{job: job}, nil
}

// prepare has nothing to set. The job holds the process, not the spawn.
func (g *group) prepare(*exec.Cmd) {}

// claim puts the started process in the job. It runs after Start because Go's
// exec offers no hook between the process being created and its first instruction.
// A child spawned inside that gap escapes the job, and the gap is microseconds
// against a Harness that takes hundreds of milliseconds to say hello.
func (g *group) claim(cmd *exec.Cmd) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return fmt.Errorf("daemon: the Harness process would not open: %w", err)
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(g.job, h); err != nil {
		return fmt.Errorf("daemon: the Harness would not join its Job Object: %w", err)
	}
	return nil
}

// kill closes the last handle to the job, and kill-on-close takes the tree with it.
func (g *group) kill(*exec.Cmd) error { return g.close() }

func (g *group) close() error {
	var err error
	g.closed.Do(func() { err = windows.CloseHandle(g.job) })
	if err != nil {
		return fmt.Errorf("daemon: the Harness's Job Object would not close: %w", err)
	}
	return nil
}
