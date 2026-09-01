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
	view := inspect(events)
	return view.state, view.reason
}

// Held is the Tool Calls whose question no ApprovalDecided has answered. Parallel
// calls can leave several open at once, so it is a list of ids rather than a
// counter that a repeated decision could drive negative.
//
// It is the set Fold answers Asking on, and it is also what the Daemon reads to
// tell a decision on an open question from one on a question nobody asked.
func Held(events []event.Event) []string {
	return inspect(events).held
}

// OpenCalls is the Tool Calls no ToolCallEnded has closed, oldest first. ADR 0005
// promises exactly one ToolCallEnded for every ToolCallRequested, and a Session
// that died mid-Prompt has none for the calls that were in flight, so the boot
// sweep reads this to close them.
func OpenCalls(events []event.Event) []string {
	return inspect(events).calls
}

type inspection struct {
	state  State
	reason event.EndReason
	held   []string
	calls  []string
}

func inspect(events []event.Event) inspection {
	view := inspection{}
	var ready, prompting bool
	for _, e := range events {
		switch e.Kind {
		case event.KindSessionEnded:
			view.state = Ended
			view.held = nil
			view.calls = nil
			if p, ok := e.Payload.(*event.SessionEnded); ok {
				view.reason = p.Reason
			}
			return view

		case event.KindSessionReady:
			ready = true

		case event.KindPromptSubmitted:
			prompting = true

		case event.KindPromptCompleted:
			prompting = false

		case event.KindApprovalRequested:
			if p, ok := e.Payload.(*event.ApprovalRequested); ok {
				view.held = append(view.held, p.ToolCallID)
			}

		case event.KindApprovalDecided:
			if p, ok := e.Payload.(*event.ApprovalDecided); ok {
				view.held = remove(view.held, p.ToolCallID)
			}

		case event.KindToolCallRequested:
			if p, ok := e.Payload.(*event.ToolCallRequested); ok {
				view.calls = append(view.calls, p.ToolCallID)
			}

		case event.KindToolCallEnded:
			if p, ok := e.Payload.(*event.ToolCallEnded); ok {
				view.calls = remove(view.calls, p.ToolCallID)
			}
		}
	}
	switch {
	case len(view.held) > 0:
		view.state = Asking
	case prompting:
		view.state = Working
	case ready:
		view.state = Idle
	default:
		view.state = Starting
	}
	return view
}

func remove(ids []string, target string) []string {
	return slices.DeleteFunc(ids, func(id string) bool { return id == target })
}
