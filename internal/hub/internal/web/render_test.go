package web

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// render.js draws the same row from a live Frame, so what is asserted here is what
// that file must draw as well. Until the shared fixture lands the two are held
// together by this test and by reading, and the rows below are the ones where
// they disagreed.
func TestDrawSpellsPayloadsAsPageJSSpellsThem(t *testing.T) {
	tests := []struct {
		name    string
		kind    event.Kind
		payload string
		title   string
		detail  string
	}{
		{
			// JSON.stringify(null) is not what render.js draws here: it draws nothing,
			// because a call with no arguments carries none.
			name:    "a Tool Call whose arguments are the literal null",
			kind:    event.KindToolCallRequested,
			payload: `{"toolCallId":"c1","name":"bash","toolKind":"execute","title":"run it","args":null}`,
			title:   "Tool call: bash",
			detail:  "run it",
		},
		{
			name:    "a Tool Call with no args key at all",
			kind:    event.KindToolCallRequested,
			payload: `{"toolCallId":"c1","name":"bash","toolKind":"execute","title":"run it"}`,
			title:   "Tool call: bash",
			detail:  "run it",
		},
		{
			// The Harness's own spacing does not reach the row. JSON.stringify writes
			// none, and the two must spell the same bytes.
			name:    "a Tool Call whose arguments arrived with spacing",
			kind:    event.KindToolCallRequested,
			payload: `{"toolCallId":"c1","name":"bash","toolKind":"execute","title":"run it","args":{"cmd": "ls",  "dir": "/tmp"}}`,
			title:   "Tool call: bash",
			detail:  `run it {"cmd":"ls","dir":"/tmp"}`,
		},
		{
			// A Kind this build never heard of keeps its payload, compacted the same
			// way, and is titled with the Kind.
			name:    "a Kind this build has never heard of",
			kind:    event.Kind("Telemetry"),
			payload: `{"beat": 3}`,
			title:   "Telemetry",
			detail:  `{"beat":3}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			drawn := rows([]protocol.Event{{
				Seq: 1, Session: "s-1", At: time.Now().UnixMicro(),
				Kind: string(tt.kind), Payload: json.RawMessage(tt.payload),
			}})
			if len(drawn) != 1 {
				t.Fatalf("%d rows, want 1", len(drawn))
			}
			if drawn[0].Title != tt.title {
				t.Errorf("title %q, want %q", drawn[0].Title, tt.title)
			}
			if drawn[0].Detail != tt.detail {
				t.Errorf("detail %q, want %q", drawn[0].Detail, tt.detail)
			}
		})
	}
}

// The page carries its Events in a script element, so a Harness that wrote
// </script> into a message must not be able to close it. encoding/json escapes
// <, > and & even inside a payload it is passing through, and this is the test
// that says so rather than the comment.
func TestTheEmbeddedEventsCannotCloseTheScriptTag(t *testing.T) {
	said := string(payloads([]protocol.Event{{
		Seq: 1, Session: "s-1", Kind: "AssistantMessage",
		Payload: json.RawMessage(`{"text":"</script><img src=x onerror=alert(1)>","complete":true}`),
	}}))

	if strings.Contains(said, "</script") || strings.Contains(said, "<img") {
		t.Fatalf("the page would carry %s", said)
	}
	// And it is still the same JSON once a browser has parsed it.
	var back []struct {
		Payload struct {
			Text string `json:"text"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(said), &back); err != nil {
		t.Fatalf("the browser could not read it: %v", err)
	}
	if back[0].Payload.Text != `</script><img src=x onerror=alert(1)>` {
		t.Errorf("the text came back as %q", back[0].Payload.Text)
	}
}

// Two levels is the ceiling. A Prompt holds Tool Calls, a Tool Call has an end,
// and that is all the structure the Event model has: ToolCallRequested carries no
// parent id, so a Client that drew a Tool Call inside another one would be
// inventing a relation no Event states.
func TestNestingStopsAtTwoLevels(t *testing.T) {
	inset := map[event.Kind]bool{
		event.KindReasoning: true, event.KindToolCallRequested: true,
		event.KindApprovalRequested: true, event.KindApprovalDecided: true,
		event.KindToolCallEnded: true,
	}
	for _, c := range []struct {
		kind    event.Kind
		payload any
	}{
		{event.KindSessionStarted, &event.SessionStarted{}},
		{event.KindPromptSubmitted, &event.PromptSubmitted{}},
		{event.KindReasoning, &event.Reasoning{}},
		{event.KindToolCallRequested, &event.ToolCallRequested{}},
		{event.KindApprovalRequested, &event.ApprovalRequested{}},
		{event.KindApprovalDecided, &event.ApprovalDecided{}},
		{event.KindToolCallEnded, &event.ToolCallEnded{}},
		{event.KindPromptCompleted, &event.PromptCompleted{}},
		{event.KindSessionEnded, &event.SessionEnded{}},
	} {
		r := draw(event.Event{Seq: 1, Kind: c.kind, Payload: c.payload})
		if r.Inset != inset[c.kind] {
			t.Errorf("%s draws inset = %v, want %v", c.kind, r.Inset, inset[c.kind])
		}
	}
	// There is no third level to draw one in: Inset is a bool, and a Tool Call is
	// drawn beside the calls around it rather than inside any of them.
	if got := draw(event.Event{Kind: event.KindToolCallRequested, Payload: &event.ToolCallRequested{}}); !got.Inset {
		t.Error("a Tool Call is not in the turn it belongs to")
	}
}

// An outcome nobody reported is grey, not red. It means the Harness went quiet,
// which is not a failure and is not the user's to fix.
func TestAnUnknownOutcomeIsGreyAndAFailureIsNot(t *testing.T) {
	for _, c := range []struct {
		outcome event.Outcome
		want    string
	}{
		{event.OutcomeUnknown, toneUnknown},
		{event.OutcomeOK, toneOK},
		{event.OutcomeError, toneBad},
		{event.OutcomeRefused, toneBad},
	} {
		got := draw(event.Event{Kind: event.KindToolCallEnded, Payload: &event.ToolCallEnded{Outcome: c.outcome}})
		if got.Tone != c.want {
			t.Errorf("%s draws %q, want %q", c.outcome, got.Tone, c.want)
		}
	}
	if toneUnknown == toneBad {
		t.Error("no result reported and failed are the same colour")
	}
}
