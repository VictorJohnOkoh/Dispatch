package hub_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub"
	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// The rail is every Session on every Host, and the Session the page draws in full
// is one of them. These drive it against Hosts that answer the two reads it makes
// and nothing else, because what is under test is the Client and not a Daemon.

// railHost answers a Session list and an empty transcript, which is everything the
// page asks a Host for.
func railHost(sessions ...string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(protocol.ListSessions, func(w http.ResponseWriter, _ *http.Request) {
		rows := make([]any, 0, len(sessions))
		for _, row := range sessions {
			id, state, _ := strings.Cut(row, " ")
			rows = append(rows, map[string]any{
				"id": id, "harness": "opencode", "model": "qwen3.5-9b",
				"cwd": "/home/victor/" + id, "state": state,
			})
		}
		json.NewEncoder(w).Encode(map[string]any{"sessions": rows, "cursor": 0})
	})
	mux.HandleFunc(protocol.SessionEvents, func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"events": []any{}, "cursor": 0})
	})
	return mux
}

// silent is a Host that answers nothing, which is what the Client has to draw
// rather than hide.
func silent() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusBadGateway)
	})
}

func railPage(t *testing.T, hosts map[hostset.HostID]http.Handler, path string) string {
	t.Helper()
	ids := make([]hostset.Host, 0, len(hosts))
	for id := range hosts {
		ids = append(ids, hostset.Host{ID: id})
	}
	h := hub.New(ids, pipeDialer{handlers: hosts}).Handler()
	body, resp := get(t, h, path)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.Code, body)
	}
	return body
}

// Two Hosts, one merged Client. Every Session on both is in the rail, and the one
// in the path is the one drawn in full.
func TestTheRailHoldsEverySessionOnEveryHost(t *testing.T) {
	body := railPage(t, map[hostset.HostID]http.Handler{
		"desk":  railHost("s-1 Working", "s-2 Idle"),
		"attic": railHost("s-9 Asking"),
	}, "/hosts/desk/sessions/s-1")

	for _, want := range []string{"s-1", "s-2", "s-9", "desk", "attic"} {
		if !strings.Contains(body, want) {
			t.Errorf("the rail says nothing about %q", want)
		}
	}
	// The rail links every other Session, so clicking one makes it the primary.
	for _, want := range []string{`href="/hosts/desk/sessions/s-2"`, `href="/hosts/attic/sessions/s-9"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the rail has no way to reach %s", want)
		}
	}
	// And the one being drawn is marked as the one being drawn.
	if !strings.Contains(body, `class="rrow on"`) {
		t.Error("nothing in the rail is marked as the Session on screen")
	}
}

// A row draws the pair. Session State alone cannot label one: Working on a Ready
// Host and Working on a Host that stopped answering mean different things.
func TestASessionRowDrawsTheSessionStateAndTheHostState(t *testing.T) {
	body := railPage(t, map[hostset.HostID]http.Handler{
		"desk":  railHost("s-1 Working"),
		"attic": silent(),
	}, "/hosts/desk/sessions/s-1")

	if !strings.Contains(body, `data-session-state="Working"`) {
		t.Error("the row does not say what the Session is doing")
	}
	if !strings.Contains(body, `data-host-answering="true"`) {
		t.Error("the row does not say what the Host is doing")
	}
	// The Host that answered nothing keeps its place and says so, because a Host is
	// never hidden for being unreachable.
	if !strings.Contains(body, `data-host-answering="false"`) {
		t.Error("the Host that said nothing was left out")
	}
	if !strings.Contains(body, "attic") {
		t.Error("the Host that said nothing was hidden")
	}
}

// The ones waiting on a human come first, and a Host that is not answering sinks
// whatever it last said its Sessions were doing.
func TestTheRailPutsTheSessionsThatWantAHumanFirst(t *testing.T) {
	body := railPage(t, map[hostset.HostID]http.Handler{
		"desk":  railHost("s-idle Idle", "s-asking Asking"),
		"attic": silent(),
	}, "/hosts/desk/sessions/s-idle")

	// Inside the rail alone: the page's own title names the Session it is drawing.
	_, rail, _ := strings.Cut(body, `<nav class="rail">`)
	rail, _, _ = strings.Cut(rail, "</nav>")

	asking, idle := strings.Index(rail, "s-asking"), strings.Index(rail, "s-idle")
	if asking < 0 || idle < 0 || asking > idle {
		t.Errorf("the rail put the Idle Session at %d and the Asking one at %d", idle, asking)
	}
	if quiet := strings.Index(rail, "not answering"); quiet < idle {
		t.Error("a Host that is not answering came before a Host that is")
	}
}

// The Client renders only Events. It asks a Host for its Sessions and for one
// Session's Events, and there is nothing else it could ask for: the raw transcript
// is a file on the Host that no endpoint serves, and the Client has no path to it.
func TestTheClientAsksOnlyForEvents(t *testing.T) {
	var asked []string
	watch := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			asked = append(asked, r.URL.Path)
			next.ServeHTTP(w, r)
		})
	}
	railPage(t, map[hostset.HostID]http.Handler{
		"desk": watch(railHost("s-1 Working")),
	}, "/hosts/desk/sessions/s-1")

	for _, path := range asked {
		if path != "/v1/sessions" && path != "/v1/sessions/s-1/events" {
			t.Errorf("the Client asked for %q, and it draws Events and nothing else", path)
		}
	}
	if len(asked) == 0 {
		t.Error("the Client asked for nothing at all")
	}
}
