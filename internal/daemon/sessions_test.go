package daemon

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/eventlog"
	"github.com/VictorJohnOkoh/Dispatch/internal/harness"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"github.com/VictorJohnOkoh/Dispatch/internal/vendors"
	"github.com/VictorJohnOkoh/Dispatch/internal/workspace"

	_ "modernc.org/sqlite"
)

// host is one Daemon a test drives, with its Workspace Root and its Event log in
// the directory the test owns, and its Vendor already polled once so a Model is
// known.
type host struct {
	*Daemon
	root    string
	logPath string
	vendor  *fake
	chat    *chatFake
	lines   *lines
}

func newHost(t *testing.T) *host {
	t.Helper()
	dir := t.TempDir()
	root, err := workspace.NewRoot(dir)
	if err != nil {
		t.Fatalf("NewRoot: %v", err)
	}
	path := filepath.Join(dir, "events.db")
	events, err := eventlog.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { events.Close() })

	f := ollamaFake()
	chat := &chatFake{}
	written := &lines{}
	d := New(slog.New(slog.NewTextHandler(written, nil)), events, root,
		[]vendors.Adapter{f}, []Harness{{Name: "passthrough", Adapter: harness.NewPassthrough(chat)}})
	d.vendors.pollAll(t.Context())

	return &host{Daemon: d, root: dir, logPath: path, vendor: f, chat: chat, lines: written}
}

func (h *host) post(t *testing.T, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return w
}

// started reads back the answer to a start that was accepted.
func (h *host) started(t *testing.T, w *httptest.ResponseRecorder) startedBody {
	t.Helper()
	if w.Code != protocol.StatusStarted {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body startedBody
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if body.Session == "" || body.Seq == 0 {
		t.Fatalf("answer = %+v", body)
	}
	return body
}

func (h *host) refusal(t *testing.T, w *httptest.ResponseRecorder, status int) protocol.Refusal {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body.String())
	}
	var r protocol.Refusal
	if err := json.NewDecoder(w.Body).Decode(&r); err != nil {
		t.Fatalf("refusal: %v", err)
	}
	return r
}

func (h *host) list(t *testing.T) []SessionView {
	t.Helper()
	w := httptest.NewRecorder()
	h.handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/sessions", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct{ Sessions []SessionView }
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("list: %v", err)
	}
	return body.Sessions
}

// kinds is every Event in the file, in Seq order. It reads the file on a
// connection of its own, so what it sees is what was committed.
func (h *host) kinds(t *testing.T) []string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+h.logPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT kind FROM events ORDER BY seq`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, kind)
	}
	return out
}

// message reads back an appendable Event's text and whether it is complete.
func (h *host) message(t *testing.T, seq int) (string, bool) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+h.logPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer db.Close()

	var payload []byte
	if err := db.QueryRow(`SELECT payload FROM events WHERE seq = ?`, seq).Scan(&payload); err != nil {
		t.Fatalf("no Event at %d: %v", seq, err)
	}
	var body struct {
		Text     string `json:"text"`
		Complete bool   `json:"complete"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("payload at %d: %v", seq, err)
	}
	return body.Text, body.Complete
}

