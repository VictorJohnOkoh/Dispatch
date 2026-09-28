package daemon

import (
	"bytes"
	"errors"
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
// stops at transcriptLimit and says on its last line where it stopped.
//
// Every line starts with the stream it came from, "stdout " or "stderr ", and a
// line is written whole, so stderr never lands inside a JSON-RPC frame on stdout.

// transcriptLimit is where a Session's transcript stops. One Pi tool call in
// docs/research/captures/pi-gate is 76 KB of raw bytes, so this is roughly 840
// tool calls and a normal Session never reaches it.
const transcriptLimit = 64 << 20

// lineLimit is how much of a line a stream holds while it waits for the newline.
// A line that reaches it is written as it stands and the rest starts a new tagged
// line, so a Harness that never writes a newline costs one buffer and not all of
// memory.
const lineLimit = 1 << 20

// transcript is one Session's file. Both the stderr drain and the stdout tee
// write here, from two goroutines, which is the one thing in the Daemon's Harness
// ownership that needs a lock of its own.
type transcript struct {
	// limit is transcriptLimit. It is a field only so a test may shorten it, and no
	// configuration names it.
	limit int64

	// path is the file this transcript is, kept so that a closed transcript can
	// still say where its bytes went.
	path string

	mu      sync.Mutex
	file    *os.File
	written int64
	stdout  stream
	stderr  stream

	// failed is the first write the file refused. A drain that stops reading stops
	// the Harness, so a failure is kept here and reported once, at the close.
	failed error
}

// newTranscript opens the transcript for one Session in the directory the Event
// log lives in.
func newTranscript(dir string, id event.SessionID) (*transcript, error) {
	path := transcriptPath(dir, id)
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("daemon: no transcript for the Session %s: %w", id, err)
	}
	t := &transcript{limit: transcriptLimit, path: path, file: f}
	t.stdout = stream{t: t, tag: "stdout "}
	t.stderr = stream{t: t, tag: "stderr "}
	return t, nil
}

func transcriptPath(dir string, id event.SessionID) string {
	return filepath.Join(dir, string(id)+".transcript")
}

// stream is one of the Harness's two outputs on its way into the transcript. It
// holds the part of a line that has no newline yet, under the transcript's mutex.
type stream struct {
	t       *transcript
	tag     string
	pending []byte
}

// Write reports every byte as written whatever happened, because a Harness is
// never told how much of its output was kept and a short write would stop the
// drain that has to keep reading.
func (s *stream) Write(b []byte) (int, error) {
	s.t.mu.Lock()
	defer s.t.mu.Unlock()

	if s.t.file == nil {
		return len(b), nil
	}
	s.pending = append(s.pending, b...)
	rest := s.pending
	for {
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			break
		}
		s.t.line(s.tag, rest[:i+1])
		rest = rest[i+1:]
	}
	s.pending = append(s.pending[:0], rest...)
	if len(s.pending) >= lineLimit {
		s.flush()
	}
	return len(b), nil
}

// flush writes what is left of a line as a line of its own. The caller holds the
// mutex.
func (s *stream) flush() {
	if len(s.pending) == 0 {
		return
	}
	s.t.line(s.tag, append(s.pending, '\n'))
	s.pending = s.pending[:0]
}

// line writes one tagged line. The caller holds the mutex.
func (t *transcript) line(tag string, b []byte) {
	t.write([]byte(tag))
	t.write(b)
}

// write keeps what fits under the limit and drops the rest. The caller holds the
// mutex.
func (t *transcript) write(b []byte) {
	if t.file == nil {
		return
	}
	room := t.limit - t.written
	if int64(len(b)) <= room {
		t.written += int64(t.keep(b))
		return
	}

	t.written += int64(t.keep(b[:room]))
	t.mark()
	t.close()
}

// keep writes to the file and remembers the first refusal. The caller holds the
// mutex.
func (t *transcript) keep(b []byte) int {
	n, err := t.file.Write(b)
	if err != nil && t.failed == nil {
		t.failed = err
	}
	return n
}

// mark is the one line a transcript that stopped ends on.
func (t *transcript) mark() {
	fmt.Fprintf(t.file, "\n-- this transcript stopped at %d bytes, and the Harness kept talking --\n", t.written)
}

// Close writes the line each stream had not finished, finishes the transcript and
// reports what it could not write. Writes after it are discarded, because the
// stdout tee and the stderr drain both outlive the process by a moment.
func (t *transcript) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stdout.flush()
	t.stderr.flush()
	return t.close()
}

// close is Close with the mutex already held. A file that is already closed is
// closed again by whichever of the limit and the Session's end came second.
func (t *transcript) close() error {
	if t.file == nil {
		return t.failed
	}
	f := t.file
	t.file = nil
	return errors.Join(t.failed, f.Close())
}
