package web

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

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
	// holds them. Neither it nor the Host half alone labels a row, which is the
	// whole reason both are here.
	SessionState string

	// Answering is the Host half of the pair, and it is deliberately not called a
	// Host State. A Host State is one of four and comes from the liveness of the
	// Hub's Event stream; this is one read of one endpoint, just now. The four
	// arrive with the Hub's own presence tracking, and until they do the Client
	// draws the one thing it knows rather than borrowing the word for it.
	Answering bool

	// On marks the Session the page is drawing in full.
	On bool

	// Since is when this Host last answered, set only on an entry drawn from
	// memory. It is the time the entry was true, which is what the Hosts view
	// stamps a card with.
	Since time.Time

	// At is where this Host's log stood when it answered. A page that opens the
	// merged stream from here is sent what happens next rather than every Event
	// this Host has ever written.
	At protocol.Cursor
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
//
// A Host that stops answering keeps its Sessions, drawn as the last read left
// them and stamped with when that read happened, which is CONTEXT.md's Stale.
//
// One thing this owes and does not yet have: a Daemon answers the Session list
// from its registry, which is memory, so a Daemon that restarted lists none of the
// Sessions its log still holds. That waits on the Hub's four Host States.
func (c *client) rail(ctx context.Context, host, id string) []entry {
	hosts := c.hosts.All()
	found := make([][]entry, len(hosts))

	var reading sync.WaitGroup
	for i, from := range hosts {
		reading.Go(func() { found[i] = c.sessionsOn(ctx, from) })
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

// approvals reads only Sessions that say they are Asking. The Session list can
// identify them, but the Events carry the tool call id and title a toast needs.
func (c *client) approvals(ctx context.Context, rail []entry) []protocol.HostFrame {
	found := make([][]protocol.HostFrame, len(rail))

	var reading sync.WaitGroup
	for i, e := range rail {
		if e.On || !e.Answering || e.SessionState != "Asking" {
			continue
		}
		reading.Go(func() {
			ctx, done := context.WithTimeout(ctx, hostWait)
			defer done()
			events, _, err := c.transcript(ctx, e.Host, e.Session)
			if err != nil {
				return
			}
			for _, question := range openApprovals(events) {
				found[i] = append(found[i], protocol.HostFrame{Event: question, Host: e.Host})
			}
		})
	}
	reading.Wait()

	all := make([]protocol.HostFrame, 0)
	for _, some := range found {
		all = append(all, some...)
	}
	return all
}

// openApprovals returns the questions that no later Event ended.
func openApprovals(events []protocol.Event) []protocol.Event {
	open := make(map[string]protocol.Event)
	var order []string
	for _, e := range events {
		var payload struct {
			ToolCallID string `json:"toolCallId"`
		}
		switch e.Kind {
		case string(event.KindApprovalRequested):
			if json.Unmarshal(e.Payload, &payload) != nil || payload.ToolCallID == "" {
				continue
			}
			if _, exists := open[payload.ToolCallID]; !exists {
				order = append(order, payload.ToolCallID)
			}
			open[payload.ToolCallID] = e
		case string(event.KindApprovalDecided), string(event.KindToolCallEnded):
			if json.Unmarshal(e.Payload, &payload) == nil {
				delete(open, payload.ToolCallID)
			}
		case string(event.KindSessionEnded):
			return nil
		}
	}

	questions := make([]protocol.Event, 0, len(open))
	for _, id := range order {
		if question, exists := open[id]; exists {
			questions = append(questions, question)
		}
	}
	return questions
}

// sessionsOn reads one Host's Sessions. A Host that refused, or that could not be
// reached at all, answers with one entry saying so, because the rail's job is to
// show every machine the user has and not only the ones that are working.
func (c *client) sessionsOn(ctx context.Context, host string) []entry {
	// Each Host is asked on its own clock. The page is drawn from all of them, so a
	// Host that accepts a connection and then says nothing would otherwise hold the
	// whole Client open, and the Session on screen belongs to one of the others.
	ctx, done := context.WithTimeout(ctx, hostWait)
	defer done()

	resp, err := c.hosts.Get(ctx, host, sessionList)
	if err != nil {
		return c.recall(host)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.recall(host)
	}

	var body struct {
		Sessions []sessionRow    `json:"sessions"`
		Cursor   protocol.Cursor `json:"cursor"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return c.recall(host)
	}

	out := make([]entry, 0, len(body.Sessions))
	for _, s := range body.Sessions {
		state := s.State.String()
		if s.EndReason != "" {
			state += " " + string(s.EndReason)
		}
		out = append(out, entry{
			Host: host, Session: s.ID, Cwd: s.Cwd, Harness: s.Harness, Model: s.Model,
			SessionState: state, Answering: true, At: body.Cursor,
		})
	}
	// A Host that answered with nothing is a Host with no Sessions, and it keeps
	// its row. A Host is never hidden, and neither is an empty one.
	if len(out) == 0 {
		out = []entry{{Host: host, Answering: true, At: body.Cursor}}
	}
	c.remember(host, out)
	return out
}

// remember keeps one Host's answer, so the next read that fails has something to
// draw. It is the whole of what the Hub holds about a Host between reads.
func (c *client) remember(host string, said []entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last == nil {
		c.last = map[string]answer{}
	}
	c.last[host] = answer{sessions: said, at: time.Now().UTC()}
}

// recall is what a Host said the last time it answered, marked as not answering
// and stamped with when it was true. A Session on a machine nobody can reach is
// still a Session, and dropping it would tell the user it ended.
//
// A Host that has never answered has nothing to recall, and it gets the empty row
// it got before, with no stamp of its own: the card says when it was asked
// instead, because there is no earlier moment to point at.
func (c *client) recall(host string) []entry {
	c.mu.Lock()
	defer c.mu.Unlock()

	was, known := c.last[host]
	if !known {
		return []entry{{Host: host}}
	}
	out := make([]entry, len(was.sessions))
	for i, e := range was.sessions {
		e.Answering = false
		e.At = 0
		e.Since = was.at
		out[i] = e
	}
	return out
}

// answer is one Host's last read, and the moment it happened. Both halves are what
// Stale asks for: the content, and the time it was true.
type answer struct {
	sessions []entry
	at       time.Time
}

// hostWait is how long the rail waits for one Host. It is the only timer in the
// Client, and it ends a wait rather than diagnosing one: a Host that misses it is
// drawn as a Host that is not answering just now, which is what it is.
const hostWait = 5 * time.Second

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
