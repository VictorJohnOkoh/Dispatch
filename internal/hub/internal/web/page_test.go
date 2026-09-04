package web

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// page.js is the wiring, and this drives it under node against a page small
// enough to read: el.js is the element and dom.js the page page.js touches. What
// runs here is the file the browser is served, not a copy of it.

// pageUnder loads fold.js, render.js and page.js over the stub page, runs the
// test's own script, and decodes what it printed.
func pageUnder(t *testing.T, script string, into any) {
	pageUnderSetup(t, "", script, into)
}

func pageUnderSetup(t *testing.T, setup, script string, into any) {
	t.Helper()
	node := findNode(t)

	var program strings.Builder
	for _, name := range []string{"testdata/el.js", "testdata/dom.js", "fold.js", "render.js"} {
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		program.WriteString(string(source))
		program.WriteString("\n")
	}
	program.WriteString(setup)
	program.WriteString("\n")
	source, err := os.ReadFile("page.js")
	if err != nil {
		t.Fatalf("page.js: %v", err)
	}
	program.WriteString(string(source))
	program.WriteString("\n")
	// The script runs after the page's own load() has settled, because that one is
	// asynchronous and everything a test asserts on comes after it.
	program.WriteString("setTimeout(async () => {\n" + script + "\n}, 0);")

	path := filepath.Join(t.TempDir(), "page.js")
	if err := os.WriteFile(path, []byte(program.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, path)
	said, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, said)
	}
	if err := json.Unmarshal(said, into); err != nil {
		t.Fatalf("node said %q: %v", said, err)
	}
}

// The first paint carries questions that are already open on other Sessions.
// Without this, a reload can hide the only path the user has to answer one.
func TestAQuestionAlreadyOpenAtFirstPaintRaisesAToast(t *testing.T) {
	var got []string
	pageUnderSetup(t, `
document.getElementById("approvals").textContent = JSON.stringify([
  {host: "attic", session: "s-9", kind: "ApprovalRequested",
    payload: {toolCallId: "c1", title: "write out.txt"}},
]);
`, `
console.log(JSON.stringify(dom.toasts.children.map((t) => t.textContent)));
`, &got)

	if len(got) != 1 || !strings.Contains(got[0], "s-9 on attic") || !strings.Contains(got[0], "write out.txt") {
		t.Errorf("the first paint raised %v", got)
	}
}

// Live Events move the Session through its states, with nothing reloaded. Each
// frame is one the Hub would send, and the state after it is what the page shows.
func TestLiveEventsMoveTheSessionThroughItsStates(t *testing.T) {
	var got []string
	pageUnder(t, `
const frames = [
  {seq: 1, kind: "SessionStarted", payload: {harness: "opencode"}},
  {seq: 2, kind: "SessionReady", payload: {model: "m"}},
  {seq: 3, kind: "PromptSubmitted", payload: {text: "go"}},
  {seq: 4, kind: "ToolCallRequested", payload: {toolCallId: "c1", name: "bash"}},
  {seq: 5, kind: "ApprovalRequested", payload: {toolCallId: "c1", title: "run it"}},
  {seq: 6, kind: "ApprovalDecided", payload: {toolCallId: "c1", decision: "allowed", by: "user"}},
  {seq: 7, kind: "ToolCallEnded", payload: {toolCallId: "c1", outcome: "ok"}},
  {seq: 8, kind: "PromptCompleted", payload: {stopReason: "end_turn", usage: {}}},
  {seq: 9, kind: "SessionEnded", payload: {reason: "stopped"}},
];
const seen = [];
for (const f of frames) {
  opened.send("event", {host: "desk", session: "s-1", ...f});
  seen.push(dom.stateElement.textContent);
}
console.log(JSON.stringify(seen));
`, &got)

	want := []string{"Starting", "Idle", "Working", "Working", "Asking", "Working", "Working", "Idle", "Ended stopped"}
	if len(got) != len(want) {
		t.Fatalf("the page answered %d states and %d frames were sent: %v", len(got), len(want), got)
	}
	for i, state := range want {
		if got[i] != state {
			t.Errorf("after frame %d the page says %q, want %q. The whole run was %v", i+1, got[i], state, got)
		}
	}
}

// A page that dropped a Delta repairs itself when the final one arrives, because
// the final Delta carries the whole text and replaces rather than appends.
func TestADroppedDeltaRepairsItselfOnTheFinalOne(t *testing.T) {
	var got struct {
		Whole   string `json:"whole"`
		Dropped string `json:"dropped"`
		Final   string `json:"final"`
	}
	// The final Delta's n is the whole length, which is what the Hub sends. A test
	// that sent 0 there would pass with the final rule deleted, because slicing to
	// nothing repairs the text by accident.
	pageUnder(t, `
function say(seq) { return dom.transcript.querySelector(".text").textContent; }
opened.send("event", {host: "desk", session: "s-1", seq: 1, kind: "AssistantMessage", payload: {text: "", complete: false}});
opened.send("delta", {host: "desk", seq: 1, n: 0, text: "the "});
opened.send("delta", {host: "desk", seq: 1, n: 4, text: "quick "});
const whole = say();
// This one never arrives: the page misses "brown " entirely.
opened.send("delta", {host: "desk", seq: 1, n: 16, text: "fox"});
const dropped = say();
opened.send("delta", {host: "desk", seq: 1, n: 19, text: "the quick brown fox", final: true});
console.log(JSON.stringify({whole, dropped, final: say()}));
`, &got)

	if got.Whole != "the quick " {
		t.Errorf("the Deltas that arrived left %q", got.Whole)
	}
	// The page could not know what it missed, so it holds something wrong until
	// the end. That is the state the final Delta has to repair.
	if got.Dropped == "the quick brown fox" {
		t.Errorf("the page holds %q and it never saw the middle of it", got.Dropped)
	}
	if got.Final != "the quick brown fox" {
		t.Errorf("the final Delta left %q, and it carries the whole text", got.Final)
	}
}

