// Package session holds Session State and the fold that derives it. A Session's
// whole state is derived by folding its own Events and never stored, so every
// transition is caused by an Event and none is internal.
//
// The same fold runs in the Daemon and in the Client. It ships here rather than
// with the Client because the Daemon behaves differently per state: it folds in
// order to refuse a Prompt on a Starting Session. testdata/fold.json is the
// specification both folds are tested against, and that shared file is the only
// thing keeping the Go one and the JavaScript one honest with each other.
//
// ADR 0008 owns the states and the transitions.
package session

import (
	"encoding/json"
	"fmt"
)

// State is what a Session is doing now. It is an integer because the set is closed
// and ordered, and it travels by name, which is how the fixture spells it.
type State uint8

const (
	Starting State = iota // launching. No Prompt may be submitted
	Idle                  // up, nothing in flight. A Prompt may be submitted
	Working               // a Prompt is in flight. A second Prompt is refused
	Asking                // a Tool Call is held, waiting for the user's decision
	Ended                 // terminal, carrying stopped, failed or lost
)

const numStates = 5

var stateNames = [numStates]string{"Starting", "Idle", "Working", "Asking", "Ended"}

func (s State) String() string {
	if int(s) >= numStates {
		return fmt.Sprintf("State(%d)", uint8(s))
	}
	return stateNames[s]
}

func (s State) MarshalJSON() ([]byte, error) {
	if int(s) >= numStates {
		return nil, fmt.Errorf("session: no such State %d", uint8(s))
	}
	return json.Marshal(stateNames[s])
}

func (s *State) UnmarshalJSON(b []byte) error {
	var name string
	if err := json.Unmarshal(b, &name); err != nil {
		return err
	}
	for i, n := range stateNames {
		if n == name {
			*s = State(i)
			return nil
		}
	}
	return fmt.Errorf("session: no such State %q", name)
}
