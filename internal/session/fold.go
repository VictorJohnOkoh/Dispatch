package session

import (
	"slices"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
)

// View folds Events in Sequence Number order. It keeps only facts needed for
// current decisions, never transcript text or completed Tool Calls. The zero
// value is Starting; Apply is the only way to change it.
type View struct {
	ready, prompting, ended bool
	reason                  event.EndReason
	held                    []string
	calls                   []call
	policy                  event.Policy
}

type call struct {
	id   string
	kind event.ToolKind
	rule event.Rule
}

// Apply accepts each Event once, in order. Replay callers deduplicate by Seq.
func (v *View) Apply(e event.Event) {
	if v.ended {
		return
	}
	switch e.Kind {
	case event.KindSessionEnded:
		v.ended = true
		v.held, v.calls = nil, nil
		if p, ok := e.Payload.(*event.SessionEnded); ok {
			v.reason = p.Reason
		}
	case event.KindSessionReady:
		v.ready = true
	case event.KindPromptSubmitted:
		v.prompting = true
	case event.KindPromptCompleted:
		v.prompting = false
	case event.KindApprovalPolicySet:
		if p, ok := e.Payload.(*event.ApprovalPolicySet); ok {
			v.policy = p.Policy
		}
	case event.KindApprovalRequested:
		if p, ok := e.Payload.(*event.ApprovalRequested); ok {
			v.held = append(v.held, p.ToolCallID)
		}
	case event.KindApprovalDecided:
		if p, ok := e.Payload.(*event.ApprovalDecided); ok {
			v.held = slices.DeleteFunc(v.held, func(id string) bool { return id == p.ToolCallID })
		}
	case event.KindToolCallRequested:
		if p, ok := e.Payload.(*event.ToolCallRequested); ok {
			v.calls = append(v.calls, call{id: p.ToolCallID, kind: p.ToolKind, rule: v.policy[p.ToolKind]})
		}
	case event.KindToolCallEnded:
		if p, ok := e.Payload.(*event.ToolCallEnded); ok {
			v.calls = slices.DeleteFunc(v.calls, func(c call) bool { return c.id == p.ToolCallID })
		}
	}
}

func (v *View) State() (State, event.EndReason) {
	switch {
	case v.ended:
		return Ended, v.reason
	case len(v.held) > 0:
		return Asking, ""
	case v.prompting:
		return Working, ""
	case v.ready:
		return Idle, ""
	default:
		return Starting, ""
	}
}

func (v *View) Held() []string       { return slices.Clone(v.held) }
func (v *View) Policy() event.Policy { return v.policy }

func (v *View) OpenCalls() []string {
	ids := make([]string, len(v.calls))
	for i, c := range v.calls {
		ids[i] = c.id
	}
	return ids
}

// Call returns the rule in force when this open Tool Call was requested.
func (v *View) Call(id string) (event.ToolKind, event.Rule, bool) {
	for _, c := range v.calls {
		if c.id == id {
			return c.kind, c.rule, true
		}
	}
	return 0, "", false
}

func inspect(events []event.Event) View {
	var view View
	for _, e := range events {
		view.Apply(e)
	}
	return view
}

// Fold derives Session State from history using the same fold as live updates.
func Fold(events []event.Event) (State, event.EndReason) {
	view := inspect(events)
	return view.State()
}

func Held(events []event.Event) []string {
	view := inspect(events)
	return view.Held()
}

func OpenCalls(events []event.Event) []string {
	view := inspect(events)
	return view.OpenCalls()
}

func Policy(events []event.Event) event.Policy {
	view := inspect(events)
	return view.Policy()
}