// A resync discards what the page holds for that Host and refetches it. The
// stream stays open, so every other Host on it keeps streaming.
func TestAResyncDiscardsAndRefetchesThatHost(t *testing.T) {
	var got struct {
		Rows    int      `json:"rows"`
		Before  int      `json:"before"`
		Fetched []string `json:"fetched"`
		Open    bool     `json:"open"`
		State   string   `json:"state"`
	}
	pageUnder(t, `
opened.send("event", {host: "desk", session: "s-1", seq: 3, kind: "PromptSubmitted", payload: {text: "go"}});

// A resync for another Host changes nothing here.
opened.send("resync", {host: "attic"});
const before = dom.transcript.children.length;

// The refetch answers with what the log now holds, which is one Event.
served.set("0", [{seq: 1, kind: "SessionStarted", payload: {harness: "opencode"}}]);
served.set("1", []);
opened.send("resync", {host: "desk"});
setTimeout(() => {
  console.log(JSON.stringify({
    rows: dom.transcript.children.length,
    before,
    fetched,
    open: opened.listeners.size > 0,
    state: dom.stateElement.textContent,
  }));
}, 0);
`, &got)

	if got.Before != 2 {
		t.Errorf("a resync for another Host left %d rows, and this page had two", got.Before)
	}
	if got.Rows != 1 {
		t.Errorf("the page holds %d rows, and the refetch answered with one", got.Rows)
	}
	if len(got.Fetched) < 2 {
		t.Errorf("the page fetched %v, want the one at load and the one the resync asked for", got.Fetched)
	}
	if !got.Open {
		t.Error("the resync tore down the stream, and every other Host is on it")
	}
	if got.State != "Starting" {
		t.Errorf("the page says %q after refetching one SessionStarted", got.State)
	}
}

// An Event Kind this build has never heard of draws as a neutral row rather than
// as nothing, which is the whole cost of the Client knowing Kinds at all.
func TestAnUnknownKindDrawsANeutralRow(t *testing.T) {
	var got struct {
		Kind  string `json:"kind"`
		Title string `json:"title"`
		Text  string `json:"text"`
	}
	pageUnder(t, `
opened.send("event", {host: "desk", session: "s-1", seq: 1, kind: "FromNextYear", payload: {note: "hello"}});
const row = dom.transcript.children[0];
console.log(JSON.stringify({kind: row.dataset.kind, title: row.querySelector(".title").textContent, text: row.textContent}));
`, &got)

	if got.Kind != "FromNextYear" || got.Title != "FromNextYear" {
		t.Errorf("the row is %+v, and an unknown Kind is drawn as itself", got)
	}
	if !strings.Contains(got.Text, "hello") {
		t.Errorf("the row says %q, and it carries the payload it could not read", got.Text)
	}
}

// An Event for a Session this page is not drawing is still news: it is a rail row
// that has changed. The rail is redrawn from the Hub, because the browser holds no
// history for a Session it is not drawing and a fold over the tail of one would be
// a guess.
func TestAnEventOnAnotherHostRedrawsTheRail(t *testing.T) {
	var got struct {
		Rows    []string `json:"rows"`
		Fetched []string `json:"fetched"`
	}
	pageUnder(t, `
railAnswer = [
  {Host: "desk", Session: "s-1", Cwd: "/w", SessionState: "Working", Answering: true, On: true},
  {Host: "attic", Session: "s-9", Cwd: "/other", SessionState: "Asking", Answering: true},
  {Host: "shed", Answering: false},
];
opened.send("event", {host: "attic", session: "s-9", seq: 4, kind: "ApprovalRequested", payload: {toolCallId: "c1"}});
setTimeout(() => {
  console.log(JSON.stringify({
    rows: dom.rail.children.map((r) => r.textContent),
    fetched: fetched.filter((u) => u.startsWith("/rail/")),
  }));
}, 0);
`, &got)

	if len(got.Fetched) == 0 {
		t.Fatal("an Event on another Host redrew nothing")
	}
	if len(got.Rows) != 3 {
		t.Fatalf("the rail holds %v", got.Rows)
	}
	if !strings.Contains(got.Rows[1], "Asking") || !strings.Contains(got.Rows[1], "/other") {
		t.Errorf("the other Host's Session reads %q", got.Rows[1])
	}
	// The pair, on a row for a Session this page is not drawing.
	if !strings.Contains(got.Rows[2], "not answering") {
		t.Errorf("the Host that is not answering reads %q", got.Rows[2])
	}
}

