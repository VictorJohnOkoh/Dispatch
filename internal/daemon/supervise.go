package daemon

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/harness"
)

// The Daemon owns the Harness process, and this file is the whole of that
// ownership: the spawn, the stdin it holds, the stderr it drains and the last
// three steps of the shutdown ladder. What it cannot say portably lives in
// supervise_unix.go and supervise_windows.go, because killing a process tree is
// the one thing in this design with no portable primitive.

// stopWait is the ladder's step 5, the fixed short wait between the closed stdin
// and the kill. It is the only timer in the design, and the rule that keeps it
// honest is that a timer may end a Session and may never diagnose one.
const stopWait = 2 * time.Second

// stderrKeep is how much of a dead Harness's stderr the Error Event carries. The
// failure is at the end of the output, so it is the tail that is kept.
const stderrKeep = 8 << 10

// spawner is the Spawn one Session's Adapter is given. The Adapter chooses the
// arguments, the Host config chose the executable, and the Daemon keeps what comes
// back: it holds stdin, drains stderr, watches for an exit nobody asked for and
// kills the group at the end.
//
// The Session's context is not passed on. Nothing about this spawn ends when it
// does, because ending the process is the ladder's job and the ladder is called
// by name.
func (d *Daemon) spawner(s *Session, h *Harness) harness.Spawner {
	return func(_ context.Context, l harness.Launch) (harness.Pipes, error) {
		if h.Exe == "" {
			return harness.Pipes{}, fmt.Errorf("daemon: the Harness %q names no executable to spawn", h.Name)
		}
		// The tail is where the drain writes. It is what an Error Event quotes when
		// the Harness dies, and the capped transcript will take it over.
		kept := &stderrTail{}
		p, pipes, err := spawn(h.Exe, s.dir, l, kept)
		if err != nil {
			return harness.Pipes{}, err
		}
		d.sessions.setProcess(s, p)
		go d.watchExit(s, p, kept)
		return pipes, nil
	}
}

// watchExit notices a Harness that left on its own. A Harness in RPC or ACP mode
// does not end by itself, so any exit the Daemon did not ask for is a failure
// whatever the exit code, and the Session ends failed rather than staying in a
// registry that thinks it is live.
//
// A stop got here first when it took the end, and endFailed writes nothing then.
func (d *Daemon) watchExit(s *Session, p *harnessProcess, kept *stderrTail) {
	<-p.exited()
	d.endFailed(s, event.ErrHarnessFailed, p.failure(kept, d.stopWait))
}

// harnessProcess is one Harness process as the Daemon holds it.
type harnessProcess struct {
	cmd   *exec.Cmd
	in    io.WriteCloser
	group *group

	reaped  chan struct{} // closed once Wait has returned
	drained chan struct{} // closed once stderr has reached EOF
	status  error         // what Wait said, read only after reaped is closed

	closeIn sync.Once
	stopped sync.Once
	killed  error // the group that would not die, read only after stopped has run
}

// spawn starts the Harness the Host config named and returns the two pipes an
// Adapter may see. The path is never a bare name for the PATH to resolve, because
// an npm shim on the PATH answers a shell and nothing else.
//
// The Session's context is not what kills this. exec.CommandContext kills one
// process, and step 6 of the ladder kills the group.
func spawn(exe, dir string, l harness.Launch, stderr io.Writer) (*harnessProcess, harness.Pipes, error) {
	if !filepath.IsAbs(exe) {
		return nil, harness.Pipes{}, fmt.Errorf("daemon: the Harness path %q is not absolute", exe)
	}

	// Each pipe is made here rather than by StdinPipe and its two siblings, because
	// those are closed by Wait and the Adapter is still reading stdout when the
	// Daemon reaps.
	in, out, errs, err := newPipes()
	if err != nil {
		return nil, harness.Pipes{}, err
	}
	// Every path that fails from here closes all six ends. A handle nobody holds is
	// a handle nobody closes, and closing one twice is harmless.
	shut := func() { in.close(); out.close(); errs.close() }

	cmd := exec.Command(exe, l.Args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), l.Env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in.read, out.write, errs.write

	g, err := newGroup()
	if err != nil {
		shut()
		return nil, harness.Pipes{}, err
	}
	g.prepare(cmd)

	if err := cmd.Start(); err != nil {
		shut()
		g.close()
		return nil, harness.Pipes{}, fmt.Errorf("daemon: the Harness %s would not start: %w", exe, err)
	}
	// The child holds its own ends now, so the Daemon drops the copies it kept. An
	// end left open here is an EOF that never arrives.
	in.read.Close()
	out.write.Close()
	errs.write.Close()

	if err := g.claim(cmd); err != nil {
		// A process the Daemon cannot kill whole is the failure this design exists to
		// prevent, so it is killed as far as it can be and the start fails.
		cmd.Process.Kill()
		cmd.Wait()
		g.close()
		shut()
		return nil, harness.Pipes{}, err
	}

	p := &harnessProcess{
		cmd: cmd, in: in.write, group: g,
		reaped: make(chan struct{}), drained: make(chan struct{}),
	}
	go p.drain(errs.read, stderr)
	go p.reap()
	return p, harness.Pipes{In: in.write, Out: out.read}, nil
}

