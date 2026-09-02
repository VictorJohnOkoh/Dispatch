package daemon

import (
	"slices"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
)

// Every Tool Call ends. When the Harness reports no result, the Daemon writes the
// end itself, and there are two triggers and no third: the Prompt completing, and
// the Session ending.
//
// The ledger is the Daemon's and not an Adapter's. Three Adapters keeping the
// same ledger is three chances to leak an open call, and synthesis is the one case
// where an Event describes an absence, so it belongs where absences are already
// handled.

// closeCalls writes the ToolCallEnded every open Tool Call is owed. Whatever is
// still open here was in flight, and nothing observed its result: a call the
// Daemon refused was ended by that refusal and is not open any more.
//
// The two triggers reach this from two goroutines: the Prompt completes on the
// Adapter's reader and the Session ends on the request that stopped it. So the
// fold and the writes it decided on are one step, and whichever trigger fires
// first is the one that finds the call open.
func (d *Daemon) closeCalls(s *Session) {
	d.closing.Lock()
	defer d.closing.Unlock()

	for _, call := range d.sessions.openCalls(s) {
		d.write(s, event.KindToolCallEnded, &event.ToolCallEnded{
			ToolCallID: call, Outcome: event.OutcomeUnknown,
		})
	}
}

// endCall closes one Tool Call, and only one that is still open. Every end goes
// through here, under the same lock as the two triggers, so a call cannot be
// ended twice by two of the three things that end one.
//
// A call that is not open was closed by the Daemon's own decision, and what the
// Harness reports about it afterwards does not overwrite that.
func (d *Daemon) endCall(s *Session, id string, outcome event.Outcome, content string) {
	d.closing.Lock()
	defer d.closing.Unlock()

	if !slices.Contains(d.sessions.openCalls(s), id) {
		// The operational log, because there is no Event to write: the Daemon already
		// decided how this call ended and this is the Harness disagreeing after the
		// fact.
		d.log.Info("a Tool Call the Daemon had already closed was ended again",
			"session", s.id, "toolCall", id, "outcome", outcome)
		return
	}
	d.write(s, event.KindToolCallEnded, &event.ToolCallEnded{
		ToolCallID: id, Outcome: outcome, Content: content,
	})
}
