package eventlog

import (
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
)

// receive takes the next Frame, and fails rather than hanging if none arrives.
func receive(t *testing.T, frames <-chan Frame) Frame {
	t.Helper()
	select {
	case f, ok := <-frames:
		if !ok {
			t.Fatal("subscriber was dropped")
		}
		return f
	case <-time.After(2 * time.Second):
		t.Fatal("no Frame arrived")
		return Frame{}
	}
}

func TestSubscriberSeesEveryEventInSeqOrder(t *testing.T) {
	log := openLog(t, tempPath(t))
	frames, stop := log.Subscribe()
	defer stop()

	const appends = 50
	for range appends {
		if _, err := log.Append(prompt("p")); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	for i := range appends {
		f := receive(t, frames)
		if f.Event == nil {
			t.Fatalf("Frame %d carries a Delta, want an Event", i)
		}
		if want := uint64(i + 1); f.Event.Seq != want {
			t.Fatalf("Seq = %d, want %d", f.Event.Seq, want)
		}
	}
}

// A subscriber that stops reading is dropped, and the writer keeps its pace.
func TestSlowSubscriberIsDroppedAndAppendStaysFast(t *testing.T) {
	log := openLog(t, tempPath(t))
	frames, stop := log.Subscribe()
	defer stop()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range subscriberBuffer * 2 {
			if _, err := log.Append(prompt("p")); err != nil {
				t.Errorf("Append: %v", err)
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Append blocked on a subscriber that stopped reading")
	}

	// The buffer holds what arrived before the drop, and then the channel ends.
	for range frames {
	}
}

// Stopping twice, and stopping a subscriber the log already dropped, are both safe.
func TestStopIsSafeToRepeat(t *testing.T) {
	log := openLog(t, tempPath(t))
	frames, stop := log.Subscribe()

	stop()
	stop()

	if _, ok := <-frames; ok {
		t.Error("a stopped subscriber still received a Frame")
	}
}

func TestDeltasReachSubscribers(t *testing.T) {
	log := openLog(t, tempPath(t))
	frames, stop := log.Subscribe()
	defer stop()

	opened, err := log.Append(openMessageEvent(event.KindAssistantMessage))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := log.AppendText(opened.Seq, "one ", false); err != nil {
		t.Fatalf("AppendText: %v", err)
	}
	if _, err := log.AppendText(opened.Seq, "two", true); err != nil {
		t.Fatalf("AppendText final: %v", err)
	}

	if f := receive(t, frames); f.Event == nil || f.Event.Seq != opened.Seq {
		t.Fatal("the open Event did not reach the subscriber first")
	}

	first := receive(t, frames).Delta
	if first == nil || first.Text != "one " || first.N != 0 || first.Final {
		t.Errorf("first Delta = %+v, want text %q at N 0 and not final", first, "one ")
	}

	// The final Delta carries the whole text, so a subscriber that missed one
	// still ends correct.
	last := receive(t, frames).Delta
	if last == nil || last.Text != "one two" || !last.Final {
		t.Errorf("final Delta = %+v, want the whole text and final", last)
	}
	if last.N != len("one two") {
		t.Errorf("final Delta N = %d, want %d", last.N, len("one two"))
	}
	if last.Seq != opened.Seq {
		t.Errorf("Delta Seq = %d, want the Event's %d", last.Seq, opened.Seq)
	}
}
