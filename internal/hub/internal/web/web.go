// Package web is the Client: one server-rendered page showing one Session's
// transcript. The template, the CSS and the three JS files are embedded, so the
// binary is the whole deployment and nothing is read from disk at runtime.
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
	"sync"

	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// Hosts is what this package needs from the Hub: the Hosts it is configured with,
// and a GET against one of their Daemons answered as that Daemon answered it.
// Closing an answer's body releases the connection.
//
// The Hub is the only component that knows more than one Host exists, and this is
// the whole of what it lends the Client. There is no second way to reach a Host
// from here.
type Hosts interface {
	All() []string
	Get(ctx context.Context, host, path string) (*http.Response, error)

	// Post runs one command against a Host and answers as that Host answered,
	// refusal and all. A command is an intention: what it changed comes back on the
	// Event stream, never in this answer.
	Post(ctx context.Context, host, path string, body []byte) (*http.Response, error)
}

//go:embed page.html start.html hosts.html page.css page.js fold.js render.js hosts.js
var files embed.FS

// pathEscape is the one function the template calls. A Session id or a Host id
// that held a slash would otherwise build a link to somewhere else.
// pathEscape is the one function the template calls, and only in a path. A value
// in a query needs no help: html/template escapes one itself, and escaping it
// first would send %2B to the Host as %252B. A path segment it leaves alone, so a
// Session id holding a slash needs this.
var funcs = template.FuncMap{"pathEscape": url.PathEscape}

var page = template.Must(template.New("page.html").Funcs(funcs).ParseFS(files, "page.html"))

// machines is the Hosts view. It shows machines and starts nothing.
var machines = template.Must(template.New("hosts.html").Funcs(funcs).ParseFS(files, "hosts.html"))

// start is the wizard. It is a page of its own rather than a dialog on the
// Session page, because it is four steps and each one has an address.
var start = template.Must(template.New("start.html").Funcs(funcs).ParseFS(files, "start.html"))

// transcriptPage is how many Events one read of the transcript asks for. It is
// the largest the Daemon serves, and a longer Session is several reads.
const transcriptPage = 1000

// The one page, one Session, with every other Session beside it. The Hosts view
// is later work, so a human names the Session in the URL.
const sessionPage = "GET /hosts/{host}/sessions/{session}"

// railRoute answers the rail on its own, so the browser can redraw it when the
// merged stream says a Session it is not drawing has changed. It is the Client's
// own route and not one of the protocol's.
const railRoute = "GET /rail/{host}/{session}"

// The wizard, and the start it ends in. Both are the Client's own.
const (
	newRoutePath = "/new"
	newRoute     = "GET " + newRoutePath
	startRoute   = "POST /start"
)

const indexHint = "Dispatch. Open /hosts/{host}/sessions/{session} to watch a Session.\n"

type client struct {
	hosts Hosts

	// last is what each Host said the last time it answered, keyed by Host id. It
	// is the only thing the Hub keeps about a Host between reads, and it is what
	// lets a Host that stops answering keep its Sessions on screen.
	mu   sync.Mutex
	last map[string]answer
}

func New(hosts Hosts) http.Handler {
	c := &client{hosts: hosts}
	mux := http.NewServeMux()
	mux.HandleFunc(sessionPage, c.session)
	mux.HandleFunc(railRoute, c.railJSON)
	mux.HandleFunc(hostsRoute, c.machinesPage)
	mux.HandleFunc(newRoute, c.newSession)
	mux.HandleFunc(startRoute, c.startSession)
	mux.HandleFunc("GET /page.css", asset("page.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /page.js", asset("page.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /fold.js", asset("fold.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /render.js", asset("render.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /hosts.js", asset("hosts.js", "text/javascript; charset=utf-8"))
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
	rail := c.rail(r.Context(), host, id)
	state, reason := fold(events)
	if err := page.Execute(w, view{
		Host:      host,
		Session:   id,
		Cursor:    protocol.MergedCursor{host: at}.String(),
		State:     state.String(),
		Reason:    string(reason),
		Answering: answering(rail, host),
		Rail:      rail,
		Rows:      rows(events),
		Events:    payloads(events),
	}); err != nil {
		// The header has gone and half a page with it, so there is nothing left to
		// answer with. The operational log is the Daemon's; the Hub's is stderr.
		fmt.Fprintf(w, "<!-- the page could not be finished: %v -->", err)
	}
}

// railJSON answers the rail as the browser redraws it from. The page is rendered
// on the server and so is this: the Daemon folds a Session's State and the Client
// draws what it is told, so a Session the browser holds no Events for is never
// something it has to fold for itself.
func (c *client) railJSON(w http.ResponseWriter, r *http.Request) {
	rail := c.rail(r.Context(), r.PathValue("host"), r.PathValue("session"))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Rail []entry `json:"rail"`
	}{rail})
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

	// Answering is the Host half of the pair for the Session on screen. See entry.
	Answering bool

	// Rail is every Session on every Host, which is the only place this page says
	// that a second Host exists.
	Rail []entry

	Rows []row

	// Events is those same Events as JSON, for the browser to fold. The rows carry
	// what a person reads and the payloads carry what the fold reads, and a page
	// that shipped only the rows would have to fetch the Session again to know what
	// it was already showing.
	Events template.JS
}

// answering is the rail's view of one Host, so the header and the rail rows agree
// rather than each asking separately and disagreeing by a moment.
func answering(rail []entry, host string) bool {
	for _, e := range rail {
		if e.Host == host {
			return e.Answering
		}
	}
	return false
}

// payloads is the Events as the page carries them, in the shape the stream sends and
// the read path answers with, so the browser applies all three the same way.
//
// It is safe in a script element because encoding/json escapes <, > and & even
// inside a payload it is passing through, so nothing a Harness writes can close
// the tag.
func payloads(events []protocol.Event) template.JS {
	raw, err := json.Marshal(events)
	if err != nil {
		// Nothing here can fail that the page can do anything about, and an empty
		// list is a page that folds from the stream alone.
		return template.JS("[]")
	}
	return template.JS(raw)
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
