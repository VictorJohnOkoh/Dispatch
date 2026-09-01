package hub_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/daemon"
	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/eventlog"
	"github.com/VictorJohnOkoh/Dispatch/internal/hub"
	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"github.com/VictorJohnOkoh/Dispatch/internal/workspace"
)

// torn is longer than the log's flush threshold, so the message it fills is
// written to the row while it is still open. That is what a tab closed mid-Prompt
// leaves behind.
var torn = strings.Repeat("a", eventlog.FlushThreshold+1)

// The first paint carries the transcript, so the page is readable with JS turned
// off, and an Event Kind this build has never heard of is a row like any other.
func TestTheFirstPaintCarriesTheTranscript(t *testing.T) {
	h, open := hostWithATranscript(t)

	body, resp := get(t, h, "/hosts/desk/sessions/s-1")
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.Code, body)
	}
	if got := resp.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("Content-Type = %q, want HTML", got)
	}
	for _, want := range []string{
		"Session started", "passthrough on llama3 via ollama",
		"Prompt", "what is the time",
		"Assistant", torn,
		"FutureKind", "from tomorrow",
		"The Hub attached",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the first paint does not carry %q", want)
		}
	}

	// The Cursor lags the message that is still arriving, so the stream the page
	// opens replays that message whole rather than resuming past it.
	cursor := attribute(t, body, "data-cursor")
	if want := "desk=" + protocol.Cursor(open-1).String(); cursor != want {
		t.Errorf("data-cursor = %q, want %q", cursor, want)
	}
}

// The Cursor the first paint was drawn at is the one the browser's stream resumes
// on, and an EventSource can only carry it in the query.
func TestTheStreamResumesOnTheCursorTheFirstPaintCarries(t *testing.T) {
	h, open := hostWithATranscript(t)
	srv := httptest.NewServer(h)
	defer srv.Close()

	// The Hub cannot check a Cursor against a log it has never been told the name
	// of, so the first stream is what teaches it. A page loaded before that is
	// answered with a resync and reloads, which is one reload and not a loop.
	merged(t, srv.URL+"/v1/events", "", "event: hello")

	from := "desk=" + protocol.Cursor(open-1).String()
	frames := merged(t, srv.URL+"/v1/events?"+protocol.CursorParam+"="+url.QueryEscape(from), "",
		`"kind":"AssistantMessage"`)
	if strings.Contains(frames, `"kind":"PromptSubmitted"`) {
		t.Errorf("the stream replayed below the Cursor: %s", frames)
	}
}

// The page is three embedded files and nothing is read from disk at runtime.
func TestThePageServesItsOwnStyleAndScript(t *testing.T) {
	h, _ := hostWithATranscript(t)
	for path, want := range map[string]string{"/page.css": "text/css", "/page.js": "text/javascript"} {
		body, resp := get(t, h, path)
		if resp.Code != http.StatusOK || body == "" {
			t.Errorf("%s = %d, %d bytes", path, resp.Code, len(body))
		}
		if got := resp.Header().Get("Content-Type"); !strings.HasPrefix(got, want) {
			t.Errorf("%s Content-Type = %q, want %q", path, got, want)
		}
	}
}

// A Host that did not answer reaches the browser as the status forward would have
// given it, so a Session that is not there and a Host that is not there read the
// same way on the page as they do on the protocol.
func TestAPageForSomethingThatIsNotThereCarriesTheHostsOwnStatus(t *testing.T) {
	h, _ := hostWithATranscript(t)
	for path, want := range map[string]int{
		"/hosts/desk/sessions/nobody": http.StatusNotFound,
		"/hosts/nowhere/sessions/s-1": http.StatusNotFound,
	} {
		if _, resp := get(t, h, path); resp.Code != want {
			t.Errorf("%s = %d, want %d", path, resp.Code, want)
		}
	}
}

// hostWithATranscript is one Host running a real Daemon over a real log, holding
// one Session whose last message is still arriving. It returns the Hub's handler
// and the Sequence Number of that open message.
func hostWithATranscript(t *testing.T) (http.Handler, uint64) {
	t.Helper()
	dir := t.TempDir()
	events, err := eventlog.Open(filepath.Join(dir, "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { events.Close() })

	write := func(kind event.Kind, payload any) event.Event {
		t.Helper()
		e, err := events.Append(event.Event{
			Session: "s-1", At: time.UnixMicro(1).UTC(), Kind: kind, Payload: payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	write(event.KindSessionStarted, &event.SessionStarted{
		Harness: "passthrough", Model: "llama3", Vendor: "ollama", Cwd: dir,
	})
	write(event.KindPromptSubmitted, &event.PromptSubmitted{Text: "what is the time"})
	write("FutureKind", map[string]string{"note": "from tomorrow"})
	write(event.KindHubAttached, &event.NoPayload{})
	open := write(event.KindAssistantMessage, &event.AssistantMessage{})
	if _, err := events.AppendText(open.Seq, torn, false); err != nil {
		t.Fatal(err)
	}

	root, err := workspace.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	d := daemon.New(slog.New(slog.NewTextHandler(io.Discard, nil)), events, root, nil, nil)
	return hub.New([]hostset.Host{{ID: "desk"}}, pipeDialer{
		handlers: map[hostset.HostID]http.Handler{"desk": d.Handler()},
	}).Handler(), open.Seq
}

func get(t *testing.T, h http.Handler, path string) (string, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w.Body.String(), w
}

// attribute reads one HTML attribute out of the page. The page is generated here,
// so finding it by name is enough and a parser would be a dependency for nothing.
func attribute(t *testing.T, page, name string) string {
	t.Helper()
	_, after, ok := strings.Cut(page, name+`="`)
	if !ok {
		t.Fatalf("the page carries no %s", name)
	}
	value, _, _ := strings.Cut(after, `"`)
	return value
}
