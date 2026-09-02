package web

import (
	"net/http"
	"time"
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

	// State is the Host State, and the Hub reports two of its four today: a Host
	// that answered is Ready and one that did not is Down. Connecting and
	// Incompatible arrive with the Hub's own presence tracking, and the card draws
	// all four already, because what they look like is this view's decision and not
	// that one's.
	State string

	// Cause is why a Down Host is down, which is unreachable or no-daemon. The Hub
	// cannot tell the two apart yet, so it is empty and the card leaves the line out
	// rather than guessing.
	Cause string

	// Seen is when this card's content was last true, stamped on a Host that is not
	// answering. Stale is last-known content with the time it was true on it, and a
	// card with no stamp would be a card claiming to be current.
	Seen string

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

// hostsView is the page.
type hostsView struct {
	Cards []card
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

	for _, e := range c.rail(r.Context(), "", "") {
		i, ok := at[e.Host]
		if !ok {
			continue
		}
		if e.Answering {
			view.Cards[i].State = stateReady
		}
		if e.Session != "" {
			view.Cards[i].Sessions = append(view.Cards[i].Sessions, e)
		}
	}
	// The stamp is now, because now is when this page read them. A Host that is not
	// answering carries it and one that is does not, which is what Stale means.
	stamp := time.Now().UTC().Format(time.RFC3339)
	for i := range view.Cards {
		if view.Cards[i].State != stateReady {
			view.Cards[i].Seen = stamp
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := machines.Execute(w, view); err != nil {
		return
	}
}
