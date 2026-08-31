package eventlog

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// openLog opens a log on a real file under the test's temp directory.
func openLog(t *testing.T, path string) *Log {
	t.Helper()
	log, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { log.Close() })
	return log
}

func tempPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "events.db")
}

// openReader opens the file with a connection of its own, so what a test reads
// back is what reached the file rather than what the writer holds.
func openReader(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// readRows reads every row as protocol.Event, the untyped wire envelope, because
// a row needs no translation before it is sent.
func readRows(t *testing.T, path string) []protocol.Event {
	t.Helper()
	db := openReader(t, path)

	rows, err := db.Query(`SELECT seq, session, at, kind, payload FROM events ORDER BY seq`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var out []protocol.Event
	for rows.Next() {
		var e protocol.Event
		var payload []byte
		if err := rows.Scan(&e.Seq, &e.Session, &e.At, &e.Kind, &payload); err != nil {
			t.Fatalf("scan: %v", err)
		}
		e.Payload = payload
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func journalMode(t *testing.T, path string) string {
	t.Helper()
	var mode string
	if err := openReader(t, path).QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	return mode
}

func prompt(text string) event.Event {
	return event.Event{
		Session: "s1",
		At:      time.UnixMicro(1_700_000_000_000_000).UTC(),
		Kind:    event.KindPromptSubmitted,
		Payload: &event.PromptSubmitted{Text: text},
	}
}

func TestOpenCreatesSchemaOnce(t *testing.T) {
	path := tempPath(t)

	first := openLog(t, path)
	if _, err := first.Append(prompt("one")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second := openLog(t, path)
	got, err := second.Append(prompt("two"))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if got.Seq != 2 {
		t.Errorf("Seq after reopen = %d, want 2", got.Seq)
	}
	if rows := readRows(t, path); len(rows) != 2 {
		t.Errorf("rows after reopen = %d, want 2", len(rows))
	}
}

func TestWriteAheadLoggingIsOn(t *testing.T) {
	path := tempPath(t)
	openLog(t, path)

	if mode := journalMode(t, path); mode != "wal" {
		t.Errorf("journal_mode = %q, want %q", mode, "wal")
	}
}

func TestSeqStartsAtOneAndNeverSkips(t *testing.T) {
	path := tempPath(t)
	log := openLog(t, path)

	const appends = 200
	for i := range appends {
		got, err := log.Append(prompt("p"))
		if err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
		if want := uint64(i + 1); got.Seq != want {
			t.Fatalf("Seq = %d, want %d", got.Seq, want)
		}
	}

	rows := readRows(t, path)
	if len(rows) != appends {
		t.Fatalf("rows = %d, want %d", len(rows), appends)
	}
	for i, row := range rows {
		if want := uint64(i + 1); row.Seq != want {
			t.Fatalf("stored Seq = %d, want %d", row.Seq, want)
		}
	}
}

func TestConcurrentAppendsStayGapless(t *testing.T) {
	path := tempPath(t)
	log := openLog(t, path)

	const writers, each = 8, 25
	seen := make([]uint64, 0, writers*each)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range each {
				got, err := log.Append(prompt("p"))
				if err != nil {
					t.Errorf("Append: %v", err)
					return
				}
				mu.Lock()
				seen = append(seen, got.Seq)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	returned := make(map[uint64]bool, len(seen))
	for _, seq := range seen {
		if returned[seq] {
			t.Fatalf("Seq %d returned twice", seq)
		}
		returned[seq] = true
	}
	for seq := uint64(1); seq <= uint64(writers*each); seq++ {
		if !returned[seq] {
			t.Fatalf("Seq %d was never returned", seq)
		}
	}

	rows := readRows(t, path)
	if len(rows) != writers*each {
		t.Fatalf("rows = %d, want %d", len(rows), writers*each)
	}
	for i, row := range rows {
		if want := uint64(i + 1); row.Seq != want {
			t.Fatalf("stored Seq = %d, want %d", row.Seq, want)
		}
	}
}

// The Event is in the file the moment Append returns, which is what lets the
// Daemon send it only after it is committed.
func TestAppendCommitsBeforeItReturns(t *testing.T) {
	path := tempPath(t)
	log := openLog(t, path)

	if _, err := log.Append(prompt("hello")); err != nil {
		t.Fatalf("Append: %v", err)
	}

	rows := readRows(t, path)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
}

// A stored row goes out as it is: the five columns are already the wire shape.
func TestStoredRowIsTheWireShape(t *testing.T) {
	path := tempPath(t)
	log := openLog(t, path)

	written, err := log.Append(prompt("hello"))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	wire, err := json.Marshal(written)
	if err != nil {
		t.Fatalf("marshal written: %v", err)
	}
	fromRow, err := json.Marshal(readRows(t, path)[0])
	if err != nil {
		t.Fatalf("marshal row: %v", err)
	}
	if string(fromRow) != string(wire) {
		t.Errorf("row on the wire = %s, want %s", fromRow, wire)
	}
}
