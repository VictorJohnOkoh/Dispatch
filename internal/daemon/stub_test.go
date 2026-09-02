package daemon

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/harness"
)

// The stub Harness is this test binary re-executing itself, which is the standard
// library's own technique for a test that needs a real process. It is not a
// checked-in binary and not a fake Run, so the ladder, the kill after the fixed
// wait, the stderr drain and the unprompted exit all run against a real process
// with real children, behaving exactly as badly as the test tells it to.

const (
	stubRole  = "DISPATCH_STUB"       // which behaviour this process runs
	stubName  = "DISPATCH_STUB_NAME"  // what it calls itself when it reports
	stubTower = "DISPATCH_STUB_TOWER" // where it reports that it is alive
)

// lastWords is what the stub that leaves on its own writes to stderr first.
const lastWords = "the Harness fell over"

// partingWords is what the stub says on stdout once stdin has closed. Those bytes
// travel while the Session is already ending, which is the moment a transcript
// closed too early loses them.
const partingWords = "the Harness said one last thing"

// stderrFlood is more than any pipe buffer on either platform, so a stub that
// writes it finishes only if somebody is draining the other end.
const stderrFlood = 1 << 20

// TestHarnessHelper is the stub. Under go test it skips; it runs only when
// stubLaunch re-executes this binary with DISPATCH_STUB set.
func TestHarnessHelper(t *testing.T) {
	role := os.Getenv(stubRole)
	if role == "" {
		t.Skip("this is the stub Harness, and it runs only when it is spawned as one")
	}
	stub(role)
}

// stub is one stub Harness's whole life. Every path ends in os.Exit, so the test
// framework never prints its own output onto the Harness's stdout.
func stub(role string) {
	report()
	switch role {
	case "polite":
		// Reads stdin to EOF and leaves, which is the step both real Harnesses answer.
		io.Copy(io.Discard, os.Stdin)
	case "deaf":
		// Ignores stdin, so only the kill ends it.
		time.Sleep(time.Hour)
	case "parent":
		// Spawns a child of its own well after it started, and then ignores stdin.
		// On Windows that child is created after the Job Object was assigned, which
		// is the case a naive kill leaves running.
		time.Sleep(50 * time.Millisecond)
		child := exec.Command(os.Args[0], "-test.run=TestHarnessHelper")
		child.Env = append(os.Environ(), stubRole+"=deaf", stubName+"=child")
		if err := child.Start(); err != nil {
			os.Exit(4)
		}
		time.Sleep(time.Hour)
	case "noisy":
		// Fills stderr, then says on stdout that it was not blocked doing it.
		os.Stderr.Write(bytes.Repeat([]byte("e"), stderrFlood))
		fmt.Println("drained")
		io.Copy(io.Discard, os.Stdin)
	case "parting":
		// Says one last thing on stdout after stdin closes, which is the ladder's
		// step 4, and then leaves.
		io.Copy(io.Discard, os.Stdin)
		fmt.Println(partingWords)
	case "quit":
		// Leaves on its own, which no Harness in RPC or ACP mode does, and says why
		// on stderr, which is the only evidence there ever is.
		fmt.Fprintln(os.Stderr, lastWords)
		os.Exit(3)
	}
	os.Exit(0)
}

// link keeps the stub's report open for the life of the process. A net.Conn that
// nothing refers to is closed by its finaliser, and this conn closing is what the
// test reads as death.
var link net.Conn

// report tells the tower this process is alive and holds the connection.
func report() {
	addr := os.Getenv(stubTower)
	if addr == "" {
		return
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		os.Exit(5)
	}
	name := os.Getenv(stubName)
	if name == "" {
		name = "harness"
	}
	fmt.Fprintln(conn, name)
	link = conn
}

// tower is how a test sees a stub process from outside. Every stub dials it and
// holds the connection for as long as it lives, so an EOF on that connection is
// that process being gone. Liveness is asked of the process itself because
// os.FindProcess answers yes on Windows for a pid that has already exited.
type tower struct {
	ln net.Listener

	mu    sync.Mutex
	conns map[string]net.Conn
}

func newTower(t *testing.T) *tower {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("tower: %v", err)
	}
	w := &tower{ln: ln, conns: map[string]net.Conn{}}
	go w.accept()
	t.Cleanup(func() { ln.Close() })
	return w
}

func (w *tower) accept() {
	for {
		conn, err := w.ln.Accept()
		if err != nil {
			return
		}
		go func() {
			name, err := bufio.NewReader(conn).ReadString('\n')
			if err != nil {
				return
			}
			w.mu.Lock()
			defer w.mu.Unlock()
			w.conns[name[:len(name)-1]] = conn
		}()
	}
}

func (w *tower) addr() string { return w.ln.Addr().String() }

// reported waits until a stub calling itself that name has said it is alive.
func (w *tower) reported(t *testing.T, name string) net.Conn {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		w.mu.Lock()
		conn := w.conns[name]
		w.mu.Unlock()
		if conn != nil {
			return conn
		}
		if time.Now().After(deadline) {
			t.Fatalf("no stub called %q ever reported", name)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// gone reports whether that stub's connection has closed within the time given,
// which is that process being gone. Only a read that timed out is a process still
// there: a killed process drops the connection, and whether that arrives as an EOF
// or as a reset is the platform's business.
func (w *tower) gone(t *testing.T, name string, within time.Duration) bool {
	t.Helper()
	conn := w.reported(t, name)
	conn.SetReadDeadline(time.Now().Add(within))
	_, err := conn.Read(make([]byte, 1))
	return err != nil && !os.IsTimeout(err)
}

// stubLaunch is the exe and the arguments that start one stub behaviour. The exe
// is this test binary, which is what makes the stub a real process.
func stubLaunch(t *testing.T, w *tower, role string) (string, harness.Launch) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	return exe, harness.Launch{
		Args: []string{"-test.run=TestHarnessHelper"},
		Env:  []string{stubRole + "=" + role, stubTower + "=" + w.addr()},
	}
}
