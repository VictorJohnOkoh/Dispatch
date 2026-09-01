package daemon

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/harness"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// spawnStub starts one stub Harness the way the Daemon starts a real one.
func spawnStub(t *testing.T, w *tower, role string, stderr io.Writer) (*harnessProcess, harness.Pipes) {
	t.Helper()
	exe, l := stubLaunch(t, w, role)
	p, pipes, err := spawn(exe, t.TempDir(), l, stderr)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	t.Cleanup(func() { p.stop(time.Second) })
	return p, pipes
}

// The rig itself: a real OS process that spawns a real child of its own. Every
// test below is only worth as much as this one is.
func TestTheStubIsARealProcessWithRealChildren(t *testing.T) {
	w := newTower(t)
	spawnStub(t, w, "parent", nil)

	w.reported(t, "harness")
	w.reported(t, "child")
}

// Step 4 on a Harness that answers it. Closing stdin is the step both real
// Harnesses answer, and a Harness that answers it never reaches the kill.
func TestClosedStdinEndsAHarnessThatAnswersIt(t *testing.T) {
	w := newTower(t)
	p, _ := spawnStub(t, w, "polite", nil)

	p.closeStdin()
	select {
	case <-p.exited():
	case <-time.After(10 * time.Second):
		t.Fatal("the Harness did not leave when stdin closed")
	}
	if p.status != nil {
		t.Errorf("the Harness left with %v, and it was asked politely", p.status)
	}
}

// Steps 5 and 6 on a Harness that ignores the polite ones. The wait is fixed and
// short, and what follows it is a kill rather than another wait.
func TestADeafHarnessIsKilledAfterTheFixedWait(t *testing.T) {
	w := newTower(t)
	p, _ := spawnStub(t, w, "deaf", nil)
	w.reported(t, "harness")

	const wait = 200 * time.Millisecond
	began := time.Now()
	if err := p.stop(wait); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if waited := time.Since(began); waited < wait {
		t.Errorf("the kill came after %v, and the fixed wait is %v", waited, wait)
	}
	if !w.gone(t, "harness", 5*time.Second) {
		t.Error("the Harness is still alive after a stop")
	}
}

// Behaviour 6, and the part a naive kill gets wrong. The child is spawned well
// after the Harness started, so on Windows it joined the Job Object after the
// Job Object was assigned, and it still dies with it.
func TestTheWholeTreeGoesWithTheHarness(t *testing.T) {
	w := newTower(t)
	p, _ := spawnStub(t, w, "parent", nil)
	w.reported(t, "harness")
	w.reported(t, "child")

	if err := p.stop(100 * time.Millisecond); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !w.gone(t, "harness", 5*time.Second) {
		t.Error("the Harness is still alive after a stop")
	}
	if !w.gone(t, "child", 5*time.Second) {
		t.Error("the Harness's child outlived the stop, which is the orphan this design exists to prevent")
	}
}

// Draining is not optional: a full pipe stops the child. The stub writes more than
// any pipe buffer holds and only then says on stdout that it was not blocked.
func TestStderrIsDrainedAndDoesNotBlockTheHarness(t *testing.T) {
	w := newTower(t)
	kept := &lines{}
	p, pipes := spawnStub(t, w, "noisy", kept)

	said := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(pipes.Out).ReadString('\n')
		said <- line
	}()
	select {
	case line := <-said:
		if line != "drained\n" {
			t.Fatalf("the Harness said %q", line)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the Harness never got past its own stderr, so nothing was draining it")
	}

	p.stop(time.Second)
	if n := len(kept.String()); n != stderrFlood {
		t.Errorf("%d bytes of stderr were kept, and the Harness wrote %d", n, stderrFlood)
	}
}

// failure is the message of the first Error Event in the log.
func (h *host) failure(t *testing.T) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+h.logPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer db.Close()

	var payload []byte
	if err := db.QueryRow(`SELECT payload FROM events WHERE kind = 'Error' ORDER BY seq LIMIT 1`).Scan(&payload); err != nil {
		t.Fatalf("no Error Event: %v", err)
	}
	var body struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("payload: %v", err)
	}
	return body.Message
}

