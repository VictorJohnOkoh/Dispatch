package web

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// page.js is the wiring, and this drives it under node against a page small
// enough to read: testdata/dom.js is the surface page.js actually touches. What
// runs here is the file the browser is served, not a copy of it.

// pageUnder loads fold.js, render.js and page.js over the stub page, runs the
// test's own script, and decodes what it printed.
func pageUnder(t *testing.T, script string, into any) {
	t.Helper()
	node := findNode(t)

	var program strings.Builder
	for _, name := range []string{"testdata/dom.js", "fold.js", "render.js", "page.js"} {
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		program.WriteString(string(source))
		program.WriteString("\n")
	}
	// The script runs after the page's own load() has settled, because that one is
	// asynchronous and everything a test asserts on comes after it.
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