// hostsUnder drives hosts.js over a page of cards. dom.js supplies the element,
// and machines.js the cards, so what runs is the file the browser is served.
func hostsUnder(t *testing.T, script string, into any) {
	t.Helper()
	node := findNode(t)

	var program strings.Builder
	for _, name := range []string{"testdata/el.js", "testdata/machines.js", "hosts.js"} {
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		program.WriteString(string(source))
		program.WriteString("\n")
	}
	program.WriteString("setTimeout(() => {\n" + script + "\n}, 0);")

	cmd := exec.Command(node, "-e", program.String())
	said, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, said)
	}
	if err := json.Unmarshal(said, into); err != nil {
		t.Fatalf("node said %q: %v", said, err)
	}
}

// The Vendor row comes from the vendors frame, and an unreachable Vendor empties
// it. A remembered list that outlived the Vendor that served it is the one thing
// the push exists to prevent.
func TestAnUnreachableVendorEmptiesItsRow(t *testing.T) {
	var got struct {
		Filled  string `json:"filled"`
		Emptied string `json:"emptied"`
		Other   string `json:"other"`
	}
	hostsUnder(t, `
opened.send("vendors", {host: "desk", vendors: [
  {kind: "ollama", base: "http://127.0.0.1:11434", reachable: true, resident: [{modelId: "qwen3.5-9b"}]},
]});
const filled = page.get("desk").row.textContent;

// The same Vendor, now not answering. What it was holding goes with it.
opened.send("vendors", {host: "desk", vendors: [
  {kind: "ollama", base: "http://127.0.0.1:11434", reachable: false, resident: []},
]});
console.log(JSON.stringify({
  filled,
  emptied: page.get("desk").row.textContent,
  other: page.get("attic").row.textContent,
}));
`, &got)

	if !strings.Contains(got.Filled, "qwen3.5-9b") {
		t.Errorf("the row read %q after the frame that filled it", got.Filled)
	}
	if strings.Contains(got.Emptied, "qwen3.5-9b") {
		t.Errorf("the row still holds %q after the Vendor stopped answering", got.Emptied)
	}
	if !strings.Contains(got.Emptied, "not answering") {
		t.Errorf("the row reads %q, and it has to say why it is empty", got.Emptied)
	}
	// A frame for one Host touches one Host.
	if !strings.Contains(got.Other, "waiting") {
		t.Errorf("another Host's row was rewritten: %q", got.Other)
	}
}

// Connecting keeps its content at full strength and marks its edge; only Down
// dims and stamps. That is the difference between reconnecting and gone.
func TestAHostStateFrameMovesTheCard(t *testing.T) {
	var got []string
	hostsUnder(t, `
const seen = [];
for (const f of [
  {host: "desk", state: "Connecting"},
  {host: "desk", state: "Down", cause: "unreachable"},
  {host: "desk", state: "Incompatible"},
  {host: "desk", state: "Ready"},
]) {
  opened.send("host", f);
  seen.push(page.get("desk").section.dataset.hostState + " " + page.get("desk").pill.textContent);
}
console.log(JSON.stringify(seen));
`, &got)

	// An Incompatible pill names the versions rather than the state alone, because
	// the user fixes it by updating a machine. TestAnIncompatibleCardNamesBothVersions
	// is where that sentence is read.
	want := []string{"Connecting Connecting", "Down Down unreachable", "Incompatible Incompatible: this Hub speaks 1, this Host speaks another", "Ready Ready"}
	if len(got) != len(want) {
		t.Fatalf("the card said %v", got)
	}
	for i, state := range want {
		if got[i] != state {
			t.Errorf("frame %d left the card %q, want %q", i+1, got[i], state)
		}
	}
}

// A Host that failed the Handshake is one the user fixes by updating a machine, so
// the card names both versions: the one this Hub requires and the one that Host
// answered with.
func TestAnIncompatibleCardNamesBothVersions(t *testing.T) {
	var got []string
	hostsUnder(t, `
const seen = [];
opened.send("host", {host: "desk", state: "Incompatible", speaks: [2]});
seen.push(page.get("desk").pill.textContent);
opened.send("host", {host: "desk", state: "Down", cause: "unreachable"});
seen.push(page.get("desk").pill.textContent);
console.log(JSON.stringify(seen));
`, &got)

	if len(got) != 2 {
		t.Fatalf("the card said %v", got)
	}
	for _, want := range []string{"Incompatible", "1", "2"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("the card reads %q, and it has to name %q", got[0], want)
		}
	}
	// A Down Host is not told about a version, because that is not why it is down.
	if strings.Contains(got[1], "speaks") {
		t.Errorf("a Down card reads %q", got[1])
	}
}

