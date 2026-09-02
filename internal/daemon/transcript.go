package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
)

// The transcript is one Session's raw Harness bytes, in a file beside the Event
// log. The Client renders only Events and never raw Harness output, so the only
// reader here is a human debugging an Adapter. Native output that no Event Kind
// covers is dropped from the log and kept here, and that is the whole reason this
// file exists.
//
// A transcript is not the log. Nothing is ever deleted from the log; this file
// stops at transcriptCap and says on its last line where it stopped.

// transcriptCap is where a Session's transcript stops. One Pi tool call in
// docs/research/captures/pi-gate is 76 KB of raw bytes, so this is roughly 840
// tool calls and a normal Session never reaches it.
const transcriptCap = 64 << 20

// transcript is one Session's file. Both the stderr drain and the stdout tee
// write here, from two goroutines, which is the one thing in the Daemon's Harness
// ownership that needs a lock of its own.
type transcript struct {
	// cap is transcriptCap. It is a field only so a test may shorten it, and no
	// configuration names it.
	cap int64

	mu      sync.Mutex
	file    *os.File
	written int64
}

// newTranscript opens the transcript for one Session in the directory the Event
// log lives in.
func newTranscript(dir string, id event.SessionID) (*transcript, error) {
	f, err := os.Create(transcriptPath(dir, id))
	if err != nil {
		return nil, fmt.Errorf("daemon: no transcript for the Session %s: %w", id, err)
	}
	return &transcript{cap: transcriptCap, file: f}, nil
}

func transcriptPath(dir string, id event.SessionID) string {
	return filepath.Join(dir, string(id)+".transcript")
}

// Write keeps what fits under the cap and drops the rest. It reports every byte
// as written, because a Harness is never told how much of its output was kept and
// a short write would stop the drain that has to keep reading.
func (t *transcript) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.file == nil {
		return len(b), nil
	}
	room := t.cap - t.written
	if int64(len(b)) < room {
		n, _ := t.file.Write(b)
		t.written += int64(n)
		return len(b), nil
	}

	t.file.Write(b[:room])
	t.written += room
	fmt.Fprintf(t.file, "\n-- this transcript stopped at %d bytes, and the Harness kept talking --\n", t.written)
	t.close()
	return len(b), nil
}

// Close finishes the transcript. Writes after it are discarded, because the stdout
// tee and the stderr drain both outlive the process by a moment.
func (t *transcript) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.close()
}

// close is Close with the mutex already held. A file that is already closed is
// closed again by whichever of the cap and the Session's end came second.
func (t *transcript) close() error {
	if t.file == nil {
		return nil
	}
	f := t.file
	t.file = nil
	return f.Close()
}
