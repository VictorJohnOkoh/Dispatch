package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/harness"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"github.com/VictorJohnOkoh/Dispatch/internal/session"
)

// The five commands that act on a Session that already exists. Each one folds the
// Session's own Events to decide whether it may run, which is why session.Fold
// ships in the Daemon and not only in the Client.
//
// A command the State says no to answers StatusConflict. The request is fine and
// the moment is wrong. A command that could never be right whatever the State is
// answers StatusUnprocessable, and that is the Client's own bug or a stale form.

type promptRequest struct {
	Text string `json:"text"`
}

// submitPrompt writes PromptSubmitted and hands the text to the Harness. The Event
// is written first, so a second Prompt arriving behind this one folds Working and
// is refused rather than racing it to the Harness.
func (d *Daemon) submitPrompt(w http.ResponseWriter, r *http.Request) {
	var req promptRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Text == "" {
		refuse(w, protocol.StatusUnprocessable, protocol.Refusal{
			Reason: protocol.ReasonMalformed, Detail: "a Prompt with no text is not a Prompt",
		})
		return
	}

	d.commanding.Lock()
	s, run, ok := d.allow(w, r, session.Idle)
	if !ok {
		d.commanding.Unlock()
		return
	}
	_, err := d.write(s, event.KindPromptSubmitted, &event.PromptSubmitted{Text: req.Text})
	if err != nil {
		d.commanding.Unlock()
		logRefused(w)
		return
	}

	if err := run.Prompt(r.Context(), req.Text); err != nil {
		d.boundFailed(s, err)
		d.commanding.Unlock()
		http.Error(w, "the Harness would not take the Prompt", http.StatusInternalServerError)
		return
	}
	d.commanding.Unlock()
	w.WriteHeader(protocol.StatusAccepted)
}

// boundFailed closes a Prompt the Harness would not take. PromptSubmitted is
// already in the log, so an unbounded Prompt would leave the Session Working and
// refusing every Prompt after it.
//
// The caller holds d.commanding, so no command can overtake this boundary.
func (d *Daemon) boundFailed(s *Session, err error) {
	d.write(s, event.KindError, &event.Error{Code: event.ErrVendor, Message: err.Error()})
	d.write(s, event.KindPromptCompleted, &event.PromptCompleted{StopReason: event.StopError})
}

