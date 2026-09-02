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
  seen.push(dom.stateLine.textContent);
}
console.log(JSON.stringify(seen));
`, &got)

	want := []string{"Starting", "Idle", "Working", "Working", "Asking", "Working", "Working", "Idle", "Ended stopped"}
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
		Dropped string `json:"dropped"`
		Final   string `json:"final"`
	}
	pageUnder(t, `
opened.send("event", {host: "desk", session: "s-1", seq: 1, kind: "AssistantMessage", payload: {text: "", complete: false}});
opened.send("delta", {host: "desk", seq: 1, n: 0, text: "the "});
// This one never arrives: the page misses "quick " entirely.
opened.send("delta", {host: "desk", seq: 1, n: 10, text: "brown fox"});
const dropped = dom.transcript.querySelector(".text").textContent;
opened.send("delta", {host: "desk", seq: 1, n: 0, text: "the quick brown fox", final: true});
console.log(JSON.stringify({dropped, final: dom.transcript.querySelector(".text").textContent}));
`, &got)

	// The page could not know what it missed, so it holds a repaired-from-where
	// version until the end.
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
		Fetched []string `json:"fetched"`
		Open    bool     `json:"open"`
		State   string   `json:"state"`
	}
	pageUnder(t, `
opened.send("event", {host: "desk", session: "s-1", seq: 1, kind: "SessionStarted", payload: {harness: "opencode"}});
opened.send("event", {host: "desk", session: "s-1", seq: 2, kind: "SessionReady", payload: {model: "m"}});

// A resync for another Host changes nothing here.
opened.send("resync", {host: "attic"});
const before = dom.transcript.children.length;

// The refetch answers with the one Event the log now holds.
served.push({events: [{seq: 1, kind: "SessionStarted", payload: {harness: "opencode"}}]});
opened.send("resync", {host: "desk"});
setTimeout(() => {
  console.log(JSON.stringify({
    rows: dom.transcript.children.length,
    fetched,
    open: opened.listeners.size > 0,
    state: dom.stateLine.textContent,
    before,
  }));
}, 0);
`, &got)

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
