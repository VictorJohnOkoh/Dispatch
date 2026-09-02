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

	// Asked is when this Hub last put the question to this Host, stamped on a card
	// whose Host did not answer it.
	//
	// It is deliberately not the time the content was true, which is what Stale
	// asks for. The Hub remembers nothing about a Host between reads, so a Down
	// Host has no last-known content to keep and no earlier time to stamp on it.
	// What this card can say honestly is when it asked and got nothing, and the
	// Hub's own presence tracking is what will let it say the other.
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
// this page was drawn, so the stream it opens is sent what happens next.
type hostsView struct {
	Cards  []card
	Cursor string
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
		}
		if e.Session != "" {
			view.Cards[i].Sessions = append(view.Cards[i].Sessions, e)
		}
	}
	// A Host that answered needs no stamp: what is on its card is current. One that
	// did not carries the time it was asked.
	asked := time.Now().UTC().Format(time.RFC3339)
	for i := range view.Cards {
		if view.Cards[i].State != stateReady {
			view.Cards[i].Asked = asked
		}
	}
	view.Cursor = drawn.String()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	machines.Execute(w, view)
}
