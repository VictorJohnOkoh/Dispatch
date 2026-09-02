package daemon

import (
	"database/sql"
	"encoding/json"
	"sync"
	"testing"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// ended is every ToolCallEnded in the file, in Seq order, as the pair the ledger
// is judged on.
func (h *host) ended(t *testing.T) []event.ToolCallEnded {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+h.logPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT payload FROM events WHERE kind = 'ToolCallEnded' ORDER BY seq`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var out []event.ToolCallEnded
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			t.Fatalf("scan: %v", err)
		}
		var body event.ToolCallEnded
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Fatalf("payload: %v", err)
		}
		out = append(out, body)
	}
	return out
}

// The first of the two triggers. The Harness announced a Tool Call and never said
// what came of it, so the Prompt completing is what ends it.
func TestAToolCallWithNoResultEndsWhenThePromptCompletes(t *testing.T) {
	h := newHost(t)
	k := &sink{d: h.Daemon, s: h.live(t)}

	k.ToolCallRequested("c1", "bash", event.ToolExecute, "run it", nil)
	k.Completed("stop", event.Usage{})

	ends := h.ended(t)
	if len(ends) != 1 || ends[0].ToolCallID != "c1" || ends[0].Outcome != event.OutcomeUnknown {
		t.Fatalf("the ledger wrote %+v, want c1 ended %q", ends, event.OutcomeUnknown)
	}
	kinds := h.kinds(t)
	if last := kinds[len(kinds)-2:]; last[0] != "ToolCallEnded" || last[1] != "PromptCompleted" {
		t.Errorf("the log ends %v, and a Tool Call ends as the Prompt completes", kinds)
	}
}

// Parallel calls are correlated one at a time, by their own id. The one the
// Harness reported keeps the outcome the Harness gave it.
func TestTwoOpenToolCallsAreBothCorrelatedByTheirOwnID(t *testing.T) {
	h := newHost(t)
	k := &sink{d: h.Daemon, s: h.live(t)}

	k.ToolCallRequested("c1", "bash", event.ToolExecute, "run it", nil)
	k.ToolCallRequested("c2", "read", event.ToolRead, "read it", nil)
	k.ToolCallEnded("c1", event.OutcomeOK, "two lines")
	k.Completed("stop", event.Usage{})

	ends := h.ended(t)
	if len(ends) != 2 {
		t.Fatalf("the ledger wrote %+v, want one end each", ends)
	}
	if ends[0].ToolCallID != "c1" || ends[0].Outcome != event.OutcomeOK {
		t.Errorf("c1 ended %+v, and the Harness reported it", ends[0])
	}
	if ends[1].ToolCallID != "c2" || ends[1].Outcome != event.OutcomeUnknown {
		t.Errorf("c2 ended %+v, and nothing observed its result", ends[1])
	}
}

// The other trigger, and the only one a Session that never completed its Prompt
// reaches.
func TestAToolCallStillOpenWhenTheSessionEndsEndsUnknown(t *testing.T) {
	h := newHost(t)
	id := h.idle(t)
	s, _, _ := h.sessions.find(id)
	k := &sink{d: h.Daemon, s: s}

	k.ToolCallRequested("c1", "bash", event.ToolExecute, "run it", nil)
	h.command(t, id, "stop", "")

	ends := h.ended(t)
	if len(ends) != 1 || ends[0].ToolCallID != "c1" || ends[0].Outcome != event.OutcomeUnknown {
		t.Fatalf("the ledger wrote %+v, want c1 ended %q", ends, event.OutcomeUnknown)
	}
}

// Whichever trigger fires first wins. The second finds nothing open, because the
// open calls are folded out of the Session's own Events rather than counted in a
// field that could be closed twice.
func TestTheSecondTriggerDoesNotEndAToolCallAgain(t *testing.T) {
	h := newHost(t)
	id := h.idle(t)
	s, _, _ := h.sessions.find(id)
	k := &sink{d: h.Daemon, s: s}

	k.ToolCallRequested("c1", "bash", event.ToolExecute, "run it", nil)
	k.Completed("stop", event.Usage{})
	h.command(t, id, "stop", "")

	if ends := h.ended(t); len(ends) != 1 {
		t.Fatalf("the ledger wrote %+v, want the one end the first trigger gave it", ends)
	}
}

// The two triggers arrive on two goroutines, so first is decided by the ledger
// and not by which one the scheduler ran. One of them writes the end and the
// other finds nothing open, whichever way round they land.
func TestTheTwoTriggersEndAToolCallOnlyOnce(t *testing.T) {
	h := newHost(t)
	id := h.idle(t)
	s, _, _ := h.sessions.find(id)
	k := &sink{d: h.Daemon, s: s}
	k.ToolCallRequested("c1", "bash", event.ToolExecute, "run it", nil)

	// The stop is posted rather than commanded, because a helper that fails the
	// test cannot be called from a goroutine that is not the test's.
	var both sync.WaitGroup
	both.Add(2)
	go func() { defer both.Done(); k.Completed("stop", event.Usage{}) }()
	go func() { defer both.Done(); h.post(t, "/v1/sessions/"+string(id)+"/stop", "") }()
	both.Wait()

	if ends := h.ended(t); len(ends) != 1 {
		t.Fatalf("the ledger wrote %+v, and one Tool Call ends once", ends)
	}
}

// A Prompt that completes with nothing open writes nothing, so the ledger costs
// an ordinary Prompt no Event at all.
func TestAPromptThatCompletesWithNothingOpenWritesNoEnd(t *testing.T) {
	h := newHost(t)
	h.chat.serve(helloStream, false)
	id := h.idle(t)
	h.command(t, id, "prompts", promptBody)
	h.waitState(t, id, "Idle")

	if ends := h.ended(t); len(ends) != 0 {
		t.Errorf("the ledger wrote %+v for a Prompt with no Tool Call in it", ends)
	}
}

// A decision the ladder refused ends because of that refusal, and not as a call
// nobody watched.
func TestAToolCallRefusedByTheLadderEndsRefused(t *testing.T) {
	h := newHost(t)
	id := h.idle(t)
	s, _, _ := h.sessions.find(id)
	h.write(s, event.KindToolCallRequested, &event.ToolCallRequested{ToolCallID: "c1", Name: "bash"})
	h.write(s, event.KindApprovalRequested, &event.ApprovalRequested{ToolCallID: "c1", Title: "run it"})

	if w := h.post(t, "/v1/sessions/"+string(id)+"/stop", ""); w.Code != protocol.StatusAccepted {
		t.Fatalf("stop: status %d: %s", w.Code, w.Body.String())
	}
	ends := h.ended(t)
	if len(ends) != 1 || ends[0].Outcome != event.OutcomeRefused {
		t.Fatalf("the ledger wrote %+v, want c1 ended %q", ends, event.OutcomeRefused)
	}
}
