package web

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"github.com/VictorJohnOkoh/Dispatch/internal/session"
)

// The rail is every Session on every Host, and the primary object is the one
// Session drawn beside it in full. The competing answer, one list of every Session
// on every Host, reads as a thing to administer rather than a thing to work in.
//
// Every entry draws the pair: Session State beside Host State. Working on a Ready
// Host and Working on a Host that stopped answering ten minutes ago look identical
// and mean different things, and no Event carries the difference, because it is
// Host State and that lives only in the Hub.

// entry is one Session in the rail.
type entry struct {
	Host    string
	Session string
	Cwd     string
	Harness string
	Model   string

	// SessionState is the Session's own, folded from its Events by the Daemon that
	// holds them. HostState is this Hub's view of the machine it is on. Neither one
	// alone labels a row, which is the whole reason both are here.
	SessionState string
	HostState    string

	// Answering is the Host State this build has. The four states arrive with the
	// Hub's own presence tracking; until then the Client knows one thing about a
	// Host, which is whether it just answered.
	Answering bool

	// On marks the Session the page is drawing in full.
	On bool
}

// sessionRow is one row of a Daemon's GET /v1/sessions, read for the rail.
type sessionRow struct {
	ID        string          `json:"id"`
	Harness   string          `json:"harness"`
	Model     string          `json:"model"`
	Cwd       string          `json:"cwd"`
	State     session.State   `json:"state"`
	EndReason event.EndReason `json:"endReason"`
}

// rail asks every Host for its Sessions at once and returns them in the order the
// user needs them: the ones waiting on a human first, and a Host that is not
// answering last.
//
// A Host that does not answer keeps its place. A Host is never hidden for being
// unreachable, and one that answers nothing is drawn as a Host with no Sessions
// rather than as a Host that is not there.
func (c *client) rail(ctx context.Context, host, id string) []entry {
	hosts := c.hosts.All()
	found := make([][]entry, len(hosts))

	var reading sync.WaitGroup
	for i, from := range hosts {
		reading.Add(1)
		go func() {
			defer reading.Done()
			found[i] = c.sessionsOn(ctx, from)
		}()
	}
	reading.Wait()

	var all []entry
	for _, some := range found {
		all = append(all, some...)
	}
	for i := range all {
		all[i].On = all[i].Host == host && all[i].Session == id
	}
	slices.SortStableFunc(all, byUrgency)
	return all
}

// sessionsOn reads one Host's Sessions. A Host that refused, or that could not be
// reached at all, answers with one entry saying so, because the rail's job is to
// show every machine the user has and not only the ones that are working.
func (c *client) sessionsOn(ctx context.Context, host string) []entry {
	resp, err := c.hosts.Get(ctx, host, sessionList)
	if err != nil {
		return []entry{{Host: host, HostState: "not answering"}}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return []entry{{Host: host, HostState: "not answering"}}
	}

	var body struct {
		Sessions []sessionRow `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return []entry{{Host: host, HostState: "not answering"}}
	}

	out := make([]entry, 0, len(body.Sessions))
	for _, s := range body.Sessions {
		state := s.State.String()
		if s.EndReason != "" {
			state += " " + string(s.EndReason)
		}
		out = append(out, entry{
			Host: host, Session: s.ID, Cwd: s.Cwd, Harness: s.Harness, Model: s.Model,
			SessionState: state, HostState: "ready", Answering: true,
		})
	}
	if len(out) == 0 {
		return []entry{{Host: host, HostState: "ready", Answering: true}}
	}
	return out
}

// sessionList is the Daemon's Session list, spelled from the route table so the
// Daemon's paths keep one owner.
var sessionList = func() string {
	_, path, _ := strings.Cut(protocol.ListSessions, " ")
	return path
}()

// byUrgency puts the Sessions that want a human first. A Host that is not
// answering sinks whatever its Sessions were doing, because what it says about
// them is old.
func byUrgency(a, b entry) int {
	if a.Answering != b.Answering {
		if a.Answering {
			return -1
		}
		return 1
	}
	return urgency(a.SessionState) - urgency(b.SessionState)
}

// order is the Session States, most wanting a human first. A row whose state is
// none of them sorts after all five, which is what a Host that said nothing gets.
var order = []string{"Asking", "Working", "Starting", "Idle", "Ended"}

func urgency(state string) int {
	if i := slices.IndexFunc(order, func(name string) bool { return strings.HasPrefix(state, name) }); i >= 0 {
		return i
	}
	return len(order)
}