// The toast is the only path a question from a Session that is not on screen has
// to the user, because this layout hides every Session but one.
func TestAQuestionFromAnotherSessionRaisesAToast(t *testing.T) {
	var got struct {
		Toasts []string `json:"toasts"`
		Where  []string `json:"where"`
		Posted []string `json:"posted"`
		After  int      `json:"after"`
	}
	pageUnder(t, `
// Two questions, from two Sessions on two Hosts, at once.
opened.send("event", {host: "attic", session: "s-9", seq: 4, kind: "ApprovalRequested",
  payload: {toolCallId: "c1", title: "rm -rf build/"}});
// The same tool call id, from another Host. A tool call id is unique inside one
// Session and nowhere else.
opened.send("event", {host: "shed", session: "s-3", seq: 9, kind: "ApprovalRequested",
  payload: {toolCallId: "c1", title: "write out.txt"}});

const toasts = dom.toasts.children.map((t) => t.textContent);
const where = dom.toasts.children.map((t) => t.querySelector(".where").href);

// The user answers the first. The command goes to the Host that asked.
dom.toasts.children[0].children.find((c) => c.dataset.decision === "allowed").onclick();

// The Daemon's own decision comes back on the stream, and takes the toast down.
opened.send("event", {host: "attic", session: "s-9", seq: 5, kind: "ApprovalDecided",
  payload: {toolCallId: "c1", decision: "allowed", by: "user"}});

setTimeout(() => {
  console.log(JSON.stringify({
    toasts,
    where,
    posted: posted.map((p) => p.url + " " + p.body),
    after: dom.toasts.children.length,
  }));
}, 0);
`, &got)

	if len(got.Toasts) != 2 {
		t.Fatalf("two questions raised %d toasts: %v", len(got.Toasts), got.Toasts)
	}
	// Each names the Host and the Session it came from, because a question with no
	// machine on it is one the user cannot place.
	if !strings.Contains(got.Toasts[0], "s-9 on attic") || !strings.Contains(got.Toasts[0], "rm -rf build/") {
		t.Errorf("the first toast reads %q", got.Toasts[0])
	}
	if !strings.Contains(got.Toasts[1], "s-3 on shed") {
		t.Errorf("the second toast reads %q, and neither replaced the other", got.Toasts[1])
	}
	// Acting on it makes that Session the primary.
	if got.Where[0] != "/hosts/attic/sessions/s-9" {
		t.Errorf("the toast leads to %q", got.Where[0])
	}
	// The decision reaches the Host that asked, naming the call it answers.
	if len(got.Posted) != 1 || !strings.Contains(got.Posted[0], "/v1/hosts/attic/sessions/s-9/approvals") {
		t.Fatalf("the decision went to %v", got.Posted)
	}
	if !strings.Contains(got.Posted[0], `"decision":"allowed"`) || !strings.Contains(got.Posted[0], `"toolCallId":"c1"`) {
		t.Errorf("the decision said %q", got.Posted[0])
	}
	// The answered question's toast goes when the Daemon says it was decided, and
	// the other one stays: a command is an intention, and the Event is the fact.
	if got.After != 1 {
		t.Errorf("%d toasts are left, and one of two questions was answered", got.After)
	}
}

// A question from the Session on screen is drawn in the transcript, where it
// belongs. A toast for it would say the same thing twice.
func TestAQuestionFromTheSessionOnScreenRaisesNoToast(t *testing.T) {
	var got struct {
		Toasts int `json:"toasts"`
		Rows   int `json:"rows"`
	}
	pageUnder(t, `
opened.send("event", {host: "desk", session: "s-1", seq: 4, kind: "ApprovalRequested",
  payload: {toolCallId: "c1", title: "rm -rf build/"}});
setTimeout(() => {
  console.log(JSON.stringify({toasts: dom.toasts.children.length, rows: dom.transcript.children.length}));
}, 0);
`, &got)

	if got.Toasts != 0 {
		t.Errorf("the Session on screen raised %d toasts, and its question is already drawn", got.Toasts)
	}
	if got.Rows < 2 {
		t.Errorf("the question was not drawn in the transcript either: %d rows", got.Rows)
	}
}

// A decision that did not land leaves the toast up and the button usable. The
// question is open until an Event says otherwise, and a toast that went quiet
// would be the user believing they had answered.
func TestADecisionThatDoesNotLandLeavesTheToastUsable(t *testing.T) {
	var got struct {
		Toasts   int    `json:"toasts"`
		Disabled bool   `json:"disabled"`
		Why      string `json:"why"`
		Tries    int    `json:"tries"`
	}
	pageUnder(t, `
postAnswer = {ok: false, status: 502};
opened.send("event", {host: "attic", session: "s-9", seq: 4, kind: "ApprovalRequested",
  payload: {toolCallId: "c1", title: "rm -rf build/"}});

const toast = dom.toasts.children[0];
const allow = toast.children.find((c) => c.dataset.decision === "allowed");
allow.onclick();

setTimeout(() => {
  const after = {
    toasts: dom.toasts.children.length,
    disabled: allow.disabled === true,
    why: toast.querySelector(".why") ? toast.querySelector(".why").textContent : "",
  };
  // And a second try, which the button has to still allow.
  postAnswer = {ok: true, status: 202};
  allow.onclick();
  setTimeout(() => console.log(JSON.stringify({...after, tries: posted.length})), 0);
}, 0);
`, &got)

	if got.Toasts != 1 {
		t.Fatalf("%d toasts are up, and the question was never answered", got.Toasts)
	}
	if got.Disabled {
		t.Error("the button is still disabled, so the question cannot be answered again")
	}
	if !strings.Contains(got.Why, "502") {
		t.Errorf("the toast says %q, and it has to say what happened", got.Why)
	}
	if got.Tries != 2 {
		t.Errorf("the decision was sent %d times, and it was answered twice", got.Tries)
	}
}

