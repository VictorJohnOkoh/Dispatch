package eventlog

import (
	"fmt"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
)

// Sweep names every Session the last run left without a SessionEnded, oldest
// first, and moves the mark to where the log stands now. The caller ends each
// Session it names, which is what makes the mark's invariant true again:
//
//	After a sweep, no Session at or below the mark is missing a SessionEnded.
//
// That invariant is why the scan is bounded by one Daemon's uptime rather than by
// the log's whole history, which matters in a log that is never pruned. Both
// halves of the query read above the mark for the same reason: SessionEnded is
// terminal and always last, so a Session that ended below the mark wrote nothing
// above it.
func (l *Log) Sweep() ([]event.SessionID, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	var mark uint64
	if err := l.db.QueryRow(`SELECT swept_to FROM meta WHERE id = 1`).Scan(&mark); err != nil {
		return nil, fmt.Errorf("eventlog: sweep: %w", err)
	}

	rows, err := l.db.Query(
		`SELECT session FROM events
		 WHERE seq > ?1 AND session NOT IN (
		   SELECT session FROM events WHERE kind = ?2 AND seq > ?1
		 )
		 GROUP BY session ORDER BY MIN(seq)`,
		mark, string(event.KindSessionEnded),
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
