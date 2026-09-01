package eventlog

import (
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
)

// ended is one Session's last Event, which is what the sweep looks for.
func ended(session event.SessionID) event.Event {
	return event.Event{
		Session: session,
		At:      time.UnixMicro(1_700_000_000_000_000).UTC(),
		Kind:    event.KindSessionEnded,
		Payload: &event.SessionEnded{Reason: event.EndStopped},
	}
}

func TestSweepNamesOnlyTheSessionsWithNoSessionEnded(t *testing.T) {
	log := openLog(t, tempPath(t))
	appendAll(t, log,
		promptIn("done", "a"), ended("done"),
		promptIn("live", "b"),
		promptIn("also-live", "c"),
	)

	lost, err := log.Sweep()
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(lost) != 2 || lost[0] != "live" || lost[1] != "also-live" {
		t.Fatalf("swept %v, want live and also-live in start order", lost)
	}
}

func TestSweepFindsNothingInAnEmptyLog(t *testing.T) {
	lost, err := openLog(t, tempPath(t)).Sweep()
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(lost) != 0 {
		t.Errorf("swept %v from an empty log", lost)
	}
}

// The mark is what keeps the scan bounded by one run rather than by the log's
// whole history. A Session the caller ended after a sweep is not named again, and
// one that started afterwards is.
func TestSweepScansOnlyWhatWasWrittenSinceTheLastOne(t *testing.T) {
	path := tempPath(t)
	log := openLog(t, path)
	appendAll(t, log, promptIn("first", "a"))

	if lost, err := log.Sweep(); err != nil || len(lost) != 1 || lost[0] != "first" {
		t.Fatalf("first sweep = %v, %v", lost, err)
	}
	appendAll(t, log, ended("first"))
	if err := log.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	next := openLog(t, path)
	appendAll(t, next, promptIn("second", "b"))
	lost, err := next.Sweep()
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(lost) != 1 || lost[0] != "second" {
		t.Fatalf("second sweep = %v, want only second", lost)
	}
}
