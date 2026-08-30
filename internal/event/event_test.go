package event

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// The two literal frames ADR 0009 prints, byte for byte. They are the wire shape.
func TestWireShapeMatchesADR0009(t *testing.T) {
	at := time.UnixMicro(1756412093118000).UTC()

	cases := []struct {
		name string
		ev   Event
		json string
	}{
		{
			name: "PromptSubmitted",
			ev: Event{
				Seq: 9412, Session: "s-7f3a2c", At: at, Kind: KindPromptSubmitted,
				Payload: &PromptSubmitted{Text: "rename the handler"},
			},
			json: `{"seq":9412,"session":"s-7f3a2c","at":1756412093118000,"kind":"PromptSubmitted","payload":{"text":"rename the handler"}}`,
		},
		{
			name: "AssistantMessage",
			ev: Event{
				Seq: 9413, Session: "s-7f3a2c", At: time.UnixMicro(1756412093402000).UTC(),
				Kind: KindAssistantMessage, Payload: &AssistantMessage{},
			},
			json: `{"seq":9413,"session":"s-7f3a2c","at":1756412093402000,"kind":"AssistantMessage","payload":{"text":"","complete":false}}`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := json.Marshal(c.ev)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != c.json {
				t.Errorf("marshal\n got %s\nwant %s", got, c.json)
			}
		})
	}
}

// The sixteen Kinds, written out here rather than read from the package, so that
// this test is a second statement of the closed set and not a copy of the first.
var everyKind = []struct {
	kind    Kind
	adapter bool // written by a Harness Adapter
	payload any
}{
	{KindSessionStarted, false, &SessionStarted{Harness: "opencode", Model: "qwen3", Vendor: "ollama", Cwd: "/srv/work"}},
	{KindSessionReady, false, &SessionReady{Model: "capstone/qwen3.5-9b"}},
	{KindApprovalPolicySet, false, &ApprovalPolicySet{Policy: Policy{RuleAuto, RuleWait, RuleWait, RuleWait, RuleAuto}, SetBy: SetByDefault}},
	{KindPromptSubmitted, false, &PromptSubmitted{Text: "rename the handler"}},
	{KindApprovalRequested, false, &ApprovalRequested{ToolCallID: "t-1", Title: "run tests", Detail: "go test ./..."}},
	{KindApprovalDecided, false, &ApprovalDecided{ToolCallID: "t-1", Decision: DecisionAllowed, By: ByPolicy}},
	{KindError, false, &Error{Code: ErrVendor, Message: "model not found"}},
	{KindSessionEnded, false, &SessionEnded{Reason: EndLost}},
	{KindHubDetached, false, &NoPayload{}},
	{KindHubAttached, false, &NoPayload{}},
	{KindDaemonStarted, false, &NoPayload{}},

	{KindReasoning, true, &Reasoning{Text: "the callers are in two files", Complete: true}},
	{KindAssistantMessage, true, &AssistantMessage{Text: "I'll rename it.", Complete: true}},
	{KindToolCallRequested, true, &ToolCallRequested{ToolCallID: "t-1", Name: "bash", ToolKind: ToolExecute, Title: "run tests", Args: json.RawMessage(`{"cmd":"go test"}`)}},
	{KindToolCallEnded, true, &ToolCallEnded{ToolCallID: "t-1", Outcome: OutcomeUnknown, Content: ""}},
	{KindPromptCompleted, true, &PromptCompleted{StopReason: "end_turn", Usage: Usage{Input: 1200, Output: 84, CacheRead: 900, Total: 1284}}},
}

func TestSixteenKindsRoundTrip(t *testing.T) {
	if len(everyKind) != 16 {
		t.Fatalf("the set is closed at sixteen, got %d", len(everyKind))
	}

	for _, c := range everyKind {
		t.Run(string(c.kind), func(t *testing.T) {
			want := Event{Seq: 41, Session: "s-7f3a2c", At: time.UnixMicro(1756412093118000).UTC(), Kind: c.kind, Payload: c.payload}

			b, err := json.Marshal(want)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var got Event
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatalf("unmarshal %s: %v", b, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("round trip\n got %+v\nwant %+v", got, want)
			}
		})
	}
}

// The Harness Adapter writes five Kinds and the Daemon writes the other eleven.
func TestKindsSplitFiveAndEleven(t *testing.T) {
	adapter := 0
	for _, c := range everyKind {
		if c.kind.WrittenByAdapter() != c.adapter {
			t.Errorf("%s: WrittenByAdapter is %v, want %v", c.kind, !c.adapter, c.adapter)
		}
		if c.adapter {
			adapter++
		}
	}
	if adapter != 5 {
		t.Errorf("the Harness Adapter writes %d Kinds, want 5", adapter)
	}
	if daemon := len(everyKind) - adapter; daemon != 11 {
		t.Errorf("the Daemon writes %d Kinds, want 11", daemon)
	}
}

// An unknown Kind is kept as raw JSON rather than refused, so a Daemon writing a
// seventeenth Kind reaches this reader as a row it can draw.
func TestUnknownKindKeepsItsPayload(t *testing.T) {
	const frame = `{"seq":9414,"session":"s-7f3a2c","at":1756412093118000,"kind":"Seventeenth","payload":{"whatever":1}}`

	var got Event
	if err := json.Unmarshal([]byte(frame), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if raw, ok := got.Payload.(json.RawMessage); !ok || string(raw) != `{"whatever":1}` {
		t.Errorf("payload is %#v, want the raw bytes", got.Payload)
	}
}

// SessionEnded carries one of three reasons and nothing else.
func TestEndReasons(t *testing.T) {
	for _, want := range []string{"stopped", "failed", "lost"} {
		b, err := json.Marshal(SessionEnded{Reason: EndReason(want)})
		if err != nil {
			t.Fatal(err)
		}
		if got := string(b); got != `{"reason":"`+want+`"}` {
			t.Errorf("got %s, want reason %q", got, want)
		}
	}
	if EndStopped != "stopped" || EndFailed != "failed" || EndLost != "lost" {
		t.Error("the three reasons are stopped, failed and lost")
	}
}

// A frame with no payload key at all is read, not refused. The three Kinds that
// carry nothing are the ones a writer is most likely to send this way.
func TestAMissingPayloadIsNotAnError(t *testing.T) {
	var got Event
	if err := json.Unmarshal([]byte(`{"seq":1,"session":"s-1","at":1756412093118000,"kind":"HubAttached"}`), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Kind != KindHubAttached {
		t.Errorf("kind is %s, want %s", got.Kind, KindHubAttached)
	}
}