// Every way a question ends takes its toast down. A toast left on screen for a
// question nobody can answer any more is worse than no toast: the user acts on it
// and nothing happens.
func TestAToastGoesWhateverEndedItsQuestion(t *testing.T) {
	var got []int
	pageUnder(t, `
const seen = [];
function raise(host, session, id) {
  opened.send("event", {host, session, seq: 1, kind: "ApprovalRequested", payload: {toolCallId: id, title: "t"}});
}

// The Tool Call ended without a decision, which is the Daemon's own synthesis
// when a Prompt completes with the call still open.
raise("attic", "s-9", "c1");
opened.send("event", {host: "attic", session: "s-9", seq: 2, kind: "ToolCallEnded",
  payload: {toolCallId: "c1", outcome: "unknown"}});
seen.push(dom.toasts.children.length);

// The Session ended under the question.
raise("attic", "s-9", "c2");
opened.send("event", {host: "attic", session: "s-9", seq: 3, kind: "SessionEnded", payload: {reason: "stopped"}});
seen.push(dom.toasts.children.length);

// A resync discards only questions from that Host. Every other Host on the merged
// stream keeps its own log and its open questions.
raise("attic", "s-9", "c3");
raise("shed", "s-3", "c4");
served.set("0", []);
opened.send("resync", {host: "attic"});
seen.push(dom.toasts.children.length);

// A resync for the primary Host also leaves questions from other Hosts alone.
opened.send("resync", {host: "desk"});
seen.push(dom.toasts.children.length);

console.log(JSON.stringify(seen));
`, &got)

	want := []string{"the Tool Call ended", "the Session ended", "another Host's resync", "the primary Host's resync"}
	left := []int{0, 0, 1, 1}
	if len(got) != len(want) {
		t.Fatalf("the page answered %v", got)
	}
	for i, why := range want {
		if got[i] != left[i] {
			t.Errorf("%d toasts are up after %s, want %d", got[i], why, left[i])
		}
	}
}

// Precedence: Host State, then the HTTP status, then an Event, then the
// operational log. A user looking at a Down Host is not also told that its Vendor
// stopped answering, because that is what a machine nobody can reach looks like.
func TestADownHostDoesNotAlsoRaiseItsVendorsFailure(t *testing.T) {
	var got struct {
		Ready string `json:"ready"`
		Down  string `json:"down"`
		After string `json:"after"`
	}
	hostsUnder(t, `
opened.send("vendors", {host: "desk", vendors: [
  {kind: "ollama", base: "http://127.0.0.1:11434", reachable: true, resident: [{modelId: "qwen3.5-9b"}]},
]});
const ready = page.get("desk").row.textContent;

// The Host goes. Its Vendor row says the Host is not answering, and not that its
// Vendor is: one failure reaches the user, and it is the one furthest up.
opened.send("host", {host: "desk", state: "Down", cause: "unreachable"});
const down = page.get("desk").row.textContent;

// A vendors frame that was already in flight when the Host went changes nothing.
opened.send("vendors", {host: "desk", vendors: [
  {kind: "ollama", base: "http://127.0.0.1:11434", reachable: false, resident: []},
]});
console.log(JSON.stringify({ready, down, after: page.get("desk").row.textContent}));
`, &got)

	if !strings.Contains(got.Ready, "qwen3.5-9b") {
		t.Fatalf("the row read %q while the Host was answering", got.Ready)
	}
	if !strings.Contains(got.Down, "this Host is not answering") {
		t.Errorf("a Down Host's Vendor row reads %q", got.Down)
	}
	if strings.Contains(got.Down, "not answering") && strings.Count(got.Down, "not answering") != 1 {
		t.Errorf("the row says it twice: %q", got.Down)
	}
	if got.After != got.Down {
		t.Errorf("a late vendors frame rewrote a Down Host's row: %q", got.After)
	}
}

// Down dims and stamps, live as well as on the server. A Host that goes Down while
// this page is open would otherwise keep content that claims to be current.
func TestALiveHostGoingDownStampsItsCard(t *testing.T) {
	var got struct {
		Down string `json:"down"`
		Back string `json:"back"`
		Told string `json:"told"`
	}
	hostsUnder(t, `
opened.send("host", {host: "desk", state: "Down", cause: "unreachable"});
const down = page.get("desk").who.textContent;

// Ready again, and the card is current: nothing on it says how old it is.
opened.send("host", {host: "desk", state: "Ready"});
const back = page.get("desk").who.textContent;

// A frame that carries the Hub's own time is stamped with that rather than with
// when this page was drawn.
opened.send("host", {host: "attic", state: "Down", since: "2026-09-02T08:30:00Z"});
console.log(JSON.stringify({down, back, told: page.get("attic").who.textContent}));
`, &got)

	if !strings.Contains(got.Down, "last answered at 2026-09-02T09:00:00Z") {
		t.Errorf("the card that went Down reads %q, and it has to say how old it is", got.Down)
	}
	if strings.Contains(got.Back, "last answered at") {
		t.Errorf("the card is current again and still reads %q", got.Back)
	}
	if !strings.Contains(got.Told, "last answered at 2026-09-02T08:30:00Z") {
		t.Errorf("the card reads %q, and the Hub told it when it last heard from that Host", got.Told)
	}
}