// waitState waits for a Session to reach a state, because the launch runs after
// the answer has gone out, which is the whole point of Starting.
func (h *host) waitState(t *testing.T, id event.SessionID, want string) SessionView {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var found SessionView
		for _, s := range h.list(t) {
			if s.ID == id {
				found = s
			}
		}
		if found.State.String() == want && found.ID == id {
			return found
		}
		if time.Now().After(deadline) {
			t.Fatalf("Session %s is %+v, want %s", id, found, want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

const startBody = `{"harness":"passthrough","model":"qwen3:8b"}`

// A start is accepted, the answer carries the Session id and the Seq of its
// SessionStarted, and the launch that follows writes SessionReady.
func TestStartSessionAnswersWithTheSessionID(t *testing.T) {
	h := newHost(t)

	answer := h.started(t, h.post(t, "/v1/sessions", startBody))
	h.waitState(t, answer.Session, "Idle")

	if got := h.kinds(t); len(got) != 2 || got[0] != "SessionStarted" || got[1] != "SessionReady" {
		t.Fatalf("log = %v, want SessionStarted then SessionReady", got)
	}
	if got := h.vendor.loads(); len(got) != 1 || got[0] != "qwen3:8b" {
		t.Errorf("Load called with %v", got)
	}

	view := h.list(t)[0]
	if view.ID != answer.Session || view.Harness != "passthrough" || view.Model != "qwen3:8b" {
		t.Errorf("view = %+v", view)
	}
	if view.Cwd != h.root {
		t.Errorf("cwd = %q, want the Workspace Root %q", view.Cwd, h.root)
	}
}

// Load is called during Starting and SessionReady waits for it, which is what
// keeps a cold Model load out of the first Prompt.
func TestSessionReadyWaitsForTheModelToLoad(t *testing.T) {
	h := newHost(t)
	loading := make(chan struct{})
	h.vendor.loading = loading
	h.vendor.gate = make(chan struct{})

	answer := h.started(t, h.post(t, "/v1/sessions", startBody))
	<-loading

	if got := h.kinds(t); len(got) != 1 || got[0] != "SessionStarted" {
		t.Fatalf("log = %v, want SessionStarted alone while the Model loads", got)
	}
	if got := h.vendor.loads(); len(got) != 1 {
		t.Fatalf("Load called %d times during Starting, want once", len(got))
	}

	close(h.vendor.gate)
	h.waitState(t, answer.Session, "Idle")
}

// A Model that will not load before SessionReady is a failed start: the record
// exists and the Session does not.
func TestAModelThatWillNotLoadEndsTheSession(t *testing.T) {
	h := newHost(t)
	h.vendor.loadErr = errNoModel

	answer := h.started(t, h.post(t, "/v1/sessions", startBody))
	view := h.waitState(t, answer.Session, "Ended")

	if view.EndReason != event.EndFailed {
		t.Errorf("end reason = %q", view.EndReason)
	}
	if got := h.kinds(t); len(got) != 3 || got[1] != "Error" || got[2] != "SessionEnded" {
		t.Fatalf("log = %v, want SessionStarted, Error, SessionEnded", got)
	}
}

// One Session at a time on a Host. The second start is refused with the Session
// holding the slot named, it writes no Event, and it does write an slog line.
func TestASecondStartIsRefusedAndNamesTheSessionHoldingTheSlot(t *testing.T) {
	h := newHost(t)
	first := h.started(t, h.post(t, "/v1/sessions", startBody))
	h.waitState(t, first.Session, "Idle")
	before := h.kinds(t)

	r := h.refusal(t, h.post(t, "/v1/sessions", startBody), protocol.StatusConflict)
	if r.Reason != protocol.ReasonAdmission {
		t.Errorf("reason = %q", r.Reason)
	}
	if len(r.Blocking) != 1 || r.Blocking[0] != string(first.Session) {
		t.Errorf("blocking = %v, want %s", r.Blocking, first.Session)
	}
	if r.Detail == "" {
		t.Error("the refusal carries no sentence to show")
	}

	if got := h.kinds(t); len(got) != len(before) {
		t.Errorf("the refusal wrote %d Events, want none", len(got)-len(before))
	}
	if !strings.Contains(h.lines.String(), "a Session start was refused") {
		t.Errorf("the operational log = %q", h.lines.String())
	}
	if got := h.list(t); len(got) != 1 {
		t.Errorf("%d Sessions, want only the one that was admitted", len(got))
	}
}

// Two starts arriving together cannot both read an empty Host and both be
// admitted, because admission and the registry entry it decided on are one step.
func TestTwoStartsArrivingTogetherAdmitOne(t *testing.T) {
	h := newHost(t)

	codes := make(chan int, 4)
	var wg sync.WaitGroup
	for range cap(codes) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- h.post(t, "/v1/sessions", startBody).Code
		}()
	}
	wg.Wait()
	close(codes)

	started := 0
	for code := range codes {
		switch code {
		case protocol.StatusStarted:
			started++
		case protocol.StatusConflict:
		default:
			t.Errorf("status %d", code)
		}
	}
	if started != 1 {
		t.Errorf("%d starts were admitted, want 1", started)
	}
}

// The slot is free again once the Session holding it has ended, and the ended one
// keeps its row in the list.
func TestAStartIsAdmittedOnceTheSessionBeforeItEnded(t *testing.T) {
	h := newHost(t)
	h.vendor.loadErr = errNoModel
	first := h.started(t, h.post(t, "/v1/sessions", startBody))
	h.waitState(t, first.Session, "Ended")

	h.vendor.loadErr = nil
	second := h.started(t, h.post(t, "/v1/sessions", startBody))
	h.waitState(t, second.Session, "Idle")

	got := h.list(t)
	if len(got) != 2 || got[0].ID != first.Session || got[1].ID != second.Session {
		t.Fatalf("list = %+v, want the ended one and then the live one", got)
	}
}

// The working directory is contained before the Session exists, so a directory
// outside the Workspace Root leaves nothing behind.
func TestADirectoryOutsideTheWorkspaceRootIsRefused(t *testing.T) {
	h := newHost(t)

	w := h.post(t, "/v1/sessions", `{"harness":"passthrough","model":"qwen3:8b","dir":"../elsewhere"}`)
	r := h.refusal(t, w, protocol.StatusUnprocessable)
	if r.Reason != protocol.ReasonWorkspace {
		t.Errorf("reason = %q", r.Reason)
	}
	if got := h.list(t); len(got) != 0 {
		t.Errorf("%d Sessions, want none", len(got))
	}
	if got := h.kinds(t); len(got) != 0 {
		t.Errorf("log = %v, want no Event", got)
	}
}

// A directory inside the Root is resolved against the Root, so a relative one
// means what the user thinks it means.
func TestADirectoryInsideTheWorkspaceRootIsTheSessionsCwd(t *testing.T) {
	h := newHost(t)
	if err := os.Mkdir(filepath.Join(h.root, "project"), 0o750); err != nil {
		t.Fatal(err)
	}

	answer := h.started(t, h.post(t, "/v1/sessions", `{"harness":"passthrough","model":"qwen3:8b","dir":"project"}`))
	view := h.waitState(t, answer.Session, "Idle")
	if want := filepath.Join(h.root, "project"); view.Cwd != want {
		t.Errorf("cwd = %q, want %q", view.Cwd, want)
	}
}

// The three ways a start is the request's own fault. All are 422 and all are
// answered before any Session exists.
func TestAStartIsRefusedForWhatThisHostDoesNotHave(t *testing.T) {
	for _, c := range []struct {
		name   string
		body   string
		reason protocol.Reason
	}{
		{"harness", `{"harness":"opencode","model":"qwen3:8b"}`, protocol.ReasonUnknownHarness},
		{"model", `{"harness":"passthrough","model":"nothing:1b"}`, protocol.ReasonUnknownModel},
		{"body", `{"harness":`, protocol.ReasonMalformed},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHost(t)
			r := h.refusal(t, h.post(t, "/v1/sessions", c.body), protocol.StatusUnprocessable)
			if r.Reason != c.reason {
				t.Errorf("reason = %q, want %q", r.Reason, c.reason)
			}
			if got := h.list(t); len(got) != 0 {
				t.Errorf("%d Sessions, want none", len(got))
			}
		})
	}
}

