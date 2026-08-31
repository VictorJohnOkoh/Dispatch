package eventlog

import (
	"fmt"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// Latest is the highest Sequence Number this log ever allocated, and 0 when it
// allocated none. It is what hello carries, so a reader can tell a Cursor that is
// merely behind from one that is impossible.
func (l *Log) Latest() (uint64, error) {
	var latest uint64
	if err := l.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM events`).Scan(&latest); err != nil {
		return 0, fmt.Errorf("eventlog: latest: %w", err)
	}
	return latest, nil
}

// SessionPage is one page of one Session's Events, from after forward, at most
// limit of them. It reads the five columns straight into the wire envelope, so
// nothing here parses a payload and an unknown Kind pages like any other.
//
// A message that is still open pages as far as the last flush got it, with
// complete false, which is the same torn message a crash would leave.
func (l *Log) SessionPage(session event.SessionID, after uint64, limit int) ([]protocol.Event, error) {
	rows, err := l.db.Query(
		`SELECT seq, session, at, kind, payload FROM events
		 WHERE session = ? AND seq > ? ORDER BY seq LIMIT ?`,
		string(session), after, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("eventlog: page %s: %w", session, err)
	}
	defer rows.Close()

	out := make([]protocol.Event, 0, limit)
	for rows.Next() {
		var e protocol.Event
		var payload []byte
		if err := rows.Scan(&e.Seq, &e.Session, &e.At, &e.Kind, &payload); err != nil {
			return nil, fmt.Errorf("eventlog: page %s: %w", session, err)
		}
		e.Payload = payload
		out = append(out, e)
	}
	return out, rows.Err()
}
