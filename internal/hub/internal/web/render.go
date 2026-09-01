package web

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// row is one Event as the page draws it. Text is the body a Delta may still add
// to, and only Reasoning and AssistantMessage have one, so a Delta finds its row
// by Seq. page.js draws the same row from a live Frame, and the two must agree.
type row struct {
	Seq        uint64
	Kind       string
	Title      string
	Text       string
	Detail     string
	Appendable bool
}

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

// draw turns one Event into its row. An unknown Kind arrives here with its payload
// still raw and draws as a neutral row, which is the whole cost of the Hub knowing
// Kinds at all.
func draw(e event.Event) row {
	// The title starts from the Kind, so a Kind with no payload and a Kind this
	// build never heard of both have a line before the switch runs. page.js reads
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
	case *event.AssistantMessage:
		r.Title = "Assistant"
		r.Text, r.Appendable = p.Text, true
	case *event.ToolCallRequested:
		r.Title = "Tool call: " + p.Name
		r.Detail = strings.TrimSpace(p.Title + " " + string(p.Args))
	case *event.ApprovalRequested:
		r.Title = "Approval requested: " + p.Title
		r.Detail = p.Detail
	case *event.ApprovalDecided:
		r.Title = "Approval " + string(p.Decision)
		r.Detail = "decided by " + string(p.By)
	case *event.ToolCallEnded:
		r.Title = "Tool call " + string(p.Outcome)
		r.Text = p.Content
	case *event.PromptCompleted:
		r.Title = "Prompt completed: " + string(p.StopReason)
		r.Detail = fmt.Sprintf("%d in, %d out, %d total", p.Usage.Input, p.Usage.Output, p.Usage.Total)
	case *event.Error:
		r.Title = "Error: " + string(p.Code)
		r.Detail = p.Message
	case *event.SessionEnded:
		r.Title = "Session ended: " + string(p.Reason)
	case json.RawMessage:
		r.Detail = string(p)
	}
	return r
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
