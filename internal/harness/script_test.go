package harness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// The scripted transport ADR 0006 asked for. A plain byte replay is not enough,
// because OpenCode stops and waits: it will not go past session/request_permission
// until it has an answer. So the script feeds the next frames when it sees the
// write it was expecting, and fails the test on a write it was not.
//
// The repo's captures are already in this shape. Every frame in a *-frames.jsonl
// carries a dir of in or out, and that is the script.
//
// Two things are matched loosely on purpose. An out frame is matched by its method
// and not byte for byte, because this Adapter's initialize says what this client
// can do and the capture's says what the capture script could. And the id of a
// recorded answer is rewritten to the id of the request this Adapter actually
// sent, so the replay does not depend on both numbering their requests alike.

type recorded struct {
	Dir   string          `json:"dir"`
	Frame json.RawMessage `json:"frame"`
}

type script struct {
	t       *testing.T
	frames  []recorded
	pipes   Pipes
	toAgent *io.PipeWriter
	agent   *bufio.Scanner

	done chan struct{}

	mu        sync.Mutex
	sent      map[uint64]uint64 // a recorded request id, against the one that was sent
	abandoned bool              // the test is over, so an unplayed frame is not a failure
}

// newScript reads one capture and hands back the two pipes a Spawner would. The
// capture is named from docs/research/captures, because the refusal was recorded
// in a directory of its own.
func newScript(t *testing.T, capture string) *script {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "research", "captures", capture))
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	var frames []recorded
	lines := bufio.NewScanner(bytes.NewReader(raw))
	lines.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for lines.Scan() {
		var r recorded
		if err := json.Unmarshal(lines.Bytes(), &r); err != nil {
			t.Fatalf("capture line: %v", err)
		}
		frames = append(frames, r)
	}

	fromAdapter, adapterWrites := io.Pipe()
	adapterReads, scriptWrites := io.Pipe()
	s := &script{
		t:       t,
		frames:  frames,
		pipes:   Pipes{In: adapterWrites, Out: adapterReads},
		toAgent: scriptWrites,
		agent:   bufio.NewScanner(fromAdapter),
		done:    make(chan struct{}),
		sent:    map[uint64]uint64{},
	}
	s.agent.Buffer(make([]byte, 0, 64<<10), 8<<20)
	go s.play()
	// The play goroutine is stopped before the test ends, because a script that
	// reported a mismatch afterwards would panic rather than fail.
	t.Cleanup(func() {
		s.abandon()
		scriptWrites.Close()
		adapterWrites.Close()
		<-s.done
	})
	return s
}

// spawn is the Spawner a test hands the Adapter. No process starts.
func (s *script) spawn() Spawner {
	return func(context.Context, Launch) (Pipes, error) { return s.pipes, nil }
}

// play walks the capture. An in frame is written to the Adapter; an out frame is
// the write the Adapter is expected to make next.
func (s *script) play() {
	defer close(s.done)
	for _, r := range s.frames {
		if r.Dir == "in" {
			if _, err := s.toAgent.Write(append(s.rewrite(r.Frame), '\n')); err != nil {
				return
			}
			continue
		}
		if !s.expect(r.Frame) {
			return
		}
	}
}

// rewrite puts the id this Adapter used on a recorded answer, so a capture whose
// client numbered its requests differently still replays.
func (s *script) rewrite(recorded json.RawMessage) []byte {
	var f struct {
		ID     *uint64         `json:"id"`
		Method string          `json:"method"`
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(recorded, &f) != nil || f.ID == nil || f.Method != "" {
		return recorded
	}
	s.mu.Lock()
	sent, ok := s.sent[*f.ID]
	s.mu.Unlock()
	if !ok {
		return recorded
	}

	var body map[string]json.RawMessage
	if json.Unmarshal(recorded, &body) != nil {
		return recorded
	}
	body["id"], _ = json.Marshal(sent)
	out, _ := json.Marshal(body)
	return out
}

// abandon stops the script from judging what it has left. A test that ends the
// Session early, and every test at its cleanup, leaves frames unplayed on purpose.
func (s *script) abandon() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.abandoned = true
}

func (s *script) quiet() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.abandoned
}

// expect reads the Adapter's next write and checks it is the one the capture
// recorded. A request is matched on its method and an answer on the id it answers.
func (s *script) expect(want json.RawMessage) bool {
	if !s.agent.Scan() {
		if s.quiet() {
			return false
		}
		s.t.Errorf("the capture expected %s and the Adapter wrote nothing more", methodOf(want))
		return false
	}
	got := s.agent.Bytes()

	wantMethod, wantID := methodOf(want), idOf(want)
	gotMethod, gotID := methodOf(got), idOf(got)
	if s.quiet() {
		return false
	}
	if wantMethod != gotMethod {
		s.t.Errorf("the Adapter wrote %q and the capture has %q", gotMethod, wantMethod)
		return false
	}
	if wantMethod == "" && (wantID == nil || gotID == nil || *wantID != *gotID) {
		s.t.Errorf("the Adapter answered %v and the capture answers %v", gotID, wantID)
		return false
	}
	if wantMethod != "" && wantID != nil && gotID != nil {
		s.mu.Lock()
		s.sent[*wantID] = *gotID
		s.mu.Unlock()
	}
	return true
}

// wait blocks until the capture is played out.
func (s *script) wait(t *testing.T) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the capture never played out")
	}
}

// prompt is the Prompt text the capture's own run was given, so a replay drives
// the Adapter with the words the frames are an answer to.
func (s *script) prompt(t *testing.T) string {
	t.Helper()
	for _, r := range s.frames {
		if r.Dir != "out" || methodOf(r.Frame) != "session/prompt" {
			continue
		}
		var f struct {
			Params struct {
				Prompt []struct {
					Text string `json:"text"`
				} `json:"prompt"`
			} `json:"params"`
		}
		if json.Unmarshal(r.Frame, &f) == nil && len(f.Params.Prompt) > 0 {
			return f.Params.Prompt[0].Text
		}
	}
	t.Fatal("this capture has no Prompt in it")
	return ""
}

func methodOf(frame []byte) string {
	var f struct {
		Method string `json:"method"`
	}
	json.Unmarshal(frame, &f)
	return f.Method
}

func idOf(frame []byte) *uint64 {
	var f struct {
		ID *uint64 `json:"id"`
	}
	json.Unmarshal(frame, &f)
	return f.ID
}
