package daemon

import (
	"slices"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
)

// Every Tool Call ends. A ToolCallRequested is correlated to its ToolCallEnded by
// tool call id, and when the Harness reports no result the Daemon writes the end
// itself. That promise is what lets the Client draw a call as running without
// ever having to time one out.
//
// There are two triggers and no third: the Prompt completing, and the Session
// ending. Whichever fires first closes the call, and the other finds nothing
// open, because the open calls are folded out of the Session's own Events rather
// than counted in a field a second trigger could drive negative.
//
// The ledger is the Daemon's and not an Adapter's. Three Adapters keeping the
// same ledger is three chances to leak an open call, and synthesis is the one
// case where an Event describes an absence, so it belongs where absences are
// already handled.

// closeCalls writes the ToolCallEnded every open Tool Call is owed. A call whose
// question was just refused ended because of that refusal; any other was in
// flight and nothing observed its result.
func (d *Daemon) closeCalls(s *Session, refused []string) {
	for _, call := range d.sessions.openCalls(s) {
		outcome := event.OutcomeUnknown
		if slices.Contains(refused, call) {
			outcome = event.OutcomeRefused
		}
		d.write(s, event.KindToolCallEnded, &event.ToolCallEnded{ToolCallID: call, Outcome: outcome})
	}
}
