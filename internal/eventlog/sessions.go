package eventlog

import (
	"encoding/json"
	"fmt"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
)

// SessionRecord reads identity and lifetime from the Events that own them.
// Live Session State is folded by the Daemon.
type SessionRecord struct {
	ID        event.SessionID
	At        int64
	Started   event.SessionStarted
	EndReason event.EndReason
}

// Sessions lists history in start order without reading message or Tool Call payloads.
func (l *Log) Sessions() ([]SessionRecord, error) {
	rows, err := l.db.Query(`
		SELECT s.session, s.at, s.payload,
		       CASE WHEN e.kind = 'SessionEnded' THEN e.payload ELSE '' END
		FROM events s JOIN events e ON e.seq = (
		    SELECT seq FROM events WHERE session = s.session ORDER BY seq DESC LIMIT 1)
		WHERE s.kind = 'SessionStarted' ORDER BY s.seq`)
	if err != nil {
		return nil, fmt.Errorf("eventlog: list Sessions: %w", err)
	}
	defer rows.Close()
	all := make([]SessionRecord, 0)
	for rows.Next() {
		var record SessionRecord
		var start, end string
		if err := rows.Scan(&record.ID, &record.At, &start, &end); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(start), &record.Started); err != nil {
			return nil, err
		}
		if end != "" {
			var ended event.SessionEnded
			if err := json.Unmarshal([]byte(end), &ended); err != nil {
				return nil, err
			}
			record.EndReason = ended.Reason
		}
		all = append(all, record)
	}
	return all, rows.Err()
}

// HasSession distinguishes an ended Session from an id this log has never held.
func (l *Log) HasSession(id event.SessionID) (bool, error) {
	var exists bool
	err := l.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE session = ?)`, string(id)).Scan(&exists)
	return exists, err
}
