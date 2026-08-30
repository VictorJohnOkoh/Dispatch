package event

import (
	"encoding/json"
	"fmt"
)

// ToolKind is the closed set of five classes a Tool Call falls into. It is an
// integer so that a fixed array can be indexed by it, which is what ADR 0006's
// Gates and the Approval Policy below both are.
type ToolKind uint8

const (
	ToolRead ToolKind = iota
	ToolEdit
	ToolExecute
	ToolFetch
	ToolOther
)

// NumToolKinds is the size of that set, and so the length of every array indexed
// by a ToolKind.
const NumToolKinds = 5

var toolKindNames = [NumToolKinds]string{"read", "edit", "execute", "fetch", "other"}

func (k ToolKind) String() string {
	if int(k) >= NumToolKinds {
		return fmt.Sprintf("ToolKind(%d)", uint8(k))
	}
	return toolKindNames[k]
}

func (k ToolKind) MarshalJSON() ([]byte, error) {
	if int(k) >= NumToolKinds {
		return nil, fmt.Errorf("event: no such ToolKind %d", uint8(k))
	}
	return json.Marshal(toolKindNames[k])
}

func (k *ToolKind) UnmarshalJSON(b []byte) error {
	var name string
	if err := json.Unmarshal(b, &name); err != nil {
		return err
	}
	for i, n := range toolKindNames {
		if n == name {
			*k = ToolKind(i)
			return nil
		}
	}
	return fmt.Errorf("event: no such ToolKind %q", name)
}

// Rule is what the Approval Policy does with one class of Tool Call.
type Rule string

const (
	Auto   Rule = "auto"   // run it without asking
	Wait   Rule = "wait"   // hold it and ask the user
	Refuse Rule = "refuse" // refuse it without asking
)

// Policy is the Approval Policy: one Rule per ToolKind, always all five set. It
// guarantees what the Daemon allowed, never what the Harness ran.
type Policy [NumToolKinds]Rule

// policySlots is the Approval Policy as it appears on the wire: an object keyed by
// ToolKind name. An array would be positional, and a positional set of five is the
// kind of thing that goes wrong silently.
type policySlots struct {
	Read    Rule `json:"read"`
	Edit    Rule `json:"edit"`
	Execute Rule `json:"execute"`
	Fetch   Rule `json:"fetch"`
	Other   Rule `json:"other"`
}

func (p Policy) MarshalJSON() ([]byte, error) {
	return json.Marshal(policySlots{
		Read:    p[ToolRead],
		Edit:    p[ToolEdit],
		Execute: p[ToolExecute],
		Fetch:   p[ToolFetch],
		Other:   p[ToolOther],
	})
}

// UnmarshalJSON refuses a policy with a slot missing, because the five are always
// all set.
func (p *Policy) UnmarshalJSON(b []byte) error {
	var s policySlots
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	slots := Policy{s.Read, s.Edit, s.Execute, s.Fetch, s.Other}
	for i, rule := range slots {
		if rule == "" {
			return fmt.Errorf("event: Approval Policy has no %s slot", toolKindNames[i])
		}
	}
	*p = slots
	return nil
}
