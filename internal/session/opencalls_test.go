package session

import (
	"slices"
	"testing"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
)

func requested(toolCallID string) event.Event {
	return ev(event.KindToolCallRequested, &event.ToolCallRequested{
		ToolCallID: toolCallID, Name: "edit", ToolKind: event.ToolEdit,
	})
}

func closed(toolCallID string) event.Event {
	return ev(event.KindToolCallEnded, &event.ToolCallEnded{
		ToolCallID: toolCallID, Outcome: event.OutcomeOK,
	})
}

// Parallel calls leave several open at once, and each is closed by the one
// ToolCallEnded that names it.
func TestOpenCallsAreTheOnesNoToolCallEndedClosed(t *testing.T) {
	cases := []struct {
		name   string
		events []event.Event
		want   []string
	}{
		{"nothing asked", []event.Event{started(), ready()}, nil},
		{"one in flight", []event.Event{requested("a")}, []string{"a"}},
		{"one closed", []event.Event{requested("a"), closed("a")}, nil},
		{"two in flight, one closed", []event.Event{
			requested("a"), requested("b"), closed("a"),
		}, []string{"b"}},
		{"an ended Session holds nothing", []event.Event{
			requested("a"), ended(event.EndLost),
		}, nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := OpenCalls(c.events); !slices.Equal(got, c.want) {
				t.Errorf("OpenCalls = %v, want %v", got, c.want)
			}
		})
	}
}

// A repeated close cannot drive the list negative, which is why it is a list of
// ids rather than a counter.
func TestARepeatedCloseChangesNothing(t *testing.T) {
	events := []event.Event{requested("a"), closed("a"), closed("a")}
	if got := OpenCalls(events); len(got) != 0 {
		t.Errorf("OpenCalls = %v, want none", got)
	}
}
