package eventlog

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// Latest is the highest Sequence Number this log ever allocated, and 0 when it
// allocated none. It is what hello carries, so a reader can tell a Cursor that is
// merely behind from one that is impossible.
func (l *Log) Latest() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.latest
}

// LogID is this log's identity. A reader whose Cursor came from a different one
// holds Sequence Numbers another log allotted, which would replay as quietly wrong
// history rather than as an error.
func (l *Log) LogID() string { return l.logID }

// Resume is the log as one reader joining it sees it: the identity, the highest
// Sequence Number allotted, and the appendable Events at or below it that are
// still taking text.
//
// The three are read as one moment, which is what lets a replay clipped at Latest
// and the Cursor it ends on describe the same log.
type Resume struct {
	LogID  string
	Latest uint64

	// Open is the Seqs still taking text, oldest first. A Cursor may not pass one,
	// and that lag is what makes an unfinished message replay whole.
	Open []uint64
}

// Cursor is where a reader that has read all of this view may resume: just below
// the oldest open message, or Latest when none is open.
func (r Resume) Cursor() protocol.Cursor {
	if len(r.Open) > 0 {
		return protocol.Cursor(r.Open[0] - 1)
	}
	return protocol.Cursor(r.Latest)
}

func (l *Log) Resume() Resume {
	l.mu.Lock()
	defer l.mu.Unlock()
	return Resume{LogID: l.logID, Latest: l.latest, Open: slices.Sorted(maps.Keys(l.open))}
}

// Cursor is where a reader may resume. Every GET answers with it beside its data,
// because a snapshot without one has a race in front of it: read the Sessions,
// open the stream at a live edge that has moved, and lose what fell between.
func (l *Log) Cursor() protocol.Cursor { return l.Resume().Cursor() }

// Replay reads every Event above a Cursor, oldest first, at most limit of them and
// none above upTo. It reads the five columns straight into the wire envelope, so
// nothing here parses a payload and an unknown Kind replays like any other.
//
// upTo is the moment the reader joined. Clipping there rather than at the live
// edge is what keeps the replay and the Resume it was planned from describing one
// log: everything above upTo reaches the reader on its subscription instead.
func (l *Log) Replay(after, upTo uint64, limit int) ([]protocol.Event, error) {
	rows, err := l.db.Query(
		`SELECT seq, session, at, kind, payload FROM events
		 WHERE seq > ? AND seq <= ? ORDER BY seq LIMIT ?`,
		after, upTo, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("eventlog: replay from %d: %w", after, err)
	}
	out, err := wireRows(rows, limit)
	if err != nil {
		return nil, fmt.Errorf("eventlog: replay from %d: %w", after, err)
	}
	return l.withOpenText(out)
}

// SessionPage is one page of one Session's Events, from after forward, at most
// limit of them.
//
// A message that is still open pages with everything that has arrived, and with
// complete false. It is not clipped at the last flush; see withOpenText.
func (l *Log) SessionPage(session event.SessionID, after uint64, limit int) ([]protocol.Event, error) {
	rows, err := l.db.Query(
		`SELECT seq, session, at, kind, payload FROM events
		 WHERE session = ? AND seq > ? ORDER BY seq LIMIT ?`,
		string(session), after, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("eventlog: page %s: %w", session, err)
	}
	out, err := wireRows(rows, limit)
	if err != nil {
		return nil, fmt.Errorf("eventlog: page %s: %w", session, err)
	}
	return l.withOpenText(out)
}

// withOpenText puts the text an open message holds now over the row it was last
// flushed to. A row is written every FlushThreshold bytes, so it lags what has
// arrived, and serving that lag would make the Cursor's own lag pointless: a
// reader resumes below an open message precisely so the message replays whole.
//
// This is the second place a read carries text that is not yet durable, the first
// being a Delta. It costs nothing a Delta did not already cost: a reader is handed
// the same bytes either way, and a crash between the last flush and the read still
// leaves the shorter message, because a crash is what the flush bounds. What it
// buys is that a reader which reattaches while the Daemon is alive sees the whole
// message instead of a blank row.
//
// A Delta names the position it writes at rather than appending, so a reader that
// is ahead of a Delta still in flight is put right by the Deltas that follow.
func (l *Log) withOpenText(events []protocol.Event) ([]protocol.Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for i, e := range events {
		message, ok := l.open[e.Seq]
		if !ok {
			continue
		}
		payload, err := json.Marshal(message.payload(false))
		if err != nil {
			return nil, fmt.Errorf("eventlog: %s payload at %d: %w", message.kind, e.Seq, err)
		}
		events[i].Payload = payload
	}
	return events, nil
}

// wireRows reads rows the read path forwards without understanding them. It closes
// rows, so every caller is one statement.
func wireRows(rows *sql.Rows, limit int) ([]protocol.Event, error) {
	defer rows.Close()

	out := make([]protocol.Event, 0, limit)
	for rows.Next() {
		var e protocol.Event
		var payload []byte
		if err := rows.Scan(&e.Seq, &e.Session, &e.At, &e.Kind, &payload); err != nil {
			return nil, err
		}
		e.Payload = payload
		out = append(out, e)
	}
	return out, rows.Err()
}

// SessionEvents reads one Session's whole log as typed Events. It is the one read
// that parses a payload, because the boot sweep has to fold a Session to know
// which of its Tool Calls are still open, and a Kind this build does not know
// keeps its payload as raw JSON rather than failing the read.
func (l *Log) SessionEvents(session event.SessionID) ([]event.Event, error) {
	rows, err := l.db.Query(
		`SELECT seq, session, at, kind, payload FROM events WHERE session = ? ORDER BY seq`,
		string(session),
	)
	if err != nil {
		return nil, fmt.Errorf("eventlog: read %s: %w", session, err)
	}
	defer rows.Close()

	var out []event.Event
	for rows.Next() {
		var seq uint64
		var id string
		var at int64
		var kind string
		var payload []byte
		if err := rows.Scan(&seq, &id, &at, &kind, &payload); err != nil {
			return nil, fmt.Errorf("eventlog: read %s: %w", session, err)
		}
		e, err := event.Decode(seq, event.SessionID(id), at, event.Kind(kind), json.RawMessage(payload))
		if err != nil {
			return nil, fmt.Errorf("eventlog: read %s: %w", session, err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