func TestHostFramesShowStateCauseMarkAndStaleStamp(t *testing.T) {
	var got struct {
		Connecting struct {
			State string `json:"state"`
			Mark  string `json:"mark"`
			Stale string `json:"stale"`
		} `json:"connecting"`
		Down struct {
			State string `json:"state"`
			Cause string `json:"cause"`
			Mark  string `json:"mark"`
			Stale string `json:"stale"`
		} `json:"down"`
	}
	pageUnder(t, `
opened.send("host", {host: "desk", state: "Connecting"});
const connecting = {
  state: dom.hostStateElement.textContent,
  mark: dom.hostMarkElement.textContent,
  stale: dom.staleElement.textContent,
};
opened.send("host", {host: "desk", state: "Down", cause: "no-daemon", at: 1788345900000000});
const down = {
  state: dom.hostStateElement.textContent,
  cause: dom.hostCauseElement.textContent,
  mark: dom.hostMarkElement.textContent,
  stale: dom.staleElement.textContent,
};
console.log(JSON.stringify({connecting, down}));
`, &got)

	if got.Connecting.State != "Connecting" || got.Connecting.Mark != "reconnecting" || got.Connecting.Stale != "" {
		t.Errorf("Connecting drew %+v", got.Connecting)
	}
	if got.Down.State != "Down" || got.Down.Cause != "no-daemon" || got.Down.Mark != "" {
		t.Errorf("Down drew %+v", got.Down)
	}
	if !strings.Contains(got.Down.Stale, "Stale") {
		t.Errorf("Down has no visible Stale stamp: %q", got.Down.Stale)
	}
}

func TestConnectingHostsStayAtFullStrength(t *testing.T) {
	css, err := os.ReadFile("page.css")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(css), `body[data-host-state="Connecting"] { opacity`) ||
		strings.Contains(string(css), `body[data-host-state="Down"], body[data-host-state="Connecting"]`) {
		t.Fatal("Connecting is dimmed with Down")
	}
}

func TestVendorFramesReplaceTheVisibleVendorState(t *testing.T) {
	var got struct {
		First  string `json:"first"`
		Second string `json:"second"`
	}
	pageUnder(t, `
opened.send("vendors", {host: "desk", vendors: [{
  kind: "ollama", base: "http://127.0.0.1:11434", reachable: true,
  resident: [{modelId: "qwen3", loadedContext: 8192, vram: 1024}],
}]});
const first = dom.vendorsElement.textContent;
opened.send("vendors", {host: "desk", vendors: [{
  kind: "ollama", base: "http://127.0.0.1:11434", reachable: false, resident: [],
}]});
console.log(JSON.stringify({first, second: dom.vendorsElement.textContent}));
`, &got)

	if !strings.Contains(got.First, "ollama") || !strings.Contains(got.First, "qwen3") {
		t.Errorf("the first Vendor frame drew %q", got.First)
	}
	if !strings.Contains(got.Second, "ollama") || !strings.Contains(got.Second, "unreachable") {
		t.Errorf("the changed Vendor frame drew %q", got.Second)
	}
	if strings.Contains(got.Second, "qwen3") {
		t.Errorf("the changed Vendor frame kept the old resident Model: %q", got.Second)
	}
}

func TestAFailedResyncKeepsTheTranscript(t *testing.T) {
	var got struct {
		Rows int    `json:"rows"`
		Text string `json:"text"`
	}
	pageUnder(t, `
served.set("0", "refused");
opened.send("resync", {host: "desk"});
setTimeout(() => console.log(JSON.stringify({
  rows: dom.transcript.children.length,
  text: dom.transcript.children[0]?.textContent ?? "",
})), 0);
`, &got)

	if got.Rows != 1 || !strings.Contains(got.Text, "Session started") {
		t.Errorf("a failed resync left %d rows saying %q", got.Rows, got.Text)
	}
}

func TestAnEventThatArrivesDuringAResyncSurvivesTheCommit(t *testing.T) {
	var got struct {
		Rows  int    `json:"rows"`
		State string `json:"state"`
	}
	pageUnder(t, `
let answer;
// The rail reads through the same fetch, and this test holds the transcript's
// read open, so the rail keeps the fixture's answer.
const rail = globalThis.fetch;
globalThis.fetch = (url) => url.startsWith("/rail/") ? rail(url) : new Promise((resolve) => {
  answer = () => resolve({ok: true, json: async () => ({events: []})});
});
opened.send("resync", {host: "desk"});
opened.send("event", {host: "desk", session: "s-1", seq: 2, kind: "SessionReady", payload: {model: "qwen3"}});
answer();
setTimeout(() => console.log(JSON.stringify({
  rows: dom.transcript.children.length,
  state: dom.stateElement.textContent,
})), 0);
`, &got)

	if got.Rows != 1 || got.State != "Idle" {
		t.Errorf("the resync commit left %d rows and State %q", got.Rows, got.State)
	}
}

// The stylesheet draws Asking and Ended off one attribute, and the live update
// has to write that same one. An update that wrote another name would leave the
// pill's text right and its styling stuck on whatever the first paint drew.
func TestTheLiveStateWritesTheAttributeTheStylesheetReads(t *testing.T) {
	var got string
	pageUnder(t, `
const frames = [
  {seq: 1, kind: "SessionStarted", payload: {harness: "opencode"}},
  {seq: 2, kind: "PromptSubmitted", payload: {text: "go"}},
  {seq: 3, kind: "ToolCallRequested", payload: {toolCallId: "c1", name: "bash"}},
  {seq: 4, kind: "ApprovalRequested", payload: {toolCallId: "c1", title: "run it"}},
];
for (const f of frames) opened.send("event", {host: "desk", session: "s-1", ...f});
console.log(JSON.stringify(dom.stateElement.dataset.sessionState));
`, &got)

	if got != "Asking" {
		t.Errorf("the state pill carries %q, and the stylesheet reads data-session-state", got)
	}
	css, err := files.ReadFile("page.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), `[data-session-state^="Asking"]`) {
		t.Error("the stylesheet no longer draws Asking off data-session-state, so the page and it have parted")
	}
}

