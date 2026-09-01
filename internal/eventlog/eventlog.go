// Package eventlog is the Daemon's Event log: a SQLite file with write-ahead
// logging, one events table whose five columns are the wire shape, and a Sequence
// Number the insert allocates so that it never skips.
//
// An Event is committed before it is sent, which is the whole reason a reader can
// trust a Cursor. A Delta is the one exception: it may carry up to 4 KiB of text
// the file does not hold yet.
//
// Nothing is ever deleted, so a Cursor is never too old and there is no retention
// pass anywhere in this package.
//
// ADR 0009 owns the schema.
package eventlog

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
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

CREATE TABLE IF NOT EXISTS meta (
  id       INTEGER PRIMARY KEY CHECK (id = 1),
  log_id   TEXT    NOT NULL,
  swept_to INTEGER NOT NULL
) STRICT;
`

// FlushThreshold is how much new text an open message holds before it is written
// out again.
const FlushThreshold = 4 << 10

// subscriberBuffer is how far behind a subscriber may fall before the log drops it.
const subscriberBuffer = 256

// Frame is one thing the log fans out, and the Daemon sends each one as the
// protocol.Frame of the same name. Exactly one field is set: an Event, or a Delta
// for an Event the subscriber already has.
//
// Every subscriber is handed the same Frame, so a reader reads it and never
// writes to it.
type Frame struct {
	Event *event.Event
	Delta *protocol.Delta

	// Open says this Event is appendable and still taking text. A Cursor may not
	// pass it until its final Delta, so a reader stamps no id on it.
	Open bool
}

// openMessage is one appendable Event whose text is still arriving. The text is
// held here, and stored is how much of it the row holds.
type openMessage struct {
	session event.SessionID
	kind    event.Kind
	text    []byte
	stored  int
}

// payload builds the row's payload from the text held so far. Only the two Kinds
// openingText admits ever reach here, and Reasoning is the one that is not the
// default.
func (m *openMessage) payload(complete bool) any {
	if m.kind == event.KindReasoning {
		return &event.Reasoning{Text: string(m.text), Complete: complete}
	}
	return &event.AssistantMessage{Text: string(m.text), Complete: complete}
}

type subscriber struct {
	frames chan Frame
	closed bool
}

// end closes the channel once. The log never reads it again after this.
func (s *subscriber) end() {
	if s.closed {
		return
	}
	s.closed = true
	close(s.frames)
}

// Log is one Daemon's Event log. Every write holds the mutex, so one insert
// allocates its Sequence Number and commits before the next one starts, and the
// Frames reach every subscriber in that same order.
type Log struct {
	db *sql.DB

	// logID is this file's identity, written once when it is created. A Sequence
	// Number means nothing without it: a log that was deleted, restored or
	// reimaged starts at 1 again while a reader still holds a Cursor of 900.
	logID string

	mu   sync.Mutex
	open map[uint64]*openMessage
	subs []*subscriber

	// latest is the highest Sequence Number this log has allotted, reloaded from
	// the file on Open. It is held here rather than queried so that it and the open
	// messages below it are read as one moment.
	latest uint64
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

	l := &Log{db: db, open: make(map[uint64]*openMessage)}
	if err := l.identify(); err != nil {
		db.Close()
		return nil, fmt.Errorf("eventlog: open %s: %w", path, err)
	}
	return l, nil
}

// identify writes the meta row if the file has none and reads back the identity
// and the counter. The insert is ignored on a file that already has one, so a log
// keeps the identity it was created with for as long as the file exists.
func (l *Log) identify() error {
	var id [4]byte
	rand.Read(id[:])
	if _, err := l.db.Exec(
		`INSERT OR IGNORE INTO meta (id, log_id, swept_to) VALUES (1, ?, 0)`,
		hex.EncodeToString(id[:]),
	); err != nil {
		return err
	}
	if err := l.db.QueryRow(`SELECT log_id FROM meta WHERE id = 1`).Scan(&l.logID); err != nil {
		return err
	}
	return l.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM events`).Scan(&l.latest)
}

// Close flushes every message that is still open, ends every subscription and
// closes the file. A message that is still open stays incomplete, because nothing
// finished it.
func (l *Log) Close() error {
	l.mu.Lock()
	flushed := l.closeOpen("")
	for _, sub := range l.subs {
		sub.end()
	}
	l.subs = nil
	l.mu.Unlock()

	if err := l.db.Close(); err != nil {
		return err
	}
	return flushed
}

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
	l.latest = e.Seq

	text, isOpen := openingText(e)
	if isOpen {
		l.open[e.Seq] = &openMessage{session: e.Session, kind: e.Kind, text: []byte(text), stored: len(text)}
	}
	l.publish(Frame{Event: &e, Open: isOpen})

	// The Event is committed and sent, so a failed flush here is reported beside
	// it rather than in place of it.
	if e.Kind == event.KindSessionEnded {
		return e, l.closeOpen(e.Session)
	}
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

	before := len(message.text)
	delta := protocol.Delta{Seq: seq, N: before, Text: text, Final: final}
	message.text = append(message.text, text...)
	if final {
		delta.N, delta.Text = len(message.text), string(message.text)
	}

	if final || len(message.text)-message.stored >= FlushThreshold {
		if err := l.store(seq, message, final); err != nil {
			// The message stays open, and stays as long as it was, so the
			// caller may send this text again.
			message.text = message.text[:before]
			return protocol.Delta{}, err
		}
	}
	if final {
		delete(l.open, seq)
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
			sub.end()
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
	sub.end()
}

// closeOpen closes every message one Session left open, or every open message
// there is when session is empty. The text each one had is written out and
// complete stays false, so the Client draws a torn message. It returns the first
// write that failed, and closes the rest either way. The caller holds the mutex.
func (l *Log) closeOpen(session event.SessionID) error {
	var failed error
	for seq, message := range l.open {
		if session != "" && message.session != session {
			continue
		}
		delete(l.open, seq)
		if message.stored == len(message.text) {
			continue
		}
		if err := l.store(seq, message, false); err != nil && failed == nil {
			failed = err
		}
	}
	return failed
}

// store writes an open message's text over its row. The caller holds the mutex.
func (l *Log) store(seq uint64, message *openMessage, complete bool) error {
	payload, err := json.Marshal(message.payload(complete))
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
