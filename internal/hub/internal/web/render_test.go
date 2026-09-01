package web

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// page.js draws the same row from a live Frame, so what is asserted here is what
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
			// JSON.stringify(null) is not what page.js draws here: it draws nothing,
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
