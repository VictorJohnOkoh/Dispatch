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

// closeCalls writes the ToolCallEnded every open Tool Call is owed. A call whose
// question was just refused ended because of that refusal; any other was in flight
// and nothing observed its result.
//
// The two triggers reach this from two goroutines: the Prompt completes on the
// Adapter's reader and the Session ends on the request that stopped it. So the
// fold and the writes it decided on are one step, and whichever trigger fires
// first is the one that finds the call open.
func (d *Daemon) closeCalls(s *Session, refused []string) {
	d.closing.Lock()
	defer d.closing.Unlock()

	for _, call := range d.sessions.openCalls(s) {
		outcome := event.OutcomeUnknown
		if slices.Contains(refused, call) {
			outcome = event.OutcomeRefused
		}
		d.write(s, event.KindToolCallEnded, &event.ToolCallEnded{ToolCallID: call, Outcome: outcome})
	}
}
