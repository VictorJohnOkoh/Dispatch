package eventlog

import (
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
)

// promptIn is a Prompt in a named Session, for the tests that need two Sessions
// in one log.
func promptIn(session event.SessionID, text string) event.Event {
	e := prompt(text)
	e.Session = session
	return e
}

func TestLatestIsTheHighestSeqAllocated(t *testing.T) {
	log := openLog(t, tempPath(t))

	if got := log.Latest(); got != 0 {
		t.Fatalf("Latest on an empty log = %d", got)
	}
	for range 3 {
		if _, err := log.Append(prompt("p")); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if got := log.Latest(); got != 3 {
		t.Fatalf("Latest = %d, want 3", got)
	}
}

func TestSessionPageReadsOnlyThatSession(t *testing.T) {
	log := openLog(t, tempPath(t))
	for _, e := range []event.Event{
		promptIn("s1", "one"), promptIn("s2", "two"), promptIn("s1", "three"),
	} {
		if _, err := log.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	got, err := log.SessionPage("s1", 0, 10)
	if err != nil {
		t.Fatalf("SessionPage: %v", err)
	}
	if len(got) != 2 || got[0].Seq != 1 || got[1].Seq != 3 {
		t.Fatalf("page = %+v, want Seq 1 and 3", got)
	}
	if got[0].Session != "s1" || got[1].Session != "s1" {
		t.Errorf("page names another Session: %+v", got)
	}
	if got[0].Kind != string(event.KindPromptSubmitted) || string(got[0].Payload) == "" {
		t.Errorf("row = %+v", got[0])
	}
	if want := time.UnixMicro(1_700_000_000_000_000).UnixMicro(); got[0].At != want {
		t.Errorf("At = %d, want %d", got[0].At, want)
	}
}

func TestSessionPageStartsAfterTheSeqItIsGivenAndStopsAtTheLimit(t *testing.T) {
	log := openLog(t, tempPath(t))
	for range 5 {
		if _, err := log.Append(promptIn("s1", "p")); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	got, err := log.SessionPage("s1", 2, 2)
	if err != nil {
		t.Fatalf("SessionPage: %v", err)
	}
	if len(got) != 2 || got[0].Seq != 3 || got[1].Seq != 4 {
		t.Fatalf("page = %+v, want Seq 3 and 4", got)
	}
}

// The row holds what was flushed, so a page taken mid-message carries the text so
// far and complete stays false.
func TestSessionPageCarriesAnOpenMessageAsFarAsItGot(t *testing.T) {
	log := openLog(t, tempPath(t))
	e, err := log.Append(openMessageEvent(event.KindAssistantMessage))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := log.AppendText(e.Seq, "half a", false); err != nil {
		t.Fatalf("AppendText: %v", err)
	}
	if _, err := log.AppendText(e.Seq, " message", true); err != nil {
		t.Fatalf("AppendText: %v", err)
	}

	got, err := log.SessionPage("s1", 0, 10)
	if err != nil {
		t.Fatalf("SessionPage: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("%d rows, want 1", len(got))
	}
	if want := `{"text":"half a message","complete":true}`; string(got[0].Payload) != want {
		t.Errorf("payload = %s", got[0].Payload)
	}
}

// An Event that leaves a message open says so, because the Cursor may not pass it
// until the message closes.
func TestFrameSaysWhetherTheEventIsStillTakingText(t *testing.T) {
	log := openLog(t, tempPath(t))
	frames, stop := log.Subscribe()
	defer stop()

	if _, err := log.Append(openMessageEvent(event.KindAssistantMessage)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if f := receive(t, frames); !f.Open {
		t.Errorf("an appendable Event that is not complete is not Open")
	}

	if _, err := log.Append(prompt("p")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if f := receive(t, frames); f.Open {
		t.Errorf("a Prompt is Open")
	}
}
