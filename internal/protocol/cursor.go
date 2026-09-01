package protocol

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// Cursor is where a reader may resume a Daemon's Event stream, carried as
// Last-Event-ID. It is the highest Sequence Number below every open appendable
// Event, so it is not the last Sequence Number a reader saw, and it lags a message
// that is still arriving. That lag is what makes an unfinished message replay
// whole after a reconnect.
//
// Zero is a real Cursor and it means the whole log, because a Sequence Number
// starts at 1. A reader with no Cursor at all sends no Last-Event-ID, which is a
// different thing and is the caller's to tell apart.
type Cursor uint64

func (c Cursor) String() string { return strconv.FormatUint(uint64(c), 10) }

// CursorHeader carries a Cursor on a request, under SSE's own name so that a
// browser's EventSource sends it unaided.
const CursorHeader = "Last-Event-ID"

// LogHeader carries the identity of the log a reader's Cursor came from. It is a
// header and not part of the Cursor because a Daemon's Cursor is one number and
// stays one number, and it is optional because a reader that has never connected
// holds no identity to send. A Daemon that is handed one it does not match answers
// a resync Frame.
const LogHeader = "Dispatch-Log"

// ParseCursor reads a Daemon's Last-Event-ID, which is one number and stays one
// number because the Hub splits the merged spelling before a Daemon sees it.
func ParseCursor(s string) (Cursor, error) {
	c, err := parseSeq(s)
	if err != nil {
		return 0, fmt.Errorf("protocol: %w", err)
	}
	return c, nil
}

// parseSeq rejects anything that is not a plain decimal number rather than reading
// it as zero, because zero means replay the whole log and a reader must never be
// given that by accident.
func parseSeq(s string) (Cursor, error) {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("cursor %q is not a Sequence Number", s)
	}
	return Cursor(n), nil
}

// MergedCursor is the Client's leg: one Cursor per Host, because its stream is
// merged and a browser's EventSource keeps exactly one Last-Event-ID. The Hub
// writes the whole thing onto every frame it forwards and splits it again on a
// reconnect, so a Client never assembles one and a Daemon never sees one.
//
// A Sequence Number is unique inside one Daemon and nowhere else, so two entries
// here are two unrelated counters and nothing ever compares them.
type MergedCursor map[string]Cursor

// String formats it as host=seq pairs, sorted by Host id so that the same set of
// Cursors is always the same string. The Hub re-emits it on every frame that
// advances anything, and a string that jittered would read as a change.
func (m MergedCursor) String() string {
	var b strings.Builder
	for i, host := range slices.Sorted(maps.Keys(m)) {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(host)
		b.WriteByte('=')
		b.WriteString(m[host].String())
	}
	return b.String()
}

// ParseMergedCursor reads the Client's Last-Event-ID. It rejects a malformed one
// whole rather than reading past the bad entry, because a Cursor that half-parses
// would silently restart the Hosts it could not read.
func ParseMergedCursor(s string) (MergedCursor, error) {
	if s == "" {
		return nil, errors.New("protocol: cursor is empty")
	}

	entries := strings.Split(s, ",")
	m := make(MergedCursor, len(entries))
	for _, entry := range entries {
		host, seq, ok := strings.Cut(entry, "=")
		if !ok {
			return nil, fmt.Errorf("protocol: cursor entry %q names no Host", entry)
		}
		if !ValidHostID(host) {
			return nil, fmt.Errorf("protocol: cursor names %q, which is not a Host id", host)
		}
		if _, twice := m[host]; twice {
			return nil, fmt.Errorf("protocol: cursor names Host %q twice", host)
		}
		c, err := parseSeq(seq)
		if err != nil {
			return nil, fmt.Errorf("protocol: Host %q: %w", host, err)
		}
		m[host] = c
	}
	return m, nil
}

// ValidHostID reports whether s matches [A-Za-z0-9_-]+. A Host id is part of a wire
// format: it is a path segment and an entry in every merged Cursor. Constraining it
// is cheaper than an escaping rule nobody would test, so the Hub rejects a profile
// at config load that does not match.
func ValidHostID(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}
