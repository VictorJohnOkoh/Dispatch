package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/harness"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"github.com/VictorJohnOkoh/Dispatch/internal/session"
)

// gating is a Harness that gates edit and execute and nothing else, which is
// OpenCode's shape. It runs no process: what these tests exercise is the Daemon's
// half, which is the Approval Policy.
type gating struct {
	sink chan harness.Sink
}

func newGating() *gating { return &gating{sink: make(chan harness.Sink, 1)} }

func (a *gating) Capabilities() harness.Capabilities {
	caps := harness.Capabilities{Tools: true}
	caps.Gates[event.ToolEdit] = true
	caps.Gates[event.ToolExecute] = true
	return caps
}

func (a *gating) Start(_ context.Context, _ harness.SessionSpec, out harness.Sink) (harness.Run, error) {
	a.sink <- out
	return a, nil
}

func (a *gating) Prompt(context.Context, string) error { return nil }
func (a *gating) Interrupt(context.Context) error      { return nil }
func (a *gating) Close() error                         { return nil }

const gateStart = `{"harness":"gate","model":"qwen3:8b"}`

// gated is a Host running the gating Harness, with one Session up and its Sink in
// hand, which is what an Adapter asks a question through.
func gated(t *testing.T, body string) (*host, event.SessionID, harness.Sink) {
	t.Helper()
	a := newGating()
	h := newHost(t, Harness{Name: "gate", Adapter: a})
	h.stopWait = 50 * time.Millisecond

	id := h.started(t, h.post(t, "/v1/sessions", body)).Session
	h.waitState(t, id, "Idle")
	select {
	case out := <-a.sink:
		return h, id, out
	case <-time.After(2 * time.Second):
		t.Fatal("the Adapter never started")
		return nil, "", nil
	}
}

// asks announces one Tool Call and puts the question the way an Adapter does,
// on its own goroutine, because Approve blocks until the Daemon decides.
func asks(out harness.Sink, id string, kind event.ToolKind) chan event.Decision {
	out.ToolCallRequested(id, "bash", kind, "echo hello", nil)
	answered := make(chan event.Decision, 1)
	go func() {
		decision, err := out.Approve(context.Background(), id, "echo hello", "echo hello")
		if err != nil {
			decision = event.Decision("error: " + err.Error())
		}
		answered <- decision
	}()
	return answered
}

func answer(t *testing.T, answered chan event.Decision) event.Decision {
	t.Helper()
	select {
	case decision := <-answered:
		return decision
	case <-time.After(2 * time.Second):
		t.Fatal("Approve never returned")
		return ""
	}
}

