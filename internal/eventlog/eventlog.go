// Package eventlog is the Daemon's Event log: a SQLite file with write-ahead
// logging, one events table whose five columns are the wire shape, and a Sequence
// Number the insert allocates so that it never skips.
//
// An Event is committed before it is sent, which is the whole reason a reader can
// trust a Cursor. A Delta is the one exception: it may carry up to 4 KiB of text
// the file does not hold yet.
//
// ADR 0009 owns the schema. Cursor, Replay and Sweep are not here yet.
package eventlog

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"

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

// FlushThreshold is how much new text an open message holds before it is written
// out again. It is a size and not a duration because a size bounds the loss in the
// unit a user cares about, and it needs no knob: a fast Model flushes often and a
// slow one rarely.
const FlushThreshold = 4 << 10

// subscriberBuffer is how far behind a subscriber may fall before the log drops
// it. It is large enough for a reader that is still draining its range scan, and
// small enough that a reader which stopped altogether is found quickly.
const subscriberBuffer = 256

// Frame is one thing the log fans out. Exactly one field is set: an Event, or a
// Delta for an Event the subscriber already has.
type Frame struct {
	Event *event.Event
	Delta *protocol.Delta
}

// openMessage is one appendable Event whose text is still arriving. The text is
// held here, and stored is how much of it the row holds.
type openMessage struct {
	session event.SessionID
	kind    event.Kind
	text    string
	stored  int
}

type subscriber struct {
	frames chan Frame
	closed bool
}

// Log is one Daemon's Event log. Every write holds the mutex, so one insert
// allocates its Sequence Number and commits before the next one starts, and the
// Frames reach every subscriber in that same order.
type Log struct {
	db *sql.DB

	mu   sync.Mutex
	open map[uint64]*openMessage
	subs []*subscriber
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
	return &Log{db: db, open: make(map[uint64]*openMessage)}, nil
}

func (l *Log) Close() error { return l.db.Close() }

// Append writes one Event and returns it carrying its Sequence Number. The insert
// allocates that number as the row's own key, which is the highest one in the
// table plus one, so it starts at 1 and skips nothing while nothing is deleted. A
// rolled back insert consumes no number.
//
// It returns after the commit, so a caller that sends the Event it gets back is
// sending something the log already holds.
//
// An appendable Event that arrives incomplete stays open from here until its final
// Delta or its Session's end, whichever comes first.
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

	if text, isOpen := openingText(e); isOpen {
		l.open[e.Seq] = &openMessage{session: e.Session, kind: e.Kind, text: text, stored: len(text)}
	}
	if e.Kind == event.KindSessionEnded {
		if err := l.endSession(e.Session); err != nil {
			return event.Event{}, err
		}
	}

	l.publish(Frame{Event: &e})
	return e, nil
}

// AppendText adds text to an open message and returns the Delta that carries it.
// The row is written again each time FlushThreshold bytes of new text have
// arrived, so a killed writer leaves a torn message rather than an empty one.
//
// The final call writes the whole text, marks the message complete and closes it.
// That Delta carries the whole text as well, and it replaces rather than appends,
// so a subscriber that dropped a Delta repairs itself.
func (l *Log) AppendText(seq uint64, text string, final bool) (protocol.Delta, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	message, ok := l.open[seq]
	if !ok {
		return protocol.Delta{}, fmt.Errorf("eventlog: append text: no open message at %d", seq)
	}

	delta := protocol.Delta{Seq: seq, N: len(message.text), Text: text, Final: final}
	message.text += text
	if final {
		delta.N, delta.Text = len(message.text), message.text
		delete(l.open, seq)
	}

	if final || len(message.text)-message.stored >= FlushThreshold {
		if err := l.store(seq, message, final); err != nil {
			return protocol.Delta{}, err
		}
	}

	l.publish(Frame{Delta: &delta})
	return delta, nil
}

// Subscribe returns a channel of live Frames and the function that ends the
// subscription. The log never reads that channel: the caller owns the goroutine at
// the other end. The channel is closed when the subscription ends, either by that
// function or because the subscriber fell too far behind.
//
// A reader subscribes first and reads the log second, so nothing written between
// the two is lost.
func (l *Log) Subscribe() (<-chan Frame, func()) {
	l.mu.Lock()
	defer l.mu.Unlock()

	sub := &subscriber{frames: make(chan Frame, subscriberBuffer)}
	l.subs = append(l.subs, sub)

	return sub.frames, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.drop(sub)
	}
}

// publish fans one Frame out to every subscriber and drops the ones whose buffer
// is full. A dropped reader reattaches on a Cursor and replays, so a drop costs
// one reconnect, and blocking here would stall every write on the Host.
func (l *Log) publish(f Frame) {
	live := l.subs[:0]
	for _, sub := range l.subs {
		select {
		case sub.frames <- f:
			live = append(live, sub)
		default:
			sub.closed = true
			close(sub.frames)
		}
	}
	l.subs = live
}

// drop ends one subscription. It is safe on a subscriber publish already dropped,
// which is what lets a caller defer its stop function. The caller holds the mutex.
func (l *Log) drop(sub *subscriber) {
	if sub.closed {
		return
	}
	for i, other := range l.subs {
		if other == sub {
			l.subs = append(l.subs[:i], l.subs[i+1:]...)
			break
		}
	}
	sub.closed = true
	close(sub.frames)
}

// endSession closes every message this Session left open. The text each one had is
// written out and complete stays false, so the Client draws a torn message. The
// caller holds the mutex.
func (l *Log) endSession(session event.SessionID) error {
	for seq, message := range l.open {
		if message.session != session {
			continue
		}
		delete(l.open, seq)
		if message.stored == len(message.text) {
			continue
		}
		if err := l.store(seq, message, false); err != nil {
			return err
		}
	}
	return nil
}

// store writes an open message's text over its row. The caller holds the mutex.
func (l *Log) store(seq uint64, message *openMessage, complete bool) error {
	payload, err := json.Marshal(messagePayload(message.kind, message.text, complete))
	if err != nil {
		return fmt.Errorf("eventlog: %s payload: %w", message.kind, err)
	}
	if _, err := l.db.Exec(`UPDATE events SET payload = ? WHERE seq = ?`, string(payload), seq); err != nil {
		return fmt.Errorf("eventlog: flush %s at %d: %w", message.kind, seq, err)
	}
	message.stored = len(message.text)
	return nil
}

// openingText returns an appendable Event's text and whether it leaves the Event
// open. A message that arrives complete opens nothing.
func openingText(e event.Event) (string, bool) {
	switch payload := e.Payload.(type) {
	case *event.AssistantMessage:
		return payload.Text, !payload.Complete
	case *event.Reasoning:
		return payload.Text, !payload.Complete
	}
	return "", false
}

func messagePayload(kind event.Kind, text string, complete bool) any {
	if kind == event.KindReasoning {
		return &event.Reasoning{Text: text, Complete: complete}
	}
	return &event.AssistantMessage{Text: text, Complete: complete}
}