// The mux registers the two routes as protocol spells them, so the two cannot
// drift.
func TestTheSessionRoutesAreTheOnesProtocolNames(t *testing.T) {
	if protocol.StartSession != "POST /v1/sessions" || protocol.ListSessions != "GET /v1/sessions" {
		t.Fatalf("routes = %q, %q", protocol.StartSession, protocol.ListSessions)
	}
}

// page reads back one page of GET /v1/sessions/{id}/events.
func (h *host) page(t *testing.T, path string) []protocol.Event {
	t.Helper()
	w := get(t, h.Daemon, path)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body struct{ Events []protocol.Event }
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("page: %v", err)
	}
	return body.Events
}

func TestSessionEventsPagesOneSessionAndNoOther(t *testing.T) {
	h := newHost(t)
	mine := &Session{id: "s-mine", cancel: func() {}}
	other := &Session{id: "s-other", cancel: func() {}}
	h.sessions.add(mine)
	h.sessions.add(other)
	h.write(mine, event.KindPromptSubmitted, &event.PromptSubmitted{Text: "mine"})
	h.write(other, event.KindPromptSubmitted, &event.PromptSubmitted{Text: "other"})
	h.write(mine, event.KindPromptSubmitted, &event.PromptSubmitted{Text: "mine again"})

	page := h.page(t, "/v1/sessions/s-mine/events")
	if len(page) != 2 {
		t.Fatalf("%d Events, want 2: %+v", len(page), page)
	}
	for _, e := range page {
		if e.Session != "s-mine" {
			t.Fatalf("the page carries %s", e.Session)
		}
	}

	if page := h.page(t, "/v1/sessions/s-mine/events?after=1&limit=1"); len(page) != 1 || page[0].Seq != 3 {
		t.Fatalf("after=1&limit=1 = %+v", page)
	}
	// A page past the end of a Session that does exist is empty and not a refusal,
	// which is the difference between read it all and never heard of it.
	if page := h.page(t, "/v1/sessions/s-mine/events?after=99"); len(page) != 0 {
		t.Errorf("past the end = %+v", page)
	}
	// A size of zero would page forever, so it is clipped to one.
	if page := h.page(t, "/v1/sessions/s-mine/events?limit=0"); len(page) != 1 {
		t.Errorf("limit=0 = %+v, want one Event", page)
	}
	if w := get(t, h.Daemon, "/v1/sessions/s-gone/events"); w.Code != protocol.StatusNoSession {
		t.Errorf("an unknown Session answers %d", w.Code)
	}
	if w := get(t, h.Daemon, "/v1/sessions/s-mine/events?limit=nine"); w.Code != protocol.StatusUnprocessable {
		t.Errorf("a limit that is not a number answers %d", w.Code)
	}
}
