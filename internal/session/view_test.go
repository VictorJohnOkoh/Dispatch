package session

import (
	"slices"
	"testing"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
)

func TestIncrementalFoldKeepsTheRuleAtRequestAndForgetsClosedCalls(t *testing.T) {
	var view View
	wait := event.Policy{event.RuleAuto, event.RuleWait, event.RuleWait, event.RuleAuto, event.RuleAuto}
	view.Apply(ready())
	view.Apply(ev(event.KindApprovalPolicySet, &event.ApprovalPolicySet{Policy: wait}))
	view.Apply(requested("c1"))
	wait[event.ToolEdit] = event.RuleAuto
	view.Apply(ev(event.KindApprovalPolicySet, &event.ApprovalPolicySet{Policy: wait}))
	kind, rule, ok := view.Call("c1")
	if !ok || kind != event.ToolEdit || rule != event.RuleWait {
		t.Fatalf("the earlier call changed with the policy: %v %q %v", kind, rule, ok)
	}
	view.Apply(asked("c1"))
	if state, _ := view.State(); state != Asking {
		t.Fatalf("state = %s", state)
	}
	held := view.Held()
	held[0] = "changed by caller"
	if !slices.Equal(view.Held(), []string{"c1"}) {
		t.Fatal("a caller changed the fold")
	}
	view.Apply(decided("c1"))
	view.Apply(closed("c1"))
	if _, _, ok := view.Call("c1"); ok || len(view.OpenCalls()) != 0 {
		t.Fatal("the fold retained a closed Tool Call")
	}
	view.Apply(ended(event.EndStopped))
	view.Apply(submitted())
	if state, reason := view.State(); state != Ended || reason != event.EndStopped {
		t.Fatalf("a terminal fold changed: %s %s", state, reason)
	}
}
