package eventlog

import (
	"testing"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// appendAll writes each Event and fails on the first that will not go in.
func appendAll(t *testing.T, log *Log, events ...event.Event) []event.Event {
	t.Helper()
	out := make([]event.Event, 0, len(events))
	for _, e := range events {
		written, err := log.Append(e)
		if err != nil {
			t.Fatalf("Append %s: %v", e.Kind, err)
		}
		out = append(out, written)
	}
	return out
}

// seqs is what a page holds, for a comparison that reads as the list it is.
func seqs(page []protocol.Event) []uint64 {
	out := make([]uint64, len(page))
	for i, e := range page {
		out[i] = e.Seq
	}
	return out
}

func TestTheLogKeepsOneIdentityForTheLifeOfTheFile(t *testing.T) {
	path := tempPath(t)

	first := openLog(t, path)
	id := first.LogID()
	if id == "" {
		t.Fatal("a new log has no identity")
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if again := openLog(t, path).LogID(); again != id {
		t.Errorf("identity after reopen = %q, want %q", again, id)
	}
	if fresh := openLog(t, tempPath(t)).LogID(); fresh == id {
		t.Errorf("a second file took the same identity %q", id)
	}
}

// The Cursor is not the last Sequence Number written. It stops below an open
// message and catches up when the message closes, which is what makes an
// unfinished message replay whole.
func TestTheCursorLagsAnOpenMessageAndCatchesUpWhenItCloses(t *testing.T) {
	log := openLog(t, tempPath(t))
	appendAll(t, log, prompt("p"))
	if got := log.Cursor(); got != 1 {
		t.Fatalf("Cursor with nothing open = %d, want 1", got)
	}

	message := appendAll(t, log, openMessageEvent(event.KindAssistantMessage))[0]
	appendAll(t, log, prompt("second"))
	if got := log.Cursor(); got != 1 {
		t.Errorf("Cursor with a message open at %d = %d, want 1", message.Seq, got)
	}

	if _, err := log.AppendText(message.Seq, "whole", true); err != nil {
		t.Fatalf("AppendText: %v", err)
	}
	if got := log.Cursor(); got != 3 {
		t.Errorf("Cursor after the message closed = %d, want 3", got)
	}
}

// Resume reads the identity, the high water mark and the open messages together,
// so a replay planned from one view describes one moment.
func TestResumeNamesTheOpenMessagesAtOrBelowLatest(t *testing.T) {
	log := openLog(t, tempPath(t))
	appendAll(t, log,
		prompt("p"),
		openMessageEvent(event.KindAssistantMessage),
		openMessageEvent(event.KindReasoning),
	)

	at := log.Resume()
	if at.LogID != log.LogID() || at.Latest != 3 {
		t.Fatalf("Resume = %+v", at)
	}
	if len(at.Open) != 2 || at.Open[0] != 2 || at.Open[1] != 3 {
		t.Fatalf("open = %v, want 2 and 3", at.Open)
	}
	if got := at.Cursor(); got != 1 {
		t.Errorf("Cursor = %d, want 1", got)
	}
}

func TestReplayReadsEveryEventAboveTheCursorAcrossSessions(t *testing.T) {
	log := openLog(t, tempPath(t))
	appendAll(t, log, promptIn("s1", "a"), promptIn("s2", "b"), promptIn("s1", "c"))

	page, err := log.Replay(1, 3, 10)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if got := seqs(page); len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("replay from 1 = %v, want 2 and 3", got)
	}
	if page[0].Session != "s2" {
		t.Errorf("replay skipped a Session: %+v", page[0])
	}
}

// upTo is the moment the reader joined, and everything above it reaches that
// reader on its subscription instead.
func TestReplayStopsAtTheMomentTheReaderJoined(t *testing.T) {
	log := openLog(t, tempPath(t))
	appendAll(t, log, prompt("a"), prompt("b"), prompt("c"))

	page, err := log.Replay(0, 2, 10)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if got := seqs(page); len(got) != 2 || got[1] != 2 {
		t.Fatalf("replay = %v, want 1 and 2", got)
	}
}

func TestReplayPagesAtTheLimit(t *testing.T) {
	log := openLog(t, tempPath(t))
	for range 5 {
		appendAll(t, log, prompt("p"))
	}

	page, err := log.Replay(1, 5, 2)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if got := seqs(page); len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("page = %v, want 2 and 3", got)
	}
}

// A replay carries an open message whole, above what the last flush wrote. The
// reader resumes below it so that the message replays whole, and clipping it at
// the flush would undo that.
func TestReplayCarriesAnOpenMessageWhole(t *testing.T) {
	log := openLog(t, tempPath(t))
	message := appendAll(t, log, openMessageEvent(event.KindAssistantMessage))[0]
	if _, err := log.AppendText(message.Seq, "half", false); err != nil {
		t.Fatalf("AppendText: %v", err)
	}

	page, err := log.Replay(0, log.Latest(), 10)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(page) != 1 || string(page[0].Payload) != `{"text":"half","complete":false}` {
		t.Fatalf("replay = %+v", page)
	}
}

func TestSessionEventsReadsTypedPayloads(t *testing.T) {
	log := openLog(t, tempPath(t))
	appendAll(t, log, promptIn("s1", "hello"), promptIn("s2", "elsewhere"))

	events, err := log.SessionEvents("s1")
	if err != nil {
		t.Fatalf("SessionEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("%d Events, want 1", len(events))
	}
	p, ok := events[0].Payload.(*event.PromptSubmitted)
	if !ok || p.Text != "hello" {
		t.Errorf("payload = %#v", events[0].Payload)
	}
}
