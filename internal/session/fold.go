package session

import "github.com/VictorJohnOkoh/Dispatch/internal/event"

// Fold derives a Session's State from that Session's own Events, in Seq order. It
// returns the end reason with Ended and no reason with any other state.
//
// A slice with no SessionStarted folds to Starting, which is what the Session will
// be the moment its first Event lands.
func Fold(events []event.Event) (State, event.EndReason) {
	var ready, prompting bool

	// The Tool Calls whose question no ApprovalDecided has answered. Parallel calls
	// can leave several open at once, and the count is unknown, so it is a set of
	// ids rather than a counter that a repeated decision could drive negative.
	var open map[string]struct{}

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

		case event.KindApprovalRequested:
			if p, ok := e.Payload.(*event.ApprovalRequested); ok {
				if open == nil {
					open = make(map[string]struct{})
				}
				open[p.ToolCallID] = struct{}{}
			}

		case event.KindApprovalDecided:
			if p, ok := e.Payload.(*event.ApprovalDecided); ok {
				delete(open, p.ToolCallID)
			}
		}
	}

	// Asking is more specific than Working and both folds are true at once, so it
	// is answered first.
	switch {
	case len(open) > 0:
		return Asking, ""
	case prompting:
		return Working, ""
	case ready:
		return Idle, ""
	}
	return Starting, ""
}