// The Host half of the pair is the Host State itself. It used to be a pill the
// server drew once and nothing ever wrote again, so a Session on a Host that went
// Down kept a pill reading "answering".
func TestTheHostPillFollowsTheHostFrame(t *testing.T) {
	var got struct {
		Down string `json:"down"`
		Back string `json:"back"`
	}
	pageUnder(t, `
opened.send("host", {host: "desk", state: "Down", cause: "no-daemon"});
const down = dom.hostStateElement.dataset.hostState;
opened.send("host", {host: "desk", state: "Ready"});
console.log(JSON.stringify({down, back: dom.hostStateElement.dataset.hostState}));
`, &got)

	if got.Down != "Down" || got.Back != "Ready" {
		t.Errorf("the pill read %q then %q", got.Down, got.Back)
	}
}

// Nothing on the Session page says how the Host is except the pair's Host half,
// which the stream writes. A second, frozen answer is a second answer that is wrong.
func TestTheSessionHeaderCarriesNoFrozenHostPill(t *testing.T) {
	markup, err := os.ReadFile("page.html")
	if err != nil {
		t.Fatal(err)
	}
	_, desk, found := strings.Cut(string(markup), `<main class="desk">`)
	if !found {
		t.Fatal("page.html has no desk")
	}
	header, _, found := strings.Cut(desk, "</header>")
	if !found {
		t.Fatal("page.html has no header")
	}
	if strings.Contains(header, "data-host-answering") {
		t.Error("the header still draws a Host pill the stream never writes")
	}
}

// The three commands, which are the whole of what a user can say to a Session.
// Each one posts to its own route and draws nothing: what it changed comes back
// on the Event stream.

// sent is one POST as the stub recorded it.
type sent struct {
	URL  string `json:"url"`
	Body string `json:"body"`
}

// idle drives the page to Idle, which is the State a Prompt is taken in.
const idle = `
opened.send("event", {host: "desk", session: "s-1", seq: 2, kind: "SessionStarted", payload: {harness: "opencode"}});
opened.send("event", {host: "desk", session: "s-1", seq: 3, kind: "SessionReady", payload: {model: "m"}});
`

func TestSendingAPromptPostsItAndEmptiesTheBox(t *testing.T) {
	var got struct {
		Posted []sent `json:"posted"`
		Left   string `json:"left"`
	}
	pageUnder(t, idle+`
dom.promptBox.value = "  count to three  ";
await dom.sendButton.onclick();
console.log(JSON.stringify({posted, left: dom.promptBox.value}));
`, &got)

	if len(got.Posted) != 1 {
		t.Fatalf("posted %v", got.Posted)
	}
	if got.Posted[0].URL != "/v1/hosts/desk/sessions/s-1/prompts" {
		t.Errorf("the Prompt went to %q", got.Posted[0].URL)
	}
	if got.Posted[0].Body != `{"text":"count to three"}` {
		t.Errorf("the Prompt was sent as %q", got.Posted[0].Body)
	}
	if got.Left != "" {
		t.Errorf("the box still holds %q", got.Left)
	}
}

// A Prompt the Host would not take stays in the box, so the user's words are not
// lost, and the Daemon's own sentence says why.
func TestAPromptTheHostRefusedStaysInTheBoxAndSaysWhy(t *testing.T) {
	var got struct {
		Left string `json:"left"`
		Why  string `json:"why"`
	}
	pageUnder(t, idle+`
postAnswer = {ok: false, status: 409, json: async () => ({detail: "the Session is Working"})};
dom.promptBox.value = "go";
await dom.sendButton.onclick();
console.log(JSON.stringify({left: dom.promptBox.value, why: dom.sendRow.textContent}));
`, &got)

	if got.Left != "go" {
		t.Errorf("the box holds %q", got.Left)
	}
	if !strings.Contains(got.Why, "the Session is Working") {
		t.Errorf("the page said %q", got.Why)
	}
}

// A Host that could not be reached is a different sentence from one that answered,
// because they are different things to do something about.
func TestAPromptToAHostThatIsNotThereSaysSo(t *testing.T) {
	var got string
	pageUnder(t, idle+`
postAnswer = "unreachable";
dom.promptBox.value = "go";
await dom.sendButton.onclick();
console.log(JSON.stringify(dom.sendRow.textContent));
`, &got)

	if !strings.Contains(got, "could not be reached") {
		t.Errorf("the page said %q", got)
	}
}

func TestStopAndInterruptPostToTheirOwnRoutes(t *testing.T) {
	var got []sent
	pageUnder(t, `
opened.send("event", {host: "desk", session: "s-1", seq: 2, kind: "SessionStarted", payload: {harness: "opencode"}});
opened.send("event", {host: "desk", session: "s-1", seq: 3, kind: "SessionReady", payload: {model: "m"}});
opened.send("event", {host: "desk", session: "s-1", seq: 4, kind: "PromptSubmitted", payload: {text: "go"}});
await dom.interruptButton.onclick();
await dom.stopButton.onclick();
console.log(JSON.stringify(posted));
`, &got)

	if len(got) != 2 {
		t.Fatalf("posted %v", got)
	}
	if got[0].URL != "/v1/hosts/desk/sessions/s-1/interrupt" {
		t.Errorf("the interrupt went to %q", got[0].URL)
	}
	if got[1].URL != "/v1/hosts/desk/sessions/s-1/stop" {
		t.Errorf("the stop went to %q", got[1].URL)
	}
}

