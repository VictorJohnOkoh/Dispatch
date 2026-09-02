package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"github.com/VictorJohnOkoh/Dispatch/internal/session"
)

// row is one Event as the page draws it. Text is the body a Delta may still add
// to, and only Reasoning and AssistantMessage have one, so a Delta finds its row
// by Seq. render.js draws the same row from a live Frame, and the two must agree.
type row struct {
	Seq        uint64
	Kind       string
	Title      string
	Text       string
	Detail     string
	Appendable bool

	// Inset is a row that belongs to the Prompt above it rather than to the
	// Session: a Tool Call, its question, its answer and its end.
	//
	// It is the second and last level. ToolCallRequested carries no parent id, so
	// no Client can draw a Tool Call inside another one without inventing a
	// relation no Event states, and this flag has no third value to invent it with.
	Inset bool

	// Tone colours the row's edge. It is empty on everything but a Tool Call that
	// ended, which is the one place the outcome is worth a colour.
	Tone string
}

// The tones a Tool Call ends in. Unknown is grey and not red: it means nobody
// reported a result, which is the Harness going quiet rather than a failure, and
// there is nothing for the user to do about it.
const (
	toneOK      = "ok"
	toneBad     = "bad"
	toneUnknown = "unknown"
	toneAsking  = "asking"
)

// rows draws the whole transcript. An Event that cannot be decoded at all is
// dropped rather than failing the page, because one bad row must not cost the
// user the rest of the Session.
func rows(events []protocol.Event) []row {
	out := make([]row, 0, len(events))
	for _, e := range events {
		decoded, err := event.Decode(e.Seq, event.SessionID(e.Session), e.At, event.Kind(e.Kind), e.Payload)
		if err != nil {
			continue
		}
		out = append(out, draw(decoded))
	}
	return out
}

// fold is the Session's State for the first paint, from the same Events the rows
// were drawn from. An Event that cannot be decoded is skipped, exactly as the
// rows skip it, so the two halves of the page describe the same list.
//
// The browser folds the same Events again with fold.js and keeps folding as more
// arrive. The two functions are the pair the shared fixture keeps honest; what it
// cannot check is this skip, because an Event this Hub cannot decode is one the
// browser folds from its raw payload anyway.
func fold(events []protocol.Event) (session.State, event.EndReason) {
	decoded := make([]event.Event, 0, len(events))
	for _, e := range events {
		one, err := event.Decode(e.Seq, event.SessionID(e.Session), e.At, event.Kind(e.Kind), e.Payload)
		if err != nil {
			continue
		}
		decoded = append(decoded, one)
	}
	return session.Fold(decoded)
}

// draw turns one Event into its row. An unknown Kind arrives here with its payload
// still raw and draws as a neutral row, which is the whole cost of the Hub knowing
// Kinds at all.
func draw(e event.Event) row {
	// The title starts from the Kind, so a Kind with no payload and a Kind this
	// build never heard of both have a line before the switch runs. render.js reads
	// the Kind and nothing else for those, and the two must not disagree because
	// one of them was written with an empty payload.
	r := row{Seq: e.Seq, Kind: string(e.Kind), Title: note(e.Kind)}
	switch p := e.Payload.(type) {
	case *event.SessionStarted:
		r.Title = "Session started"
		r.Detail = fmt.Sprintf("%s on %s via %s, in %s", p.Harness, p.Model, p.Vendor, p.Cwd)
	case *event.SessionReady:
		r.Title = "Session ready"
		r.Detail = p.Model
	case *event.ApprovalPolicySet:
		r.Title = "Approval policy, set by " + string(p.SetBy)
		r.Detail = policyLine(p.Policy)
	case *event.PromptSubmitted:
		r.Title = "Prompt"
		r.Text = p.Text
	case *event.Reasoning:
		r.Title = "Reasoning"
		r.Text, r.Appendable = p.Text, true
		r.Inset = true
	case *event.AssistantMessage:
		r.Title = "Assistant"
		r.Text, r.Appendable = p.Text, true
	case *event.ToolCallRequested:
		r.Title = "Tool call: " + p.Name
		r.Detail = strings.TrimSpace(p.Title + " " + args(p.Args))
		r.Inset = true
	case *event.ApprovalRequested:
		r.Title = "Approval requested: " + p.Title
		r.Detail = p.Detail
		r.Inset, r.Tone = true, toneAsking
	case *event.ApprovalDecided:
		r.Title = "Approval " + string(p.Decision)
		r.Detail = "decided by " + string(p.By)
		r.Inset = true
		if p.Decision == event.DecisionRefused {
			r.Tone = toneBad
		}
	case *event.ToolCallEnded:
		r.Title = "Tool call " + string(p.Outcome)
		r.Text = p.Content
		r.Inset, r.Tone = true, tone(p.Outcome)
	case *event.PromptCompleted:
		r.Title = "Prompt completed: " + string(p.StopReason)
		r.Detail = fmt.Sprintf("%d in, %d out, %d total", p.Usage.Input, p.Usage.Output, p.Usage.Total)
	case *event.Error:
		r.Title = "Error: " + string(p.Code)
		r.Detail = p.Message
	case *event.SessionEnded:
		r.Title = "Session ended: " + string(p.Reason)
	case json.RawMessage:
		r.Detail = compact(p)
	}
	return r
}

// tone is the colour one Tool Call's end carries. Unknown is grey rather than
// red, because nobody reporting a result is not the same as a failure.
func tone(outcome event.Outcome) string {
	switch outcome {
	case event.OutcomeOK:
		return toneOK
	case event.OutcomeUnknown:
		return toneUnknown
	default:
		return toneBad
	}
}

// compact spells a raw payload the way render.js spells it. The Hub forwards what
// the Harness sent, byte for byte, and JSON.stringify writes no space between a
// key and its value, so the same Event drawn here and drawn live would otherwise
// differ by whitespace alone.
func compact(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		// A payload that is not JSON still reaches the reader. This package draws
		// what it was given and never drops a row.
		return string(raw)
	}
	return out.String()
}

// args is a Tool Call's arguments, and nothing at all for a call that carries
// none. A Harness that sends the literal null means no arguments, and "null" on
// the line reads as an argument whose value is null.
func args(raw json.RawMessage) string {
	if spelt := compact(raw); spelt != "null" {
		return spelt
	}
	return ""
}

// note is the line for the three Kinds that carry no payload, and the Kind's own
// name for every other, which is what a neutral row is titled with.
func note(k event.Kind) string {
	switch k {
	case event.KindHubAttached:
		return "The Hub attached"
	case event.KindHubDetached:
		return "The Hub detached"
	case event.KindDaemonStarted:
		return "The Daemon started"
	}
	return string(k)
}

// policyLine spells all five slots, because the Approval Policy is always all
// five set and a line naming three of them would read as the other two being off.
func policyLine(p event.Policy) string {
	slots := make([]string, len(p))
	for i, rule := range p {
		slots[i] = event.ToolKind(i).String() + " " + string(rule)
	}
	return strings.Join(slots, ", ")
}