// interrupt abandons the Prompt in flight and keeps the Session. It returns once
// the Adapter has stopped reading, and the Adapter is what writes PromptCompleted,
// so the Session is Idle again by the time this answers.
func (d *Daemon) interrupt(w http.ResponseWriter, r *http.Request) {
	d.commanding.Lock()
	s, run, ok := d.allow(w, r, session.Working, session.Asking)
	d.commanding.Unlock()
	if !ok {
		return
	}

	if err := run.Interrupt(r.Context()); err != nil {
		d.log.Warn("an interrupt did not finish", "session", s.id, "err", err)
		http.Error(w, "the Harness would not interrupt", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(protocol.StatusAccepted)
}

// stopSession ends the Session. Everything it was doing is abandoned. A Prompt in
// flight gets no PromptCompleted and its message is left torn, because the user
// asked for this, so it is neither a fault nor a finish.
func (d *Daemon) stopSession(w http.ResponseWriter, r *http.Request) {
	d.commanding.Lock()
	s, run, ok := d.allow(w, r, session.Starting, session.Idle, session.Working, session.Asking)
	if !ok {
		d.commanding.Unlock()
		return
	}
	first := d.sessions.endOnce(s)
	d.commanding.Unlock()

	// A launch that failed reached the end first. The Session is ending either way,
	// so the caller got what it asked for.
	if !first {
		w.WriteHeader(protocol.StatusAccepted)
		return
	}

	// run is nil while the Session is Starting, and the ladder skips to step 4 for
	// it: there is nothing to say goodbye to and nothing open to close.
	d.ladder(s, run)
	d.write(s, event.KindSessionEnded, &event.SessionEnded{Reason: event.EndStopped})
	s.cancel()

	w.WriteHeader(protocol.StatusAccepted)
}

// policyRequest carries an Approval Policy that decodes strictly on its own. All
// five slots, and each one a Rule.
type policyRequest struct {
	Policy event.Policy `json:"policy"`
}

// setPolicy sets the Approval Policy. A slot the Harness cannot gate is refused
// rather than quietly turned into auto, because a policy that says wait and
// behaves like auto is the one lie this project cannot afford.
func (d *Daemon) setPolicy(w http.ResponseWriter, r *http.Request) {
	var req policyRequest
	if !decode(w, r, &req) {
		return
	}

	d.commanding.Lock()
	defer d.commanding.Unlock()

	s, _, ok := d.allow(w, r, session.Starting, session.Idle, session.Working, session.Asking)
	if !ok {
		return
	}
	if refusal := ungated(s.caps, req.Policy); refusal != nil {
		refuse(w, protocol.StatusUnprocessable, *refusal)
		return
	}

	if _, err := d.write(s, event.KindApprovalPolicySet, &event.ApprovalPolicySet{
		Policy: req.Policy, SetBy: event.SetByUser,
	}); err != nil {
		logRefused(w)
		return
	}
	w.WriteHeader(protocol.StatusAccepted)
}

// ungated reports why this Harness cannot honour this Approval Policy, or nil.
//
// ADR 0006 gives a Harness with no tools no Approval Policy at all, an absence
// rather than five slots that happen to be auto, so every Policy is refused rather
// than only the ones naming a slot it cannot gate.
func ungated(caps harness.Capabilities, p event.Policy) *protocol.Refusal {
	if !caps.Tools {
		return &protocol.Refusal{
			Reason: protocol.ReasonNoGate,
			Detail: "this Harness runs no tools, so it has no Approval Policy",
		}
	}
	for kind, rule := range p {
		if rule != event.RuleAuto && !caps.Gates[kind] {
			return &protocol.Refusal{
				Reason: protocol.ReasonNoGate,
				Detail: fmt.Sprintf("this Harness cannot hold a %s Tool Call, so that slot may only be %q",
					event.ToolKind(kind), event.RuleAuto),
			}
		}
	}
	return nil
}

type approvalRequest struct {
	ToolCallID string         `json:"toolCallId"`
	Decision   event.Decision `json:"decision"`

	// Always is the user answering this question and the next one of its class at
	// the same time, which is what "always allow" is. It flips one slot to auto and
	// writes the whole policy it produced.
	Always bool `json:"always"`
}

// decideApproval answers one held Tool Call. The decision is the record, the
// Adapter is released with it, and a refusal ends the Tool Call here, because a
// Tool Call the Daemon refused is over whatever the Harness says next.
func (d *Daemon) decideApproval(w http.ResponseWriter, r *http.Request) {
	var req approvalRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Decision != event.DecisionAllowed && req.Decision != event.DecisionRefused {
		refuse(w, protocol.StatusUnprocessable, protocol.Refusal{
			Reason: protocol.ReasonMalformed,
			Detail: fmt.Sprintf("%q is not a decision", req.Decision),
		})
		return
	}

	d.commanding.Lock()
	defer d.commanding.Unlock()

	s, _, ok := d.allow(w, r, session.Asking)
	if !ok {
		return
	}
	if !slices.Contains(d.sessions.held(s), req.ToolCallID) {
		refuse(w, protocol.StatusConflict, protocol.Refusal{
			Reason: protocol.ReasonNoQuestion,
			Detail: fmt.Sprintf("no held Tool Call %q is waiting on a decision", req.ToolCallID),
		})
		return
	}

	if req.Decision == event.DecisionRefused {
		d.refuse(s, req.ToolCallID, event.ByUser)
	} else if _, err := d.write(s, event.KindApprovalDecided, &event.ApprovalDecided{
		ToolCallID: req.ToolCallID, Decision: req.Decision, By: event.ByUser,
	}); err != nil {
		logRefused(w)
		return
	}
	d.sessions.tell(s, req.ToolCallID, req.Decision)

	// The flip comes after the decision, so this question was answered by the user
	// and the new policy is what the next Tool Call of that class meets.
	if req.Always && req.Decision == event.DecisionAllowed {
		d.flip(s, req.ToolCallID)
	}
	w.WriteHeader(protocol.StatusAccepted)
}

// flip is "always allow": the slot this Tool Call belongs to becomes auto, and the
// whole policy that produced goes in the log, because every value the Approval
// Policy ever holds is an ApprovalPolicySet.
func (d *Daemon) flip(s *Session, id string) {
	kind, ok := d.sessions.kindOf(s, id)
	if !ok {
		return
	}
	policy := d.sessions.policy(s)
	if policy[kind] == event.RuleAuto {
		return
	}
	policy[kind] = event.RuleAuto
	d.write(s, event.KindApprovalPolicySet, &event.ApprovalPolicySet{
		Policy: policy, SetBy: event.SetByUser,
	})
}

// allow finds the Session the path names and folds it. It answers the request
// itself when this Host has no such Session or when its State does not allow the
// command, and reports whether the caller may carry on.
//
// The caller holds d.commanding, so the State it is given is still true when it
// writes the Event that changes it.
func (d *Daemon) allow(w http.ResponseWriter, r *http.Request, states ...session.State) (*Session, harness.Run, bool) {
	id := event.SessionID(r.PathValue("session"))
	s, run, state := d.sessions.find(id)
	if s == nil {
		refuse(w, protocol.StatusNoSession, protocol.Refusal{
			Reason: protocol.ReasonUnknownSession,
			Detail: fmt.Sprintf("this Host has no Session %q", id),
		})
		return nil, nil, false
	}
	if !slices.Contains(states, state) {
		refuse(w, protocol.StatusConflict, protocol.Refusal{
			Reason: protocol.ReasonState,
			Detail: fmt.Sprintf("the Session is %s, and this command needs it to be %s", state, oneOf(states)),
		})
		return nil, nil, false
	}
	return s, run, true
}

// oneOf spells the States a command allows, for the sentence a refusal shows.
func oneOf(states []session.State) string {
	names := make([]string, len(states))
	for i, s := range states {
		names[i] = s.String()
	}
	if len(names) == 1 {
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// decode reads a command body, and answers the refusal itself when it cannot.
func decode(w http.ResponseWriter, r *http.Request, into any) bool {
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		refuse(w, protocol.StatusUnprocessable, protocol.Refusal{
			Reason: protocol.ReasonMalformed, Detail: err.Error(),
		})
		return false
	}
	return true
}

// logRefused is the Event log saying no, which is this Host failing rather than
// the request being wrong.
func logRefused(w http.ResponseWriter) {
	http.Error(w, "the Event log refused the write", http.StatusInternalServerError)
}
