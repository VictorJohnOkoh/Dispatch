package daemon

import (
	"fmt"
	"slices"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/session"
)

// sweep is the boot sweep. A Session is one Harness process and the process does
// not fold, so a Session that was live when the Daemon died cannot be resumed: it
// is ended lost, with its transcript intact, and the Client offers a new Session
// in its place rather than a resume.
//
// It finishes before the listener answers. Otherwise a reconnecting Hub reads a
// Session that is Working in the log and dead in reality, and the Client draws a
// spinner for a process that no longer exists.
//
// Nothing joins the Session registry here. The registry is this run's live
// Sessions, and every Session this touches ended before the run began.
func (d *Daemon) sweep() error {
	lost, err := d.events.Sweep()
	if err != nil {
		return fmt.Errorf("daemon: the boot sweep could not read the Event log: %w", err)
	}
	for _, id := range lost {
		if err := d.endLost(id); err != nil {
			return err
		}
	}
	if len(lost) > 0 {
		d.log.Info("the boot sweep ended the Sessions the last run left open", "sessions", lost)
	}
	return nil
}

// endLost writes ADR 0008's restart sequence for one abandoned Session:
// DaemonStarted, then every open question refused, then every open Tool Call
// closed, then SessionEnded{lost}. A fold over that reaches Ended{lost} with no
// Tool Call left open, which is the invariant a Session that died mid-Prompt
// would otherwise break forever.
func (d *Daemon) endLost(id event.SessionID) error {
	events, err := d.events.SessionEvents(id)
	if err != nil {
		return fmt.Errorf("daemon: the boot sweep could not read %s: %w", id, err)
	}
	held, calls := session.Held(events), session.OpenCalls(events)

	w := lost{d: d, id: id}
	w.write(event.KindDaemonStarted, &event.NoPayload{})
	for _, call := range held {
		w.write(event.KindApprovalDecided, &event.ApprovalDecided{
			ToolCallID: call, Decision: event.DecisionRefused, By: event.ByDaemonRestart,
		})
	}
	for _, call := range calls {
		// A call whose question this sweep just refused ended because of that
		// refusal. Any other was in flight, and nothing observed its result.
		outcome := event.OutcomeUnknown
		if slices.Contains(held, call) {
			outcome = event.OutcomeRefused
		}
		w.write(event.KindToolCallEnded, &event.ToolCallEnded{ToolCallID: call, Outcome: outcome})
	}
	w.write(event.KindSessionEnded, &event.SessionEnded{Reason: event.EndLost})
	return w.err
}

// lost appends one abandoned Session's ending Events in order, keeping the first
// write that failed so that the sequence above reads as the sequence. It writes to
// the log and not through the Daemon, because the Session it is ending is not in
// the registry and never will be.
type lost struct {
	d   *Daemon
	id  event.SessionID
	err error
}

func (w *lost) write(kind event.Kind, payload any) {
	if w.err != nil {
		return
	}
	_, err := w.d.events.Append(event.Event{
		Session: w.id, At: time.Now().UTC(), Kind: kind, Payload: payload,
	})
	if err != nil {
		w.err = fmt.Errorf("daemon: the boot sweep could not end %s: %w", w.id, err)
	}
}
