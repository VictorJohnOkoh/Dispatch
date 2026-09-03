package harness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// The scripted transport for Pi, and the same idea as script_test.go: Pi stops
// and waits on the Gate's dialog, so a plain byte replay would deadlock. The
// script feeds the next frames when it sees the write it was expecting, and fails
// the test on a write it was not.
//
// Two capture shapes are replayed. The pi-gate-dispatch runs are already
// bidirectional, with a dir on every frame, and they are the script as recorded.
// The pi-vendors runs are event streams with nothing going the other way, so a
// launch is put in front of them: they were captured with `pi -ne` months before
// the Gate existed, and an Adapter that declares Gates will not start without one.
// The announcement is the gate capture's own bytes and the probe's answer is built
// from the Model and Vendor the test names, which is the one part of a replay this
// package writes rather than replays.

type piScript struct {
	t       *testing.T
	frames  []recorded
	pipes   Pipes
	toAgent *io.PipeWriter
	agent   *bufio.Scanner

	done chan struct{}

	mu        sync.Mutex
	sent      map[string]string // a recorded command id, against the one that was sent
	abandoned bool              // the test is over, so an unplayed frame is not a failure
}

// newPiScript replays one pi-gate-dispatch capture whole, both directions.
func newPiScript(t *testing.T, capture string) *piScript {
	t.Helper()
	return startPiScript(t, readFrames(t, filepath.Join("pi-gate-dispatch", capture)))
}

// newPiStream replays one pi-vendors event stream behind a launch that says the
// Session got model on the Vendor at base.
func newPiStream(t *testing.T, capture, model, base string) *piScript {
	t.Helper()
	events := readEvents(t, capture)
	launch := []recorded{
		{Dir: "out", Frame: json.RawMessage(`{"type":"get_state"}`)},
		{Dir: "in", Frame: announcement(t)},
		{Dir: "in", Frame: json.RawMessage(fmt.Sprintf(
			`{"id":%q,"type":"response","command":"get_state","success":true,`+
				`"data":{"model":{"id":%q,"provider":"capture","baseUrl":"%s/v1"}}}`,
			startProbe, model, base))},
		{Dir: "out", Frame: json.RawMessage(`{"type":"prompt"}`)},
	}
	return startPiScript(t, append(launch, events...))
}

// announcement is the Gate saying it is ready, taken out of the gate capture so
// that the launch of a stream replay is the bytes a real Gate sent.
func announcement(t *testing.T) json.RawMessage {
	t.Helper()
	for _, r := range readFrames(t, filepath.Join("pi-gate-dispatch", "gate-allow-frames.jsonl")) {
		if r.Dir == "in" && piMethod(r.Frame) == "notify" {
			return r.Frame
		}
	}
	t.Fatal("the gate capture holds no announcement")
	return nil
}

func startPiScript(t *testing.T, frames []recorded) *piScript {
	t.Helper()
	fromAdapter, adapterWrites := io.Pipe()
	adapterReads, scriptWrites := io.Pipe()
	s := &piScript{
		t:       t,
		frames:  frames,
		pipes:   Pipes{In: adapterWrites, Out: adapterReads},
		toAgent: scriptWrites,
		agent:   bufio.NewScanner(fromAdapter),
		done:    make(chan struct{}),
		sent:    map[string]string{},
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

// readFrames reads a capture whose lines carry a dir and a frame.
func readFrames(t *testing.T, capture string) []recorded {
	t.Helper()
	var frames []recorded
	for _, line := range captureLines(t, capture) {
		var r recorded
		if err := json.Unmarshal(line, &r); err != nil {
			t.Fatalf("capture line: %v", err)
		}
		frames = append(frames, r)
	}
	return frames
}

// readEvents reads a capture that is only what the Harness said.
func readEvents(t *testing.T, capture string) []recorded {
	t.Helper()
	var frames []recorded
	for _, line := range captureLines(t, capture) {
		frames = append(frames, recorded{Dir: "in", Frame: append(json.RawMessage(nil), line...)})
	}
	return frames
}

func captureLines(t *testing.T, capture string) [][]byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "research", "captures", capture))
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	var out [][]byte
	lines := bufio.NewScanner(bytes.NewReader(raw))
	lines.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for lines.Scan() {
		if len(bytes.TrimSpace(lines.Bytes())) == 0 {
			continue
		}
		out = append(out, append([]byte(nil), lines.Bytes()...))
	}
	if err := lines.Err(); err != nil {
		t.Fatalf("capture %s: %v", capture, err)
	}
	return out
}

