package eventlog

import (
	"fmt"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
)

// Sweep names Sessions whose latest Run has not closed, scanning above the
// previous boot mark. An older SessionEnded does not close a later Run.
func (l *Log) Sweep() ([]event.SessionID, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	var mark uint64
	if err := l.db.QueryRow(`SELECT swept_to FROM meta WHERE id = 1`).Scan(&mark); err != nil {
		return nil, fmt.Errorf("eventlog: sweep: %w", err)
	}

	rows, err := l.db.Query(
		`SELECT session FROM events
         WHERE seq > ?1
         GROUP BY session
         HAVING MAX(seq) > MAX(CASE WHEN kind = 'SessionEnded' THEN seq ELSE 0 END)
         ORDER BY MIN(seq)`, mark,
	)
	if err != nil {
		return nil, fmt.Errorf("eventlog: sweep: %w", err)
	}
	defer rows.Close()

	var lost []event.SessionID
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("eventlog: sweep: %w", err)
		}
		lost = append(lost, event.SessionID(id))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("eventlog: sweep: %w", err)
	}

	// The mark is where the log stood before the caller writes the ends, so the
	// next boot rescans this run's own Events and finds them ended.
	if _, err := l.db.Exec(`UPDATE meta SET swept_to = ? WHERE id = 1`, l.latest); err != nil {
		return nil, fmt.Errorf("eventlog: sweep: %w", err)
	}
	return lost, nil
}
