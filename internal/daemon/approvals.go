package daemon

import (
	"context"
	"errors"
	"fmt"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/harness"
)

// The Approval Policy, as it runs. Five slots, one per ToolKind, always all set,
// and the Daemon is what holds a Tool Call while the user thinks.
//
// The one claim this makes is what the Daemon allowed. It never claims what the
// Harness ran: a refusal is written here, from the Daemon's own ApprovalDecided,
// and whatever the Harness says afterwards is corroboration.

// approve is harness.Sink.Approve, and it is the only call an Adapter makes that
// blocks. The Adapter is holding one Tool Call while this runs, and so is the
// Harness behind it.
func (d *Daemon) approve(ctx context.Context, s *Session, id, title, detail string) (event.Decision, error) {
	kind, ok := d.sessions.kindOf(s, id)
	if !ok {
		// A question about a Tool Call the Harness never announced. Nothing here can
		// say which slot it belongs to, and guessing would gate the wrong thing.
		//
		// The refusal is an Error rather than an ApprovalDecided, because there is no
		// Tool Call to decide about: writing one would invent the very thing that is
		// missing. The Adapter is told no, and the Session stays usable.
		msg := fmt.Sprintf("the Harness asked about the Tool Call %q and never requested it", id)
		d.write(s, event.KindError, &event.Error{Code: event.ErrAdapterFailed, Message: msg})
		return event.DecisionRefused, errors.New("daemon: " + msg)
	}

	switch d.sessions.policy(s)[kind] {
	case event.RuleAuto:
		d.write(s, event.KindApprovalDecided, &event.ApprovalDecided{
			ToolCallID: id, Decision: event.DecisionAllowed, By: event.ByPolicy,
		})
		return event.DecisionAllowed, nil

	case event.RuleRefuse:
		d.refuse(s, id, event.ByPolicy)
		return event.DecisionRefused, nil
	}

	// Everything else waits, and a slot that somehow holds no Rule waits with it,
	// because waiting is the side that asks a human rather than the side that runs
	// a tool unattended.

	// Wait. The question goes in the log, the Session folds to Asking, and the
	// answer is what releases this.
	answer := d.sessions.ask(s, id)
	defer d.sessions.answered(s, id)

	if _, err := d.write(s, event.KindApprovalRequested, &event.ApprovalRequested{
		ToolCallID: id, Title: title, Detail: detail,
	}); err != nil {
		return event.DecisionRefused, err
	}

	select {
	case decision := <-answer:
		return decision, nil
	case <-ctx.Done():
		// The Session is ending. Whoever ended it writes the refusal, because the
		// ladder is already doing exactly that.
		return event.DecisionRefused, ctx.Err()
	}
}

// refuse is the Daemon refusing one Tool Call, which is two Events and always both.
// The decision is the record, and the end is what the decision means: a Tool Call
// the Daemon refused is over, whatever the Harness reports about it afterwards.
func (d *Daemon) refuse(s *Session, id string, by event.DecidedBy) {
	d.write(s, event.KindApprovalDecided, &event.ApprovalDecided{
		ToolCallID: id, Decision: event.DecisionRefused, By: by,
	})
	d.endCall(s, id, event.OutcomeRefused, "")
}

// chosenPolicy is the Approval Policy a Session begins with: the user's, when the
// start named one, and the Host config's default clipped by the Gates otherwise.
// A start that named a slot the Harness cannot gate never reaches here, because
// that start was refused.
func (d *Daemon) chosenPolicy(caps harness.Capabilities, asked *event.Policy) (event.Policy, event.SetBy, bool) {
	if asked != nil {
		policy, has := startingPolicy(caps, *asked)
		return policy, event.SetByUser, has
	}
	policy, has := startingPolicy(caps, d.policyDefault)
	return policy, event.SetByDefault, has
}

// startingPolicy is the Approval Policy a Session begins with: the Host config's
// default, clipped by what this Harness can hold. A slot with no Gate is Auto,
// because a slot that says wait and behaves like auto is the one lie this project
// cannot afford.
//
// A Harness that runs no tools has no Approval Policy at all, which is an absence
// rather than five slots that happen to be Auto, so nothing is written for one.
func startingPolicy(caps harness.Capabilities, want event.Policy) (event.Policy, bool) {
	if !caps.Tools {
		return event.Policy{}, false
	}
	var policy event.Policy
	for kind := range policy {
		switch {
		case !caps.Gates[kind]:
			policy[kind] = event.RuleAuto
		case want[kind] == "":
			// A Host config that named no default gets ADR 0008's: auto for read and
			// wait for the rest.
			policy[kind] = event.RuleWait
			if event.ToolKind(kind) == event.ToolRead {
				policy[kind] = event.RuleAuto
			}
		default:
			policy[kind] = want[kind]
		}
	}
	return policy, true
}
