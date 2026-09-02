// Package web is the Client: one server-rendered page showing one Session's
// transcript. The template, the CSS and the three JS files are embedded, so the binary is the
// whole deployment and nothing is read from disk at runtime.
//
// The first paint is drawn here rather than in JS. SPEC.md decides it, and it is
// what keeps this the only Hub package that knows Event Kinds exist. The Hub
// still forwards payloads byte for byte, so a Kind this build never heard of still
// reaches the browser and draws as a neutral row.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// Hosts is the one thing this package needs from the Hub: a GET against a named
// Host's Daemon, answered as that Daemon answered it. Closing the answer's body
// releases the connection.
type Hosts interface {
	Get(ctx context.Context, host, path string) (*http.Response, error)
}

//go:embed page.html page.css page.js fold.js render.js
var files embed.FS

var page = template.Must(template.ParseFS(files, "page.html"))

// transcriptPage is how many Events one read of the transcript asks for. It is
// the largest the Daemon serves, and a longer Session is several reads.
const transcriptPage = 1000

// The one page, one Session. The rail, the wizard and the Hosts view are later
// work, so a human names the Session in the URL.
const sessionPage = "GET /hosts/{host}/sessions/{session}"

const indexHint = "Dispatch. Open /hosts/{host}/sessions/{session} to watch a Session.\n"

type client struct{ hosts Hosts }

func New(hosts Hosts) http.Handler {
	c := &client{hosts: hosts}
	mux := http.NewServeMux()
	mux.HandleFunc(sessionPage, c.session)
	mux.HandleFunc("GET /page.css", asset("page.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /page.js", asset("page.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /fold.js", asset("fold.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /render.js", asset("render.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, indexHint)
	})
	return mux
}

// session draws one Session's whole transcript, with the Cursor it was drawn at,
// so the browser opens its stream where this paint stopped and loses nothing that
// fell in between.
func (c *client) session(w http.ResponseWriter, r *http.Request) {
	host, id := r.PathValue("host"), r.PathValue("session")
	events, at, err := c.transcript(r.Context(), host, id)
	if err != nil {
		var refused *refusal
		if errors.As(err, &refused) {
			http.Error(w, refused.body, refused.status)
			return
		}
		http.Error(w, "this Session could not be read: "+err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	state, reason := fold(events)
	if err := page.Execute(w, view{
		Host:    host,
		Session: id,
		Cursor:  protocol.MergedCursor{host: at}.String(),
		State:   state.String(),
		Reason:  string(reason),
		Rows:    rows(events),
	}); err != nil {
		// The header has gone and half a page with it, so there is nothing left to
		// answer with. The operational log is the Daemon's; the Hub's is stderr.
		fmt.Fprintf(w, "<!-- the page could not be finished: %v -->", err)
	}
}

// view is the page. Cursor is the merged spelling, because that is what the
// browser's stream resumes on.
type view struct {
	Host    string
	Session string
	Cursor  string

	// State is the Session's, folded here for the first paint. The browser folds it
	// again from the same Events and keeps folding as they arrive, which is why
	// fold.js exists.
	State  string
	Reason string

	Rows []row
}

// transcript reads one Session whole, oldest first, and the Cursor the last read
// stood at.
func (c *client) transcript(ctx context.Context, host, id string) ([]protocol.Event, protocol.Cursor, error) {
	var all []protocol.Event
	var at protocol.Cursor
	for after := uint64(0); ; {
		resp, err := c.hosts.Get(ctx, host, sessionEvents(id, after))
		if err != nil {
			return nil, 0, err
		}
		body, err := readPage(resp)
		if err != nil {
			return nil, 0, err
		}
		all = append(all, body.Events...)
		at = body.Cursor
		if len(body.Events) < transcriptPage {
			return all, at, nil
		}
		after = body.Events[len(body.Events)-1].Seq
	}
}

// sessionEvents is one read of one Session, spelled from the route table so that
// the Daemon's paths keep one owner.
func sessionEvents(id string, after uint64) string {
	_, path, _ := strings.Cut(protocol.SessionEvents, " ")
	path = strings.Replace(path, "{session}", url.PathEscape(id), 1)
	return fmt.Sprintf("%s?after=%d&limit=%d", path, after, transcriptPage)
}

// eventsBody is the answer to GET /v1/sessions/{session}/events.
type eventsBody struct {
	Events []protocol.Event `json:"events"`
	Cursor protocol.Cursor  `json:"cursor"`
}

func readPage(resp *http.Response) (eventsBody, error) {
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		text, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return eventsBody{}, &refusal{status: resp.StatusCode, body: string(text)}
	}
	var body eventsBody
	err := json.NewDecoder(resp.Body).Decode(&body)
	return body, err
}

// refusal is a Host saying no, carried out to the browser with the Host's own
// status, so a Session that does not exist reads as 404 and not as a Hub failure.
type refusal struct {
	status int
	body   string
}

func (r *refusal) Error() string { return r.body }

func asset(name, contentType string) http.HandlerFunc {
	body, err := files.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Write(body)
	}
}
