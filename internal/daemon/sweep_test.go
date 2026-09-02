package daemon

import (
	"log/slog"
	"testing"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/eventlog"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"github.com/VictorJohnOkoh/Dispatch/internal/session"
)

// killed is what a Daemon that died and came back is: a second Daemon over the
// same Event log, holding the file and none of the memory.
func killed(t *testing.T, h *host) *Daemon {
	t.Helper()
	if err := h.events.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	events, err := eventlog.Open(h.logPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { events.Close() })

	d := New(slog.New(slog.NewTextHandler(h.lines, nil)), events, h.root, h.Daemon.root, nil, nil, event.Policy{})
	if err := d.sweep(); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	return d
}

// folded is one Session's State as the log describes it, which is the only place
// a restarted Daemon can read it from.
func folded(t *testing.T, d *Daemon, id event.SessionID) (session.State, event.EndReason) {
	t.Helper()
	events, err := d.events.SessionEvents(id)
	if err != nil {
		t.Fatalf("SessionEvents: %v", err)
	}
	state, reason := session.Fold(events)
	return state, reason
}

// Behaviour 5. The process does not fold, so a Session that was live when the
// Daemon died is ended lost, and its transcript is still there to read.
func TestADaemonKilledUnderALiveSessionEndsItLostOnTheNextBoot(t *testing.T) {
	h := newHost(t)
	id := h.started(t, h.post(t, "/v1/sessions", startBody)).Session
	h.waitState(t, id, "Idle")
	events := "/v1/sessions/" + string(id) + "/events"
	before := pageOf(t, h.Daemon, events)

	d := killed(t, h)

	state, reason := folded(t, d, id)
	if state != session.Ended || reason != event.EndLost {
		t.Fatalf("Session %s folds to %s{%s}, want Ended{lost}", id, state, reason)
	}

	// The restarted Daemon has no registry, and the Events are all still readable
	// from the file, which is what the Client draws the transcript from.
	after := pageOf(t, d, events)
	if len(after) <= len(before) {
		t.Fatalf("%d Events after the sweep, %d before: the transcript did not survive", len(after), len(before))
	}
	for i, was := range before {
		if after[i].Seq != was.Seq || after[i].Kind != was.Kind {
			t.Fatalf("Event %d changed: %+v, was %+v", i, after[i], was)
		}
	}
}

// The sweep writes DaemonStarted, then refuses every open question, then closes
// every open Tool Call, then ends the Session. The order is fixed, and the fold
// over it leaves nothing open.
func TestTheSweepClosesTheQuestionsAndTheCallsALostSessionLeftOpen(t *testing.T) {
	h := newHost(t)
	s := &Session{id: "s-busy", cancel: func() {}}
	h.sessions.add(s)
	write := func(kind event.Kind, payload any) {
		t.Helper()
		if _, err := h.write(s, kind, payload); err != nil {
			t.Fatalf("write %s: %v", kind, err)
		}
	}
	write(event.KindSessionStarted, &event.SessionStarted{Harness: "passthrough", Model: "qwen3:8b"})
	write(event.KindToolCallRequested, &event.ToolCallRequested{ToolCallID: "asked", Name: "write"})
	write(event.KindApprovalRequested, &event.ApprovalRequested{ToolCallID: "asked"})
	write(event.KindToolCallRequested, &event.ToolCallRequested{ToolCallID: "running", Name: "read"})

	d := killed(t, h)

	events, err := d.events.SessionEvents("s-busy")
	if err != nil {
		t.Fatalf("SessionEvents: %v", err)
	}
	var got []event.Kind
	for _, e := range events[4:] {
		got = append(got, e.Kind)
	}
	want := []event.Kind{
		event.KindDaemonStarted,
		event.KindApprovalDecided,
		event.KindToolCallEnded,
		event.KindToolCallEnded,
		event.KindSessionEnded,
	}
	if len(got) != len(want) {
		t.Fatalf("the sweep wrote %v, want %v", got, want)
	}
	for i, kind := range want {
		if got[i] != kind {
			t.Fatalf("the sweep wrote %v, want %v", got, want)
		}
	}

	decided, ok := events[5].Payload.(*event.ApprovalDecided)
	if !ok || decided.Decision != event.DecisionRefused || decided.By != event.ByDaemonRestart {
		t.Errorf("the question was answered %#v", events[5].Payload)
	}
	// The call whose question was refused ended refused. The one that was merely
	// in flight ended unknown, because nothing observed its result.
	for i, want := range map[int]event.Outcome{6: event.OutcomeRefused, 7: event.OutcomeUnknown} {
		ended, ok := events[i].Payload.(*event.ToolCallEnded)
		if !ok || ended.Outcome != want {
			t.Errorf("Event %d = %#v, want outcome %s", i, events[i].Payload, want)
		}
	}
	if len(session.OpenCalls(events)) != 0 || len(session.Held(events)) != 0 {
		t.Error("the swept Session still holds an open call or an open question")
	}
}

// The mark the sweep leaves is what makes a second boot over the same log free.
// Every Session at or below it has a SessionEnded, so there is nothing to write.
func TestASecondSweepOverTheSameLogWritesNothing(t *testing.T) {
	h := newHost(t)
	id := h.started(t, h.post(t, "/v1/sessions", startBody)).Session
	h.waitState(t, id, "Idle")

	first := killed(t, h)
	latest := first.events.Latest()

	second := New(slog.New(slog.NewTextHandler(h.lines, nil)), first.events, first.transcripts, first.root, nil, nil, event.Policy{})
	if err := second.sweep(); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if got := second.events.Latest(); got != latest {
		t.Errorf("a second sweep wrote %d Events over an ended Session", got-latest)
	}
}

// The stream's identity is the log's, so a Client holding a Cursor from before the
// restart is served rather than resynced: the file is the same file.
func TestTheLogKeepsItsIdentityAcrossARestart(t *testing.T) {
	h := newHost(t)
	was := h.events.LogID()

	d := killed(t, h)
	if d.events.LogID() != was {
		t.Errorf("the log's identity changed across a restart: %q, was %q", d.events.LogID(), was)
	}
}

// pageOf reads one page of a Session's Events off any Daemon, because a restart
// answers from a second one over the same file.
func pageOf(t *testing.T, d *Daemon, path string) []protocol.Event {
	t.Helper()
	var body struct {
		Events []protocol.Event `json:"events"`
	}
	decodeGet(t, d, path, &body)
	return body.Events
}
