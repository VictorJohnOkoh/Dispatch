package daemon

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/harness"
)

// read is one Session's transcript as a human debugging an Adapter reads it.
func (h *host) transcript(t *testing.T, id event.SessionID) string {
	t.Helper()
	raw, err := os.ReadFile(transcriptPath(h.root, id))
	if err != nil {
		t.Fatalf("no transcript for %s: %v", id, err)
	}
	return string(raw)
}

// The cap is a byte counter and one threshold. What fits is kept, what follows is
// dropped, and the last line says where it stopped.
func TestTheTranscriptStopsAtItsCapAndSaysWhereItStopped(t *testing.T) {
	dir := t.TempDir()
	tr, err := newTranscript(dir, "s-cap")
	if err != nil {
		t.Fatalf("newTranscript: %v", err)
	}
	tr.limit = 16

	tr.Write(bytes.Repeat([]byte("a"), 10))
	tr.Write(bytes.Repeat([]byte("b"), 10))
	tr.Write([]byte("this never lands"))
	tr.Close()

	raw, err := os.ReadFile(transcriptPath(dir, "s-cap"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	kept, marker, found := strings.Cut(string(raw), "\n")
	if !found {
		t.Fatalf("the transcript is %q, and it says nothing about stopping", raw)
	}
	if kept != "aaaaaaaaaabbbbbb" {
		t.Errorf("the transcript kept %q, want the 16 bytes that fit", kept)
	}
	if !strings.Contains(marker, strconv.Itoa(16)) {
		t.Errorf("the last line is %q, and it has to say where it stopped", marker)
	}
	if strings.Contains(string(raw), "this never lands") {
		t.Error("the transcript kept writing past its limit")
	}
}

// A Session whose output fills the file exactly is not a Session that was cut
// short, so it gets no line saying it was.
func TestATranscriptThatExactlyFillsItsLimitSaysNothingAboutStopping(t *testing.T) {
	dir := t.TempDir()
	tr, err := newTranscript(dir, "s-exact")
	if err != nil {
		t.Fatalf("newTranscript: %v", err)
	}
	tr.limit = 16
	tr.Write(bytes.Repeat([]byte("a"), 16))
	tr.Close()

	raw, err := os.ReadFile(transcriptPath(dir, "s-exact"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(raw) != strings.Repeat("a", 16) {
		t.Errorf("the transcript is %q, and every byte of it fitted", raw)
	}
}

// A write after the file is closed is discarded rather than refused. The stdout
// tee and the stderr drain both outlive the process by a moment.
func TestTheTranscriptTakesAWriteAfterItIsClosed(t *testing.T) {
	dir := t.TempDir()
	tr, err := newTranscript(dir, "s-closed")
	if err != nil {
		t.Fatalf("newTranscript: %v", err)
	}
	tr.Close()

	if n, err := tr.Write([]byte("too late")); n != len("too late") || err != nil {
		t.Errorf("Write = %d, %v after the close", n, err)
	}
	raw, err := os.ReadFile(transcriptPath(dir, "s-closed"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(raw) != 0 {
		t.Errorf("the closed transcript took %q", raw)
	}
}

// A Session that ended failed still has a readable transcript holding the
// process's stderr, which is the only evidence an exit nobody asked for leaves.
func TestAFailedSessionsTranscriptHoldsTheProcessStderr(t *testing.T) {
	w := newTower(t)
	h, _ := stubHost(t, w, "quit")

	id := h.started(t, h.post(t, "/v1/sessions", stubStart)).Session
	h.waitState(t, id, "Ended")

	if said := h.transcript(t, id); !strings.Contains(said, lastWords) {
		t.Errorf("the transcript holds %q, and the Harness said %q on its way out", said, lastWords)
	}
}

// The Harness's stdout goes to the transcript whole, including the output no
// Event Kind covers. The Adapter reads the same bytes and drops what it cannot
// translate, so this file is the only place they are kept.
func TestTheTranscriptHoldsTheHarnessStdout(t *testing.T) {
	w := newTower(t)
	exe, l := stubLaunch(t, w, "noisy")
	a := &reading{launch: l, read: make(chan string, 1)}
	h := newHost(t, Harness{Name: "stub", Exe: exe, Adapter: a})
	h.stopWait = 100 * time.Millisecond

	id := h.started(t, h.post(t, "/v1/sessions", stubStart)).Session
	h.waitState(t, id, "Idle")
	select {
	case <-a.read:
	case <-time.After(20 * time.Second):
		t.Fatal("the Harness never said anything on stdout")
	}
	h.command(t, id, "stop", "")

	if said := h.transcript(t, id); !strings.Contains(said, "drained") {
		t.Errorf("the transcript holds %d bytes and none of them is what the Harness said on stdout", len(said))
	}
}

// reading is a Harness Adapter that spawns a real process and reads its stdout,
// which is what puts the tee to the transcript in the path.
type reading struct {
	launch harness.Launch
	read   chan string
}

func (a *reading) Capabilities() harness.Capabilities { return harness.Capabilities{Tools: true} }

func (a *reading) Start(ctx context.Context, spec harness.SessionSpec, _ harness.Sink) (harness.Run, error) {
	pipes, err := spec.Spawn(ctx, a.launch)
	if err != nil {
		return nil, err
	}
	// One line, not the whole stream. The stub says its line and then waits on a
	// stdin that only the ladder closes.
	go func() {
		said, _ := bufio.NewReader(pipes.Out).ReadString('\n')
		a.read <- said
	}()
	return a, nil
}

func (a *reading) Prompt(context.Context, string) error { return nil }
func (a *reading) Interrupt(context.Context) error      { return nil }
func (a *reading) Close() error                         { return nil }
