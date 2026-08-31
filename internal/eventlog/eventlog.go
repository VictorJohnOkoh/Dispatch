// Package eventlog is the Daemon's Event log: a SQLite file with write-ahead
// logging, one events table whose five columns are the wire shape, and a Sequence
// Number the insert allocates so that it never skips.
//
// An Event is committed before it is sent, which is the whole reason a reader can
// trust a Cursor.
//
// ADR 0009 owns the schema. AppendText and Subscribe are not here yet, and nor
// are Cursor, Replay and Sweep.
package eventlog

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"

	_ "modernc.org/sqlite"
)

// schema is applied on every Open. Each statement is guarded, so opening a file
// that already holds Events changes nothing.
const schema = `
CREATE TABLE IF NOT EXISTS events (
  seq     INTEGER PRIMARY KEY,
  session TEXT    NOT NULL,
  at      INTEGER NOT NULL,
  kind    TEXT    NOT NULL,
  payload TEXT    NOT NULL
) STRICT;

CREATE INDEX IF NOT EXISTS events_by_session ON events (session, seq);
`

// Log is one Daemon's Event log. Every write holds the mutex, so one insert
// allocates its Sequence Number and commits before the next one starts.
type Log struct {
	db *sql.DB

	mu sync.Mutex
}

// Open opens the log at path, creating the file and the schema if they are not
// there. The pragmas are on the connection string so that every connection the
// pool opens carries them.
func Open(path string) (*Log, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, fmt.Errorf("eventlog: open %s: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("eventlog: schema %s: %w", path, err)
	}
	return &Log{db: db}, nil
}

func (l *Log) Close() error { return l.db.Close() }

// Append writes one Event and returns it carrying its Sequence Number. The insert
// allocates that number as the row's own key, which is the highest one in the
// table plus one, so it starts at 1 and skips nothing while nothing is deleted. A
// rolled back insert consumes no number.
//
// It returns after the commit, so a caller that sends the Event it gets back is
// sending something the log already holds.
func (l *Log) Append(e event.Event) (event.Event, error) {
	payload, err := json.Marshal(e.Payload)
	if err != nil {
		return event.Event{}, fmt.Errorf("eventlog: %s payload: %w", e.Kind, err)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	res, err := l.db.Exec(
		`INSERT INTO events (session, at, kind, payload) VALUES (?, ?, ?, ?)`,
		string(e.Session), e.At.UnixMicro(), string(e.Kind), string(payload),
	)
	if err != nil {
		return event.Event{}, fmt.Errorf("eventlog: append %s: %w", e.Kind, err)
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return event.Event{}, fmt.Errorf("eventlog: append %s: %w", e.Kind, err)
	}

	e.Seq = uint64(seq)
	return e, nil
}