// policies is every value the Approval Policy ever held, read back out of the
// file, which is the only place it is kept.
func (h *host) policies(t *testing.T) []event.ApprovalPolicySet {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+h.logPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT payload FROM events WHERE kind = 'ApprovalPolicySet' ORDER BY seq`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var out []event.ApprovalPolicySet
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			t.Fatalf("scan: %v", err)
		}
		var set event.ApprovalPolicySet
		if err := json.Unmarshal(payload, &set); err != nil {
			t.Fatalf("payload: %v", err)
		}
		out = append(out, set)
	}
	return out
}

// A refused slot answers without asking anybody, and the Tool Call ends refused
// on the Daemon's own decision.
func TestARefusedSlotEndsTheToolCallWithoutAsking(t *testing.T) {
	policy := `{"read":"auto","edit":"auto","execute":"refuse","fetch":"auto","other":"auto"}`
	h, id, out := gated(t, `{"harness":"gate","model":"qwen3:8b","policy":`+policy+`}`)

	if decision := answer(t, asks(out, "c1", event.ToolExecute)); decision != event.DecisionRefused {
		t.Fatalf("Approve = %q", decision)
	}
	if state := h.list(t)[0].State; state == session.Asking {
		t.Error("the Session asked about a slot that is refused")
	}

	var decided event.ApprovalDecided
	h.payload(t, id, event.KindApprovalDecided, &decided)
	if decided.By != event.ByPolicy || decided.Decision != event.DecisionRefused {
		t.Errorf("the decision is %+v, and the Approval Policy made it", decided)
	}
	ends := h.ended(t)
	if len(ends) != 1 || ends[0].Outcome != event.OutcomeRefused {
		t.Fatalf("the log holds %+v, want c1 ended refused", ends)
	}
}

// A wait slot holds the Tool Call, the Session folds to Asking, and the user's
// decision is what releases it.
func TestAWaitSlotHoldsTheToolCallUntilTheUserDecides(t *testing.T) {
	h, id, out := gated(t, gateStart)
	answered := asks(out, "c1", event.ToolExecute)
	h.waitState(t, id, "Asking")

	h.command(t, id, "approvals", `{"toolCallId":"c1","decision":"allowed"}`)
	if decision := answer(t, answered); decision != event.DecisionAllowed {
		t.Fatalf("Approve = %q", decision)
	}
	h.waitState(t, id, "Idle")

	var decided event.ApprovalDecided
	h.payload(t, id, event.KindApprovalDecided, &decided)
	if decided.By != event.ByUser {
		t.Errorf("the decision was made by %q, and the user made it", decided.By)
	}
	if ends := h.ended(t); len(ends) != 0 {
		t.Errorf("an allowed Tool Call was ended by the Daemon: %+v", ends)
	}
}

// The user refusing is the Daemon refusing. The Tool Call ends refused, and what
// the Harness reports about it afterwards does not overwrite that.
func TestAUserRefusalEndsTheToolCallAndOutlastsTheHarness(t *testing.T) {
	h, id, out := gated(t, gateStart)
	answered := asks(out, "c1", event.ToolExecute)
	h.waitState(t, id, "Asking")

	h.command(t, id, "approvals", `{"toolCallId":"c1","decision":"refused"}`)
	if decision := answer(t, answered); decision != event.DecisionRefused {
		t.Fatalf("Approve = %q", decision)
	}
	// The Harness says the call failed, which is its own account of a call the
	// Daemon had already closed.
	out.ToolCallEnded("c1", event.OutcomeError, "denied")

	ends := h.ended(t)
	if len(ends) != 1 || ends[0].Outcome != event.OutcomeRefused {
		t.Fatalf("the log holds %+v, want the one refusal the Daemon decided", ends)
	}
}

// Always allow answers this question and flips the slot, and the value it flipped
// to is in the log like every other value the policy has held.
func TestAlwaysAllowFlipsTheSlotAndWritesThePolicy(t *testing.T) {
	h, id, out := gated(t, gateStart)
	answered := asks(out, "c1", event.ToolExecute)
	h.waitState(t, id, "Asking")

	h.command(t, id, "approvals", `{"toolCallId":"c1","decision":"allowed","always":true}`)
	answer(t, answered)

	held := h.policies(t)
	if len(held) != 2 {
		t.Fatalf("the log holds %d policies, want the start's and the flip", len(held))
	}
	if held[0].Policy[event.ToolExecute] != event.RuleWait || held[0].SetBy != event.SetByDefault {
		t.Errorf("the Session started on %+v", held[0])
	}
	if held[1].Policy[event.ToolExecute] != event.RuleAuto || held[1].SetBy != event.SetByUser {
		t.Errorf("the flip wrote %+v", held[1])
	}
	// The next call of that class runs without asking.
	if decision := answer(t, asks(out, "c2", event.ToolExecute)); decision != event.DecisionAllowed {
		t.Errorf("the second call was %q, and the slot is auto now", decision)
	}
}

// A change applies to Tool Calls requested after it, never to a question already
// open.
func TestAPolicyChangeDoesNotAnswerAQuestionAlreadyOpen(t *testing.T) {
	h, id, out := gated(t, gateStart)
	answered := asks(out, "c1", event.ToolExecute)
	h.waitState(t, id, "Asking")

	h.command(t, id, "policy", `{"policy":{"read":"auto","edit":"wait","execute":"auto","fetch":"auto","other":"auto"}}`)

	// The open question is still open: the Session is still Asking and Approve has
	// not returned.
	if view := h.waitState(t, id, "Asking"); view.State != session.Asking {
		t.Fatalf("the Session is %s", view.State)
	}
	select {
	case decision := <-answered:
		t.Fatalf("the change answered a question already open: %q", decision)
	case <-time.After(50 * time.Millisecond):
	}

	h.command(t, id, "approvals", `{"toolCallId":"c1","decision":"allowed"}`)
	answer(t, answered)
}

// One rule, two call sites. A slot the Harness cannot gate fails the start, and
// fails the same way on a change while the Session runs.
func TestASlotWithNoGateFailsAtTheStartAndOnAChange(t *testing.T) {
	a := newGating()
	h := newHost(t, Harness{Name: "gate", Adapter: a})

	// read is not gated by this Harness, so it may only be auto.
	bad := `{"read":"wait","edit":"wait","execute":"wait","fetch":"auto","other":"auto"}`
	refusal := h.refusal(t, h.post(t, "/v1/sessions", `{"harness":"gate","model":"qwen3:8b","policy":`+bad+`}`),
		protocol.StatusUnprocessable)
	if refusal.Reason != protocol.ReasonNoGate {
		t.Fatalf("the start was refused %+v", refusal)
	}
	if !strings.Contains(refusal.Detail, "read") {
		t.Errorf("the refusal is %q, and it has to name the slot", refusal.Detail)
	}
	if len(h.list(t)) != 0 {
		t.Error("a Session exists for a start that was refused")
	}

	id := h.started(t, h.post(t, "/v1/sessions", gateStart)).Session
	h.waitState(t, id, "Idle")
	again := h.refusal(t, h.post(t, "/v1/sessions/"+string(id)+"/policy", `{"policy":`+bad+`}`),
		protocol.StatusUnprocessable)
	if again.Reason != protocol.ReasonNoGate || !strings.Contains(again.Detail, "read") {
		t.Errorf("the change was refused %+v", again)
	}
}

// The ladder's first step frees a Harness that is blocked on an answer nobody is
// going to give it. A stop that only wrote the Event would leave it blocked until
// the kill.
func TestAStopReleasesAHarnessWaitingOnAnAnswer(t *testing.T) {
	h, id, out := gated(t, gateStart)
	answered := asks(out, "c1", event.ToolExecute)
	h.waitState(t, id, "Asking")

	h.command(t, id, "stop", "")
	if decision := answer(t, answered); decision != event.DecisionRefused {
		t.Errorf("Approve = %q, and a stop refuses what it was holding", decision)
	}
	var decided event.ApprovalDecided
	h.payload(t, id, event.KindApprovalDecided, &decided)
	if decided.By != event.BySessionStopped {
		t.Errorf("the decision was made by %q", decided.By)
	}
}

// A Harness that runs no tools has no Approval Policy at all, which is an absence
// rather than five slots that happen to be auto.
func TestAHarnessWithNoToolsGetsNoPolicyAtAll(t *testing.T) {
	h := newHost(t)
	h.idle(t)

	if held := h.policies(t); len(held) != 0 {
		t.Errorf("a passthrough Session was given %+v", held)
	}
}

// A question about a Tool Call the Harness never announced is refused, and the
// refusal is an Error rather than a decision about a Tool Call that does not
// exist.
func TestAQuestionAboutACallThatWasNeverRequestedIsRefused(t *testing.T) {
	h, id, out := gated(t, gateStart)

	decision, err := out.Approve(context.Background(), "ghost", "rm -rf /", "")
	if decision != event.DecisionRefused || err == nil {
		t.Fatalf("Approve = %q, %v", decision, err)
	}
	var failed event.Error
	h.payload(t, id, event.KindError, &failed)
	if failed.Code != event.ErrAdapterFailed || !strings.Contains(failed.Message, "ghost") {
		t.Errorf("the Error is %+v, and it has to name the call", failed)
	}
	if ends := h.ended(t); len(ends) != 0 {
		t.Errorf("a Tool Call that was never requested was ended: %+v", ends)
	}
	if held := h.policies(t); len(held) != 1 {
		t.Errorf("the policy changed: %+v", held)
	}
}

// An interrupt refuses the question the Prompt left open, because the Adapter
// holding one is blocked inside Approve and cannot read the interrupt until it
// has an answer.
func TestAnInterruptRefusesTheQuestionThePromptLeftOpen(t *testing.T) {
	h, id, out := gated(t, gateStart)
	answered := asks(out, "c1", event.ToolExecute)
	h.waitState(t, id, "Asking")

	h.command(t, id, "interrupt", "")
	if decision := answer(t, answered); decision != event.DecisionRefused {
		t.Errorf("Approve = %q, and the interrupt abandoned the Prompt", decision)
	}
	ends := h.ended(t)
	if len(ends) != 1 || ends[0].Outcome != event.OutcomeRefused {
		t.Errorf("the log holds %+v, want c1 ended refused", ends)
	}
}

// The Host config's default is what a Session starts on, and it is told apart
// from the built-in fallback by being different from it.
func TestTheHostConfigsDefaultIsWhatASessionStartsOn(t *testing.T) {
	a := newGating()
	h := newHost(t, Harness{Name: "gate", Adapter: a})
	// refuse where the fallback would wait, and wait where it would run.
	h.policyDefault = event.Policy{event.RuleWait, event.RuleRefuse, event.RuleRefuse, event.RuleWait, event.RuleWait}

	id := h.started(t, h.post(t, "/v1/sessions", gateStart)).Session
	h.waitState(t, id, "Idle")

	held := h.policies(t)
	if len(held) != 1 {
		t.Fatalf("the Session started on %+v", held)
	}
	// read and the two ungated slots are clipped to auto; the gated two are the
	// config's own.
	want := event.Policy{event.RuleAuto, event.RuleRefuse, event.RuleRefuse, event.RuleAuto, event.RuleAuto}
	if held[0].Policy != want {
		t.Errorf("the Session started on %v, want %v", held[0].Policy, want)
	}
}

// The Session's own default is the Host config's, clipped by the Gates: a slot
// this Harness cannot hold is auto whatever the config said.
func TestTheStartingPolicyIsTheHostDefaultClippedByTheGates(t *testing.T) {
	h, _, _ := gated(t, gateStart)

	held := h.policies(t)
	if len(held) != 1 || held[0].SetBy != event.SetByDefault {
		t.Fatalf("the Session started on %+v", held)
	}
	want := event.Policy{event.RuleAuto, event.RuleWait, event.RuleWait, event.RuleAuto, event.RuleAuto}
	if held[0].Policy != want {
		t.Errorf("the Session started on %v, want %v", held[0].Policy, want)
	}
}