// drain reads stderr for the life of the process and writes it where the Daemon
// keeps it. Draining is not optional, since a full pipe stops the child. Parsing
// it is forbidden, and Hermes is the reason: its phantom denial produced no stderr
// at all, so stderr was silent in exactly the case a supervisor would want it to
// speak.
func (p *harnessProcess) drain(from *os.File, into io.Writer) {
	defer close(p.drained)
	defer from.Close()
	if into == nil {
		into = io.Discard
	}
	io.Copy(into, from)
}

func (p *harnessProcess) reap() {
	defer close(p.reaped)
	p.status = p.cmd.Wait()
}

func (p *harnessProcess) exited() <-chan struct{} { return p.reaped }

// closeStdin is the ladder's step 4. It is the step both Harnesses answer, and the
// Hermes hang went from 118.83s to 1.18s when stdin was closed before the tool ran.
func (p *harnessProcess) closeStdin() {
	p.closeIn.Do(func() { p.in.Close() })
}

// stop is the ladder's steps 4 to 6, and it always finishes, which is what lets
// stopping be an operation rather than a state. A Harness that answered the closed
// stdin is gone before the wait ends; one that did not is killed with its tree.
//
// It returns the group that would not die, which a caller writes to the
// operational log, because a descendant that outlived the kill is a thing a human
// has to go and find.
func (p *harnessProcess) stop(wait time.Duration) error {
	p.stopped.Do(func() {
		p.closeStdin()
		select {
		case <-p.reaped:
		case <-time.After(wait):
		}
		// The kill runs even when the Harness already left. On Unix its children can
		// still be in the group, and on Windows the job is a handle that has to be
		// closed whatever happened to the process inside it.
		if p.killed = p.group.kill(p.cmd); p.killed != nil {
			// The group would not take it, so the one process is taken instead.
			p.cmd.Process.Kill()
		}
		<-p.reaped
		// A descendant that outlived the kill can still hold stderr open, and a stop
		// that waits for an EOF that is not coming is a stop that does not finish.
		select {
		case <-p.drained:
		case <-time.After(wait):
		}
	})
	return p.killed
}

// failure is what an unprompted exit is written into the log as. A Harness in RPC
// or ACP mode does not end by itself, so exit code 0 is not evidence of a clean
// finish and the tail of stderr is the only evidence there is.
//
// The drain is waited on and not only the exit. Wait returns as soon as the
// process is gone, and the words a dying Harness wrote are still in the pipe at
// that moment.
func (p *harnessProcess) failure(kept *stderrTail, wait time.Duration) string {
	<-p.reaped
	select {
	case <-p.drained:
	case <-time.After(wait):
	}
	stderr := kept.String()
	if p.status == nil {
		return "the Harness exited on its own, which is a failure whatever the exit code" + tail(stderr)
	}
	return fmt.Sprintf("the Harness exited on its own: %v%s", p.status, tail(stderr))
}

func tail(stderr string) string {
	if stderr == "" {
		return ""
	}
	return ". Its stderr ended: " + stderr
}

// pipe is one direction of one pipe, with both ends still held.
type pipe struct{ read, write *os.File }

func (p pipe) close() {
	p.read.Close()
	p.write.Close()
}

// newPipes makes the three a spawn needs. os.Pipe fails only when the process has no
// handles left, and then the ones already made are closed rather than leaked.
func newPipes() (in, out, errs pipe, err error) {
	if in, err = newPipe(); err != nil {
		return
	}
	if out, err = newPipe(); err != nil {
		in.close()
		return
	}
	if errs, err = newPipe(); err != nil {
		in.close()
		out.close()
	}
	return
}

func newPipe() (pipe, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return pipe{}, fmt.Errorf("daemon: no pipe for the Harness: %w", err)
	}
	return pipe{read: r, write: w}, nil
}

// stderrTail keeps the last stderrKeep bytes the Harness wrote. It is where the
// drain writes until the capped transcript lands, and it never grows, so a Harness
// that writes forever costs one buffer.
type stderrTail struct {
	mu  sync.Mutex
	buf []byte
}

func (s *stderrTail) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, b...)
	if len(s.buf) > stderrKeep {
		s.buf = s.buf[len(s.buf)-stderrKeep:]
	}
	return len(b), nil
}

func (s *stderrTail) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.buf)
}
