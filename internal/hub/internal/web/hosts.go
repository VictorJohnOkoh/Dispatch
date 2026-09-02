package web

import (
	"net/http"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// The Hosts view shows machines. It is read only: starting a Session is the
// wizard's job, and a view that both showed machines and started work on them
// would be two things.
//
// One card per configured Host, and no Host is ever hidden for being
// unreachable. A card that cannot be filled is a card that says so.

// hostsRoute is the Client's own, like the wizard's.
const hostsRoute = "GET /hosts"

// card is one Host as the view draws it.
type card struct {
	Host string

	// State is the Host State. The Hub reports two of the four today, from one read:
	// a Host that answered is Ready and one that did not is Down. The card draws all
	// four, because what each looks like is this view's to decide.
	State string

	// Cause is why a Down Host is down, which is unreachable or no-daemon. The Hub
	// cannot tell the two apart yet, so it is empty and the card leaves the line out
	// rather than guessing.
	Cause string

	// Since is when this card's content was last true, which is when this Host last
	// answered. It is what Stale asks for, and it is stamped on a card whose Host is
	// not answering now.
	Since string

	// Asked is when this Hub put the question, stamped instead on a card for a Host
	// that has never answered. There is no earlier moment to point at, so the card
	// says the one thing it knows.
	Asked string

	// Sessions is this Host's, last known. They keep their state beside a Host that
	// is not answering rather than disappearing, because a Session on a machine
	// nobody can reach is still a Session.
	Sessions []entry
}

// The four Host States, spelled as CONTEXT.md spells them. The view keys its
// drawing rules on these and nothing else.
const (
	stateReady        = "Ready"
	stateConnecting   = "Connecting"
	stateDown         = "Down"
	stateIncompatible = "Incompatible"
)

// hostsView is the page. Cursor is where every answering Host's log stood when
// this page was drawn, so the stream it opens is sent what happens next, and Drawn
// is when that was, so a Host that goes Down while the page is open can be stamped
// with a time the page knows is true.
type hostsView struct {
	Cards  []card
	Cursor string
	Drawn  string

	// Protocol is the version this Hub requires. A Host that failed the Handshake
	// answers with the set it serves, and the card names both, so the page carries
	// the Hub's half rather than letting the browser name a version of its own.
	Protocol int
}

// machinesPage draws every configured Host, in the order the config named them,
// which is the order the user thinks of their machines in.
func (c *client) machinesPage(w http.ResponseWriter, r *http.Request) {
	hosts := c.hosts.All()
	view := hostsView{Cards: make([]card, len(hosts))}
	at := make(map[string]int, len(hosts))
	for i, host := range hosts {
		view.Cards[i] = card{Host: host, State: stateDown}
		at[host] = i
	}

	drawn := protocol.MergedCursor{}
	for _, e := range c.rail(r.Context(), "", "") {
		i, ok := at[e.Host]
		if !ok {
			continue
		}
		if e.Answering {
			view.Cards[i].State = stateReady
			drawn[e.Host] = e.At
		} else if !e.Since.IsZero() {
			view.Cards[i].Since = e.Since.Format(time.RFC3339)
		}
		if e.Session != "" {
			view.Cards[i].Sessions = append(view.Cards[i].Sessions, e)
		}
	}
	// A Host that answered needs no stamp: what is on its card is current. One that
	// did not carries the time its content was true, or the time it was asked when
	// it has never given this Hub any.
	asked := time.Now().UTC().Format(time.RFC3339)
	for i := range view.Cards {
		if view.Cards[i].State == stateReady {
			continue
		}
		if view.Cards[i].Since == "" {
			view.Cards[i].Asked = asked
		}
	}
	view.Cursor = drawn.String()
	view.Drawn = asked
	view.Protocol = protocol.Version

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	machines.Execute(w, view)
}