// spawn is the Spawner a test hands the Adapter. No process starts.
func (s *piScript) spawn() Spawner {
	return func(context.Context, Launch) (Pipes, error) { return s.pipes, nil }
}

// play walks the capture. An in frame is written to the Adapter; an out frame is
// the write the Adapter is expected to make next. The pipe is closed at the end,
// because a capture that runs out is a Harness whose stdout ended.
func (s *piScript) play() {
	defer close(s.done)
	defer s.toAgent.Close()
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

// rewrite puts the id this Adapter used on a recorded response, so a capture whose
// client numbered its commands differently still replays.
func (s *piScript) rewrite(frame json.RawMessage) []byte {
	var f struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if json.Unmarshal(frame, &f) != nil || f.Type != "response" {
		return frame
	}
	s.mu.Lock()
	sent, ok := s.sent[f.ID]
	s.mu.Unlock()
	if !ok {
		return frame
	}

	var body map[string]json.RawMessage
	if json.Unmarshal(frame, &body) != nil {
		return frame
	}
	body["id"], _ = json.Marshal(sent)
	out, _ := json.Marshal(body)
	return out
}

// abandon stops the script from judging what it has left. A test that ends the
// Session early, and every test at its cleanup, leaves frames unplayed on purpose.
func (s *piScript) abandon() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.abandoned = true
}

func (s *piScript) quiet() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.abandoned
}

// expect reads the Adapter's next write and checks it is the one the capture
// recorded. A command is matched on its type, and on the one field that carries
// the decision or the Prompt, because those two are what the replay is for.
func (s *piScript) expect(want json.RawMessage) bool {
	if !s.agent.Scan() {
		if s.quiet() {
			return false
		}
		s.t.Errorf("the capture expected %s and the Adapter wrote nothing more", piType(want))
		return false
	}
	got := s.agent.Bytes()
	if s.quiet() {
		return false
	}

	if piType(want) != piType(got) {
		s.t.Errorf("the Adapter wrote %q and the capture has %q", piType(got), piType(want))
		return false
	}
	if value := piValue(want); value != "" && value != piValue(got) {
		s.t.Errorf("the Adapter answered the Gate with %q and the capture answers %q", piValue(got), value)
		return false
	}
	if message := piMessage(want); message != "" && message != piMessage(got) {
		s.t.Errorf("the Adapter sent the Prompt %q and the capture sends %q", piMessage(got), message)
		return false
	}
	if id := piID(want); id != "" {
		s.mu.Lock()
		s.sent[id] = piID(got)
		s.mu.Unlock()
	}
	return true
}

// wait blocks until the capture is played out.
func (s *piScript) wait(t *testing.T) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the capture never played out")
	}
}

// prompts is the Prompt text of every turn the capture's own run drove, so a
// replay asks the Harness the words its frames are an answer to. A gate capture
// records the commands it sent; a stream capture has them as the user's messages.
func (s *piScript) prompts(t *testing.T) []string {
	t.Helper()
	var said []string
	for _, r := range s.frames {
		if r.Dir == "out" && piType(r.Frame) == "prompt" && piMessage(r.Frame) != "" {
			said = append(said, piMessage(r.Frame))
		}
	}
	for _, r := range s.frames {
		if len(said) > 0 {
			break
		}
		if r.Dir == "in" && piType(r.Frame) == "message_start" {
			if text := userText(r.Frame); text != "" {
				said = append(said, text)
			}
		}
	}
	if len(said) == 0 {
		t.Fatal("this capture has no Prompt in it")
	}
	return said
}

// userText is the text of a user message_start, and empty for every other role.
func userText(frame json.RawMessage) string {
	var f struct {
		Message struct {
			Role    string `json:"role"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(frame, &f) != nil || f.Message.Role != "user" || len(f.Message.Content) == 0 {
		return ""
	}
	return f.Message.Content[0].Text
}

func piField(frame []byte, name string) string {
	var f map[string]json.RawMessage
	if json.Unmarshal(frame, &f) != nil {
		return ""
	}
	var value string
	if json.Unmarshal(f[name], &value) != nil {
		return ""
	}
	return value
}

func piType(frame []byte) string    { return piField(frame, "type") }
func piID(frame []byte) string      { return piField(frame, "id") }
func piValue(frame []byte) string   { return piField(frame, "value") }
func piMessage(frame []byte) string { return piField(frame, "message") }
func piMethod(frame []byte) string  { return piField(frame, "method") }
