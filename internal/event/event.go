// Package event holds the Event model: the Envelope every Event carries, the
// closed set of sixteen Event Kinds, and one payload type per Kind. It is a leaf
// package and imports nothing else in this project.
//
// ADR 0005 owns the model and ADR 0008 raised the Kind count to sixteen. ADR 0009
// owns the wire shape the JSON methods here produce.
package event

import (
	"encoding/json"
	"fmt"
	"time"
)

// SessionID names one Session. It is unique inside one Daemon and nowhere else.
type SessionID string

// Event is one normalised thing that happened inside a Session. The Envelope is
// these five fields whatever the Kind: there is no Harness field, no Host field
// and no version field.
type Event struct {
	// Seq is per Daemon, from 1, and never skips, so a reader detects a lost
	// Event by subtraction.
	Seq     uint64
	Session SessionID

	// At is the Daemon's clock, because it is the only one present on every path.
	At time.Time

	Kind Kind

	// Payload is the struct this Kind names, by pointer. It is json.RawMessage
	// after reading a Kind this build does not know.
	Payload any
}

// wireEvent is the Envelope as ADR 0009 frames it. At is Unix microseconds, which
// sorts and compares without parsing.
//
// ADR 0010 gives the read path its own untyped envelope, protocol.Event, which
// carries the payload as raw JSON so the Hub can forward a Kind it never heard of.
// This one is the typed spelling, and it is here because turning a Kind into its
// payload is Kind knowledge. The two meet in the SQLite row.
type wireEvent struct {
	Seq     uint64          `json:"seq"`
	Session SessionID       `json:"session"`
	At      int64           `json:"at"`
	Kind    Kind            `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

func (e Event) MarshalJSON() ([]byte, error) {
	payload, err := json.Marshal(e.Payload)
	if err != nil {
		return nil, fmt.Errorf("event %s payload: %w", e.Kind, err)
	}
	return json.Marshal(wireEvent{
		Seq:     e.Seq,
		Session: e.Session,
		At:      e.At.UnixMicro(),
		Kind:    e.Kind,
		Payload: payload,
	})
}

// UnmarshalJSON keeps an unknown Kind's payload as raw JSON rather than failing,
// which is ADR 0005's rule that a reader draws an unknown Kind as a neutral row.
func (e *Event) UnmarshalJSON(b []byte) error {
	var w wireEvent
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	decoded, err := Decode(w.Seq, w.Session, w.At, w.Kind, w.Payload)
	if err != nil {
		return err
	}
	*e = decoded
	return nil
}

// Decode builds a typed Event from the five fields the Envelope is, wherever they
// came from: the JSON above, or the five columns of a row. At is Unix
// microseconds, and an unknown Kind keeps its payload as raw JSON.
func Decode(seq uint64, session SessionID, at int64, kind Kind, payload json.RawMessage) (Event, error) {
	e := Event{Seq: seq, Session: session, At: time.UnixMicro(at).UTC(), Kind: kind, Payload: payload}

	typed := NewPayload(kind)
	if typed == nil || len(payload) == 0 {
		return e, nil
	}
	if err := json.Unmarshal(payload, typed); err != nil {
		return Event{}, fmt.Errorf("event %s payload: %w", kind, err)
	}
	e.Payload = typed
	return e, nil
}
