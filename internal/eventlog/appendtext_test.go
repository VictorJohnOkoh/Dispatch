package eventlog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
)

// crashLogEnv carries the log's path to the child process, and its presence is
// what tells the child it is the child.
const crashLogEnv = "DISPATCH_CRASH_LOG"

// crashText is 10 KiB written in 100 byte pieces, so the flush at 4 KiB fires
// twice: at 4100 bytes and at 8200. The last 2040 bytes never reach the file.
const (
	crashSize    = 10 << 10
	crashPiece   = 100
	crashFlushed = 8200
)

func openMessageEvent(kind event.Kind) event.Event {
	e := event.Event{
		Session: "s1",
		At:      time.UnixMicro(1_700_000_000_000_000).UTC(),
		Kind:    kind,
	}
	if kind == event.KindReasoning {
		e.Payload = &event.Reasoning{}
	} else {
		e.Payload = &event.AssistantMessage{}
	}
	return e
}

// storedMessage reads one row's text and its complete flag back from the file.
func storedMessage(t *testing.T, path string, seq uint64) (string, bool) {
	t.Helper()
	var stored struct {
		Text     string `json:"text"`
		Complete bool   `json:"complete"`
	}
	for _, row := range readRows(t, path) {
		if row.Seq != seq {
			continue
		}
		if err := json.Unmarshal(row.Payload, &stored); err != nil {
			t.Fatalf("payload of seq %d: %v", seq, err)
		}
		return stored.Text, stored.Complete
	}
	t.Fatalf("no row at seq %d", seq)
	return "", false
}

func TestAppendOpensAMessageWithEmptyText(t *testing.T) {
	path := tempPath(t)
	log := openLog(t, path)

	opened, err := log.Append(openMessageEvent(event.KindAssistantMessage))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	text, complete := storedMessage(t, path, opened.Seq)
	if text != "" || complete {
		t.Errorf("opened row = %q, complete %v, want empty and open", text, complete)
	}
}

// Closing the message is a second write to the same row, not a second row.
func TestFinalAppendTextClosesTheSameRow(t *testing.T) {
	path := tempPath(t)
	log := openLog(t, path)

	opened, err := log.Append(openMessageEvent(event.KindAssistantMessage))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := log.AppendText(opened.Seq, "hello ", false); err != nil {
		t.Fatalf("AppendText: %v", err)
	}
	if _, err := log.AppendText(opened.Seq, "world", true); err != nil {
		t.Fatalf("AppendText final: %v", err)
	}

	if rows := readRows(t, path); len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	text, complete := storedMessage(t, path, opened.Seq)
	if text != "hello world" || !complete {
		t.Errorf("closed row = %q, complete %v, want %q and complete", text, complete, "hello world")
	}
}

// Text below the threshold stays in memory, so the row is still empty and open.
func TestTextBelowTheThresholdIsNotWrittenYet(t *testing.T) {
	path := tempPath(t)
	log := openLog(t, path)

	opened, err := log.Append(openMessageEvent(event.KindReasoning))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := log.AppendText(opened.Seq, strings.Repeat("a", FlushThreshold-1), false); err != nil {
		t.Fatalf("AppendText: %v", err)
	}

	if text, _ := storedMessage(t, path, opened.Seq); text != "" {
		t.Errorf("stored %d bytes, want 0", len(text))
	}
}

func TestFlushHappensAtFourKiB(t *testing.T) {
	path := tempPath(t)
	log := openLog(t, path)

	opened, err := log.Append(openMessageEvent(event.KindAssistantMessage))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := log.AppendText(opened.Seq, strings.Repeat("a", FlushThreshold), false); err != nil {
		t.Fatalf("AppendText: %v", err)
	}

	text, complete := storedMessage(t, path, opened.Seq)
	if len(text) != FlushThreshold || complete {
		t.Errorf("stored %d bytes, complete %v, want %d and open", len(text), complete, FlushThreshold)
	}
}

func TestAppendTextNeedsAnOpenMessage(t *testing.T) {
	log := openLog(t, tempPath(t))

	if _, err := log.AppendText(7, "text", false); err == nil {
		t.Error("AppendText on no open message returned no error")
	}
}

// A Session that dies mid-message leaves a torn message: the text it had, and
// complete still false.
func TestSessionEndedClosesAnOpenMessage(t *testing.T) {
	path := tempPath(t)
	log := openLog(t, path)

	opened, err := log.Append(openMessageEvent(event.KindAssistantMessage))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := log.AppendText(opened.Seq, "half a ", false); err != nil {
		t.Fatalf("AppendText: %v", err)
	}

	ended := openMessageEvent(event.KindSessionEnded)
	ended.Kind, ended.Payload = event.KindSessionEnded, &event.SessionEnded{Reason: event.EndFailed}
	if _, err := log.Append(ended); err != nil {
		t.Fatalf("Append SessionEnded: %v", err)
	}

	text, complete := storedMessage(t, path, opened.Seq)
	if text != "half a " || complete {
		t.Errorf("torn row = %q, complete %v, want %q and open", text, complete, "half a ")
	}
	if _, err := log.AppendText(opened.Seq, "more", false); err == nil {
		t.Error("AppendText after SessionEnded returned no error")
	}
}

// The child writes 10 KiB in small pieces and is then killed. Every byte the
// flush wrote is in the file, and the message is still open.
func TestKilledWriterKeepsEveryFlushedByte(t *testing.T) {
	path := tempPath(t)

	child := exec.Command(os.Args[0], "-test.run=TestCrashWriter")
	child.Env = append(os.Environ(), crashLogEnv+"="+path)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := child.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	defer child.Wait()

	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("child never reported: %v", err)
	}
	if strings.TrimSpace(line) != "written" {
		t.Fatalf("child said %q", line)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatalf("kill child: %v", err)
	}
	child.Wait()

	text, complete := storedMessage(t, path, 1)
	if len(text) != crashFlushed || complete {
		t.Fatalf("stored %d bytes, complete %v, want %d and open", len(text), complete, crashFlushed)
	}
	if text != strings.Repeat("a", crashFlushed) {
		t.Error("stored text is not the text that was written")
	}
}

// TestCrashWriter is the child of TestKilledWriterKeepsEveryFlushedByte. It runs
// only when the parent names the log in the environment.
func TestCrashWriter(t *testing.T) {
	path := os.Getenv(crashLogEnv)
	if path == "" {
		t.Skip("child of TestKilledWriterKeepsEveryFlushedByte")
	}

	log, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	opened, err := log.Append(openMessageEvent(event.KindAssistantMessage))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	for written := 0; written < crashSize; written += crashPiece {
		if _, err := log.AppendText(opened.Seq, strings.Repeat("a", crashPiece), false); err != nil {
			t.Fatalf("AppendText: %v", err)
		}
	}

	fmt.Println("written")
	time.Sleep(time.Minute)
}