// spawning is a Harness Adapter that starts a real process and does nothing else
// with it. The Adapters that speak a protocol land in later milestones; what this
// exercises is the half the Daemon owns, which is the process.
type spawning struct {
	launch harness.Launch

	mu     sync.Mutex
	closed int
}

func (a *spawning) Capabilities() harness.Capabilities {
	return harness.Capabilities{Tools: true}
}

func (a *spawning) Start(ctx context.Context, spec harness.SessionSpec, _ harness.Sink) (harness.Run, error) {
	if _, err := spec.Spawn(ctx, a.launch); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *spawning) Prompt(context.Context, string) error { return nil }
func (a *spawning) Interrupt(context.Context) error      { return nil }

func (a *spawning) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed++
	return nil
}

func (a *spawning) closes() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.closed
}

// stubHost is a Daemon serving one Harness that spawns the stub in that behaviour.
func stubHost(t *testing.T, w *tower, role string) (*host, *spawning) {
	t.Helper()
	exe, l := stubLaunch(t, w, role)
	a := &spawning{launch: l}
	h := newHost(t, Harness{Name: "stub", Exe: exe, Adapter: a})
	h.stopWait = 100 * time.Millisecond
	return h, a
}

const stubStart = `{"harness":"stub","model":"qwen3:8b"}`

// The whole ladder, in order, against a Harness that ignores every polite step.
// Steps 1, 2 and 7 are Events; step 3 is the Adapter's goodbye; steps 4 to 6 are
// the process, and this one only ends at 6.
func TestTheShutdownLadderRunsInOrder(t *testing.T) {
	w := newTower(t)
	h, a := stubHost(t, w, "deaf")

	id := h.started(t, h.post(t, "/v1/sessions", stubStart)).Session
	h.waitState(t, id, "Idle")
	w.reported(t, "harness")

	// Two Tool Calls, one of them holding a question. Nothing on this Host asks yet,
	// so they are written by hand rather than by a Harness that cannot.
	s, _, _ := h.sessions.find(id)
	h.write(s, event.KindToolCallRequested, &event.ToolCallRequested{ToolCallID: "c1", Name: "bash"})
	h.write(s, event.KindToolCallRequested, &event.ToolCallRequested{ToolCallID: "c2", Name: "bash"})
	h.write(s, event.KindApprovalRequested, &event.ApprovalRequested{ToolCallID: "c1", Title: "run it"})

	if w := h.post(t, "/v1/sessions/"+string(id)+"/stop", ""); w.Code != protocol.StatusAccepted {
		t.Fatalf("stop: status %d: %s", w.Code, w.Body.String())
	}

	kinds := h.kinds(t)
	want := []string{"ApprovalDecided", "ToolCallEnded", "ToolCallEnded", "SessionEnded"}
	if got := kinds[len(kinds)-len(want):]; !slices.Equal(got, want) {
		t.Errorf("the ladder wrote %v, want it to end %v", kinds, want)
	}
	if a.closes() != 1 {
		t.Errorf("Run.Close ran %d times, and step 3 runs once", a.closes())
	}
	if !w.gone(t, "harness", 5*time.Second) {
		t.Error("the Harness outlived the ladder")
	}
}

// A Harness in RPC or ACP mode does not end by itself, so an exit nobody asked for
// is a failure whatever the exit code, and the registry must not go on thinking
// the Session is live.
func TestAHarnessThatLeavesOnItsOwnEndsTheSession(t *testing.T) {
	w := newTower(t)
	h, _ := stubHost(t, w, "quit")

	id := h.started(t, h.post(t, "/v1/sessions", stubStart)).Session
	view := h.waitState(t, id, "Ended")
	if view.EndReason != event.EndFailed {
		t.Errorf("the Session ended %q, want %q", view.EndReason, event.EndFailed)
	}
	// The Error carries the tail of the process's stderr, which is where the reason
	// for an exit nobody asked for is written.
	if said := h.failure(t); !strings.Contains(said, lastWords) {
		t.Errorf("the Error says %q, and the Harness said %q on its way out", said, lastWords)
	}
	if live := h.sessions.live(); len(live) != 0 {
		t.Errorf("the registry still holds %d live Session, and its Harness is gone", len(live))
	}
}
