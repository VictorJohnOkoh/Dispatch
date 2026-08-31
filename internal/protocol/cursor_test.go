package protocol

import (
	"maps"
	"testing"
)

// The Daemon's Cursor is one number and stays one number, because the Hub splits
// the merged one before a Daemon ever sees it.
func TestDaemonCursorRoundTrips(t *testing.T) {
	for _, want := range []Cursor{0, 1, 412, 9412, 18446744073709551615} {
		got, err := ParseCursor(want.String())
		if err != nil {
			t.Fatalf("%s: %v", want, err)
		}
		if got != want {
			t.Errorf("got %d, want %d", got, want)
		}
	}
}

// The Client's carries one entry per Host, because its stream is merged.
func TestMergedCursorRoundTrips(t *testing.T) {
	want := MergedCursor{"desktop": 9412, "laptop": 98, "attic-box_2": 0}

	s := want.String()
	got, err := ParseMergedCursor(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	if !maps.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// The Hub re-emits the Cursor on every frame that advances anything, so the same
// set of Cursors has to be the same string every time. A jittering string would
// read as a change.
func TestMergedCursorSortsByHost(t *testing.T) {
	m := MergedCursor{"laptop": 98, "desktop": 9412}
	if got, want := m.String(), "desktop=9412,laptop=98"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A Cursor that half-parses would silently restart a Host, and zero is a real
// Cursor meaning replay the whole log, so neither parser may fall back to it.
func TestMalformedCursorIsRejected(t *testing.T) {
	daemon := []struct {
		name, in string
	}{
		{"empty", ""},
		{"not a number", "latest"},
		{"negative", "-1"},
		{"signed", "+9412"},
		{"trailing text", "9412x"},
		{"padded", " 9412"},
		{"fractional", "94.12"},
		{"past uint64", "18446744073709551616"},
		{"the merged spelling", "desktop=9412"},
	}
	for _, c := range daemon {
		t.Run("daemon/"+c.name, func(t *testing.T) {
			got, err := ParseCursor(c.in)
			if err == nil {
				t.Fatalf("%q parsed to %d, want an error", c.in, got)
			}
			if got != 0 {
				t.Errorf("a rejected Cursor answered %d, want the zero value", got)
			}
		})
	}

	merged := []struct {
		name, in string
	}{
		{"empty", ""},
		{"no host", "9412"},
		{"empty host", "=9412"},
		{"empty seq", "desktop="},
		{"empty entry", "desktop=9412,,laptop=98"},
		{"trailing comma", "desktop=9412,"},
		{"a space after the comma", "desktop=9412, laptop=98"},
		{"a Host id that is not one", "desk top=9412"},
		{"a dot in the Host id", "desktop.local=9412"},
		{"one Host named twice", "desktop=9412,desktop=98"},
		{"a seq that is not a number", "desktop=latest"},
		{"two equals signs", "desktop=94=12"},
	}
	for _, c := range merged {
		t.Run("merged/"+c.name, func(t *testing.T) {
			got, err := ParseMergedCursor(c.in)
			if err == nil {
				t.Fatalf("%q parsed to %v, want an error", c.in, got)
			}
			if got != nil {
				t.Errorf("a rejected Cursor answered %v, want nothing", got)
			}
		})
	}
}

// A Host id is part of a wire format: it is a path segment and an entry in every
// merged Cursor, so it is constrained rather than free text.
func TestValidHostID(t *testing.T) {
	good := []string{"desktop", "laptop2", "attic-box", "attic_box", "A", "0"}
	for _, s := range good {
		if !ValidHostID(s) {
			t.Errorf("%q should be a Host id", s)
		}
	}

	bad := []string{"", "desktop.local", "desk top", "desktop=1", "desktop/1", "café"}
	for _, s := range bad {
		if ValidHostID(s) {
			t.Errorf("%q should not be a Host id", s)
		}
	}
}