// offered is the three commands, in the order the page shows them, for one State.
type offered struct {
	Prompt    bool `json:"prompt"`
	Interrupt bool `json:"interrupt"`
	Stop      bool `json:"stop"`
}

// Only the commands the Daemon takes in a State are offered in it. These are the
// same lists internal/daemon/commands.go passes to allow, and a page that offered
// more would send the user commands that can only be refused.
func TestOnlyTheCommandsTheStateTakesAreOffered(t *testing.T) {
	var got map[string]offered
	pageUnder(t, `
const frames = [
  [null, "Starting"],
  [{seq: 2, kind: "SessionStarted", payload: {harness: "opencode"}}, "Starting"],
  [{seq: 3, kind: "SessionReady", payload: {model: "m"}}, "Idle"],
  [{seq: 4, kind: "PromptSubmitted", payload: {text: "go"}}, "Working"],
  [{seq: 5, kind: "ToolCallRequested", payload: {toolCallId: "c1", name: "bash"}}, "Working"],
  [{seq: 6, kind: "ApprovalRequested", payload: {toolCallId: "c1", title: "run"}}, "Asking"],
  [{seq: 7, kind: "SessionEnded", payload: {reason: "stopped"}}, "Ended"],
];
const seen = {};
for (const [frame, state] of frames) {
  if (frame) opened.send("event", {host: "desk", session: "s-1", ...frame});
  seen[state] = {
    prompt: !dom.sendButton.disabled,
    interrupt: !dom.interruptButton.disabled,
    stop: !dom.stopButton.disabled,
  };
}
console.log(JSON.stringify(seen));
`, &got)

	want := map[string]offered{
		"Starting": {Stop: true},
		"Idle":     {Prompt: true, Stop: true},
		"Working":  {Interrupt: true, Stop: true},
		"Asking":   {Interrupt: true, Stop: true},
		"Ended":    {},
	}
	for state, want := range want {
		if got[state] != want {
			t.Errorf("%s offers %+v, want %+v", state, got[state], want)
		}
	}
}

// A box that will not take a Prompt says why, so a dimmed composer is an answer
// rather than a page that looks broken.
func TestTheBoxSaysWhyItWillNotTakeAPrompt(t *testing.T) {
	var got []string
	pageUnder(t, `
const said = [dom.promptBox.placeholder];
opened.send("event", {host: "desk", session: "s-1", seq: 2, kind: "SessionStarted", payload: {harness: "opencode"}});
opened.send("event", {host: "desk", session: "s-1", seq: 3, kind: "SessionReady", payload: {model: "m"}});
said.push(dom.promptBox.placeholder);
opened.send("event", {host: "desk", session: "s-1", seq: 4, kind: "PromptSubmitted", payload: {text: "go"}});
said.push(dom.promptBox.placeholder);
console.log(JSON.stringify(said));
`, &got)

	want := []string{"this Session is Starting", "type a Prompt", "this Session is Working"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("the box said %q, want %q", got, want)
	}
}

// A failure says what was true when the command was refused, so it goes when the
// State it named has moved on. One that stayed would be the page reporting
// something that is no longer happening.
func TestAFailureGoesWhenTheStateItNamedHasMovedOn(t *testing.T) {
	var got struct {
		Refused  string `json:"refused"`
		StillOn  string `json:"stillOn"`
		StateGon string `json:"stateGone"`
	}
	pageUnder(t, idle+`
postAnswer = {ok: false, status: 409, json: async () => ({detail: "the Session is Idle"})};
await dom.stopButton.onclick();
const refused = dom.pair.textContent;

// An Event that leaves the Session Idle. The refusal is still true, so it stays.
opened.send("event", {host: "desk", session: "s-1", seq: 4, kind: "FutureKind", payload: {}});
const stillOn = dom.pair.textContent;

// And one that moves it, which is what the refusal was about.
opened.send("event", {host: "desk", session: "s-1", seq: 5, kind: "PromptSubmitted", payload: {text: "go"}});
console.log(JSON.stringify({refused, stillOn, stateGone: dom.pair.textContent}));
`, &got)

	if !strings.Contains(got.Refused, "the Session is Idle") {
		t.Errorf("the refusal said %q", got.Refused)
	}
	if !strings.Contains(got.StillOn, "the Session is Idle") {
		t.Errorf("the refusal went while its State was still true: %q", got.StillOn)
	}
	if strings.Contains(got.StateGon, "the Session is Idle") {
		t.Errorf("the refusal outlived its State: %q", got.StateGon)
	}
}

// An Approval decision and the three commands on the page are one shape, so a
// decision that was refused reads the Daemon's own sentence too.
func TestADecisionCarriesTheDaemonsOwnRefusal(t *testing.T) {
	var got string
	pageUnder(t, `
postAnswer = {ok: false, status: 409, json: async () => ({detail: "that Tool Call was already decided"})};
opened.send("event", {host: "attic", session: "s-9", seq: 4, kind: "ApprovalRequested",
  payload: {toolCallId: "c1", title: "rm -rf build/"}});
const toast = dom.toasts.children[0];
await toast.children.find((c) => c.dataset.decision === "allowed").onclick();
console.log(JSON.stringify(toast.querySelector(".why").textContent));
`, &got)

	if !strings.Contains(got, "Allow again: that Tool Call was already decided") {
		t.Errorf("the toast says %q", got)
	}
}
