package session

import (
	"slices"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
)

// Fold derives a Session's State from that Session's own Events, in Seq order. It
// returns the end reason with Ended and no reason with any other state.
//
// A slice with no SessionStarted folds to Starting, which is what the Session will
// be the moment its first Event lands.
func Fold(events []event.Event) (State, event.EndReason) {
	var ready, prompting bool

	for _, e := range events {
		switch e.Kind {
		case event.KindSessionEnded:
			// Terminal and always last, so nothing after it can change the answer.
			var reason event.EndReason
			if p, ok := e.Payload.(*event.SessionEnded); ok {
				reason = p.Reason
			}
			return Ended, reason

		case event.KindSessionReady:
			ready = true

		case event.KindPromptSubmitted:
			prompting = true

		case event.KindPromptCompleted:
			prompting = false
		}
	}

	// Asking is more specific than Working and both folds are true at once, so it
	// is answered first.
	switch {
	case len(Held(events)) > 0:
		return Asking, ""
	case prompting:
		return Working, ""
	case ready:
		return Idle, ""
	}
	return Starting, ""
}

// Held is the Tool Calls whose question no ApprovalDecided has answered. Parallel
// calls can leave several open at once, so it is a list of ids rather than a
// counter that a repeated decision could drive negative.
//
// It is the set Fold answers Asking on, and it is also what the Daemon reads to
// tell a decision on an open question from one on a question nobody asked.
func Held(events []event.Event) []string {
	var open []string
	for _, e := range events {
		switch e.Kind {
		case event.KindSessionEnded:
			// Terminal, and an ended Session holds nothing.
			return nil

		case event.KindApprovalRequested:
			if p, ok := e.Payload.(*event.ApprovalRequested); ok {
				open = append(open, p.ToolCallID)
			}

		case event.KindApprovalDecided:
			if p, ok := e.Payload.(*event.ApprovalDecided); ok {
				open = slices.DeleteFunc(open, func(id string) bool { return id == p.ToolCallID })
			}
		}
	}
	return open
}
