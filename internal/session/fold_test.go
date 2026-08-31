package session

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
)

// at is a Daemon timestamp. The fold never reads one, so every Event in this file
// can share it.
var at = time.UnixMicro(1756412090000000).UTC()

func ev(kind event.Kind, payload any) event.Event {
	return event.Event{Session: "s-7f3a2c", At: at, Kind: kind, Payload: payload}
}

func started() event.Event {
	return ev(event.KindSessionStarted, &event.SessionStarted{
		Harness: "opencode", Model: "qwen3.5-9b", Vendor: "ollama", Cwd: "/home/victor/work",
	})
}

func ready() event.Event {
	return ev(event.KindSessionReady, &event.SessionReady{Model: "capstone/qwen3.5-9b"})
}

func submitted() event.Event {
	return ev(event.KindPromptSubmitted, &event.PromptSubmitted{Text: "rename the handler"})
}

func asked(toolCallID string) event.Event {
	return ev(event.KindApprovalRequested, &event.ApprovalRequested{ToolCallID: toolCallID})
}

func decided(toolCallID string) event.Event {
	return ev(event.KindApprovalDecided, &event.ApprovalDecided{
		ToolCallID: toolCallID, Decision: event.DecisionAllowed, By: event.ByUser,
	})
}

func ended(reason event.EndReason) event.Event {
	return ev(event.KindSessionEnded, &event.SessionEnded{Reason: reason})
}

// Every one of ADR 0008's five states, from a slice built by hand.
func TestFoldReachesEachOfTheFiveStates(t *testing.T) {
	cases := []struct {
		name   string
		events []event.Event
		want   State
	}{
		{"no SessionReady", []event.Event{started()}, Starting},
		{"launched", []event.Event{started(), ready()}, Idle},
		{"a Prompt in flight", []event.Event{started(), ready(), submitted()}, Working},
		{"a question open", []event.Event{started(), ready(), submitted(), asked("tc-1")}, Asking},
		{"terminal", []event.Event{started(), ready(), ended(event.EndStopped)}, Ended},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, _ := Fold(c.events); got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

func TestEndedCarriesEachOfItsThreeReasons(t *testing.T) {
	for _, reason := range []event.EndReason{event.EndStopped, event.EndFailed, event.EndLost} {
		state, got := Fold([]event.Event{started(), ready(), ended(reason)})
		if state != Ended {
			t.Errorf("%s: got %s, want Ended", reason, state)
		}
		if got != reason {
			t.Errorf("got reason %q, want %q", got, reason)
		}
	}
}

// Every state but Ended answers with no reason, so a caller cannot read one off a
// Session that is still running.
func TestOnlyEndedCarriesAReason(t *testing.T) {
	if _, reason := Fold([]event.Event{started(), ready(), submitted()}); reason != "" {
		t.Errorf("Working carries reason %q, want none", reason)
	}
}

// Asking is more specific than Working and both folds are true at once, so the fold
// answers Asking first and stays there until the last question is decided.
func TestAskingOutranksWorkingUntilTheLastQuestionIsDecided(t *testing.T) {
	events := []event.Event{started(), ready(), submitted(), asked("tc-1"), asked("tc-2")}

	if got, _ := Fold(events); got != Asking {
		t.Fatalf("two questions open: got %s, want Asking", got)
	}
	if got, _ := Fold(append(events, decided("tc-1"))); got != Asking {
		t.Errorf("one question still open: got %s, want Asking", got)
	}
	if got, _ := Fold(append(events, decided("tc-1"), decided("tc-2"))); got != Working {
		t.Errorf("both decided: got %s, want Working", got)
	}
}

// SessionEnded is terminal and always last, so nothing above it and nothing below
// it can produce another answer.
func TestSessionEndedBeatsAnOpenQuestion(t *testing.T) {
	events := []event.Event{started(), ready(), submitted(), asked("tc-1"), ended(event.EndLost)}
	state, reason := Fold(events)
	if state != Ended || reason != event.EndLost {
		t.Errorf("got %s{%s}, want Ended{lost}", state, reason)
	}
}

// The fixture is the specification the Client's JS twin is held to as well, so a
// case added here changes both folds or neither.
func TestFoldAgainstTheSharedFixture(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name   string          `json:"name"`
			Events []event.Event   `json:"events"`
			State  State           `json:"state"`
			Reason event.EndReason `json:"reason"`
		} `json:"cases"`
	}

	b, err := os.ReadFile("testdata/fold.json")
	if err != nil {
		t.Fatalf("read the fixture: %v", err)
	}
	if err := json.Unmarshal(b, &fixture); err != nil {
		t.Fatalf("decode the fixture: %v", err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("the fixture holds no cases")
	}

	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			state, reason := Fold(c.Events)
			if state != c.State || reason != c.Reason {
				t.Errorf("got %s{%s}, want %s{%s}", state, reason, c.State, c.Reason)
			}
		})
	}
}

// The five states are exactly these, in this order, and they round trip by name
// because the fixture and the Client both spell them that way.
func TestStateNames(t *testing.T) {
	want := []struct {
		state State
		name  string
	}{
		{Starting, "Starting"},
		{Idle, "Idle"},
		{Working, "Working"},
		{Asking, "Asking"},
		{Ended, "Ended"},
	}

	if len(want) != numStates {
		t.Fatalf("numStates is %d, want %d", numStates, len(want))
	}

	for i, c := range want {
		if int(c.state) != i {
			t.Errorf("%s is %d, want %d", c.name, c.state, i)
		}
		b, err := json.Marshal(c.state)
		if err != nil {
			t.Fatalf("marshal %s: %v", c.name, err)
		}
		if got := string(b); got != `"`+c.name+`"` {
			t.Errorf("marshal: got %s, want %q", got, c.name)
		}

		var back State
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatalf("unmarshal %s: %v", b, err)
		}
		if back != c.state {
			t.Errorf("round trip: got %s, want %s", back, c.name)
		}
	}
}

func TestStateRejectsAnythingElse(t *testing.T) {
	var s State
	if err := json.Unmarshal([]byte(`"Stopping"`), &s); err == nil {
		t.Error("Stopping is not a State and should not decode")
	}
	if _, err := json.Marshal(State(numStates)); err == nil {
		t.Error("a State past the set should not encode")
	}
}
