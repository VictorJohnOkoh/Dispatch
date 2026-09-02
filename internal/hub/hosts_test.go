package hub_test

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub"
	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
)

// The Hosts view shows machines. These drive it against one Host that answers and
// one that does not, which is the pair every rule on the card is about.

func machines(t *testing.T) string {
	t.Helper()
	h := hub.New([]hostset.Host{{ID: "desk"}, {ID: "attic"}}, pipeDialer{
		handlers: map[hostset.HostID]http.Handler{
			"desk":  railHost("s-1 Working"),
			"attic": silent(),
		},
	}).Handler()
	body, resp := get(t, h, "/hosts")
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.Code, body)
	}
	return body
}

// Every configured Host has a card, and the view starts nothing: that is the
// wizard's, and a view that did both would be two things.
func TestTheHostsViewListsEveryHostAndStartsNothing(t *testing.T) {
	body := machines(t)

	for _, want := range []string{`data-host="desk"`, `data-host="attic"`} {
		if !strings.Contains(body, want) {
			t.Errorf("no card for %s", want)
		}
	}
	// It links to the wizard and does not contain one.
	if !strings.Contains(body, `href="/new"`) {
		t.Error("the view does not say where a Session is started")
	}
	if strings.Contains(body, `action="/start"`) || strings.Contains(body, `name="policy.`) {
		t.Error("the Hosts view starts Sessions, and it is read only")
	}
}

// A Host is never hidden for being unreachable, and one this Hub has never reached
// has no last-known content to keep.
func TestAHostThatIsNotAnsweringKeepsItsCardAndSaysSo(t *testing.T) {
	body := machines(t)

	attic := section(t, body, "attic")
	if !strings.Contains(attic, `data-host-state="Down"`) {
		t.Errorf("the Host that said nothing is drawn as %q", attic)
	}
	// Down dims and stamps. This Host has never answered, so there is no earlier
	// content and no earlier time to put on one, and the stamp says the one thing
	// this Hub knows: when it asked and got nothing.
	if !strings.Contains(attic, "asked at ") {
		t.Error("a Down Host carries no stamp, so the page claims to be current")
	}

	desk := section(t, body, "desk")
	if !strings.Contains(desk, `data-host-state="Ready"`) {
		t.Errorf("the Host that answered is drawn as %q", desk)
	}
	if strings.Contains(desk, "asked at ") {
		t.Error("a Host that is answering is stamped, and its content is current")
	}
	if !strings.Contains(desk, "s-1") || !strings.Contains(desk, `data-session-state="Working"`) {
		t.Errorf("the card does not carry the Host's Sessions: %s", desk)
	}
}

// The Vendor row is never drawn on the server. A Resident list is pushed because
// it is worthless when old, so the row starts empty and the stream fills it.
func TestTheVendorRowWaitsForTheStream(t *testing.T) {
	body := machines(t)

	desk := section(t, body, "desk")
	if !strings.Contains(desk, `data-vendors="desk"`) || !strings.Contains(desk, "waiting for this Host's Vendors") {
		t.Errorf("the answering Host draws a Vendor row the server made up: %s", desk)
	}

	// A Host that is not answering sends no frame, so its row would wait forever.
	// It says what it is instead.
	attic := section(t, body, "attic")
	if !strings.Contains(attic, "not answering, so what it serves is not known") {
		t.Errorf("the Host that said nothing waits forever for a frame it will not send: %s", attic)
	}

	// The page carries the Cursor every answering Host's log stood at, so the
	// stream it opens is sent what happens next rather than the whole log.
	if !strings.Contains(body, `data-cursor="desk=`) {
		t.Error("the page opens its stream from nowhere, and a Host would replay its log into it")
	}

	// The page carries the Cursor every answering Host's log stood at, so the
	// stream it opens is sent what happens next rather than the whole log.
	if !strings.Contains(body, `data-cursor="desk=`) {
		t.Error("the page opens its stream from nowhere, and a Host would replay its log into it")
	}
}

// section is one card, so a test asserts on the Host it means rather than on the
// whole page.
func section(t *testing.T, body, host string) string {
	t.Helper()
	_, after, found := strings.Cut(body, `data-host="`+host+`"`)
	if !found {
		t.Fatalf("no card for %s", host)
	}
	card, _, _ := strings.Cut(after, "</section>")
	return card
}

// CONTEXT.md's Stale: a Host that stops answering keeps its last-known content and
// says when it was true. A Session on a machine nobody can reach is still a
// Session, and dropping it from the card would say it had ended.
func TestAHostThatStopsAnsweringKeepsItsSessionsAndSaysWhenTheyWereTrue(t *testing.T) {
	var answering atomic.Bool
	answering.Store(true)
	live, gone := railHost("s-1 Working"), silent()
	h := hub.New([]hostset.Host{{ID: "desk"}}, pipeDialer{
		handlers: map[hostset.HostID]http.Handler{
			"desk": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if answering.Load() {
					live.ServeHTTP(w, r)
					return
				}
				gone.ServeHTTP(w, r)
			}),
		},
	}).Handler()

	body, _ := get(t, h, "/hosts")
	if !strings.Contains(section(t, body, "desk"), "s-1") {
		t.Fatal("the Host that answered does not carry its Session")
	}
	// Everything true from here on was true no later than this.
	trueUntil := time.Now().UTC()

	answering.Store(false)
	body, _ = get(t, h, "/hosts")
	desk := section(t, body, "desk")

	if !strings.Contains(desk, `data-host-state="Down"`) {
		t.Errorf("the Host that stopped answering is drawn as %q", desk)
	}
	if !strings.Contains(desk, "s-1") || !strings.Contains(desk, `data-session-state="Working"`) {
		t.Errorf("the Session disappeared when its Host went Down: %s", desk)
	}
	// The stamp is the time the content was true, not the time this Hub failed to
	// read it. A stamp made now would say the Session was Working a moment ago.
	stamp := stampOn(t, desk, "last answered at ")
	if stamp.After(trueUntil) {
		t.Errorf("the stamp reads %s, which is after the last read that worked at %s", stamp, trueUntil)
	}
	if strings.Contains(desk, "asked at ") {
		t.Error("the card carries the failed read's time as well, and one of the two is wrong")
	}
}

// stampOn reads a card's stamp as the time it is.
func stampOn(t *testing.T, card, says string) time.Time {
	t.Helper()
	_, after, found := strings.Cut(card, says)
	if !found {
		t.Fatalf("the card carries no %q, so it claims to be current: %s", says, card)
	}
	text, _, _ := strings.Cut(after, "<")
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(text))
	if err != nil {
		t.Fatalf("the stamp reads %q: %v", text, err)
	}
	return at
}

// Stale survives every reload, not only the first one after the Host went quiet.
// The Hub answers a failed read from what the Host last said, and a failed read
// never replaces that, so the stamp does not creep forward either.
func TestReloadingTheHostsViewKeepsTheStaleSessionAndItsStamp(t *testing.T) {
	var answering atomic.Bool
	answering.Store(true)
	live, gone := railHost("s-1 Working"), silent()
	h := hub.New([]hostset.Host{{ID: "desk"}}, pipeDialer{
		handlers: map[hostset.HostID]http.Handler{
			"desk": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if answering.Load() {
					live.ServeHTTP(w, r)
					return
				}
				gone.ServeHTTP(w, r)
			}),
		},
	}).Handler()

	get(t, h, "/hosts")
	answering.Store(false)

	body, _ := get(t, h, "/hosts")
	first := stampOn(t, section(t, body, "desk"), "last answered at ")

	for reload := 2; reload <= 4; reload++ {
		body, _ = get(t, h, "/hosts")
		desk := section(t, body, "desk")
		if !strings.Contains(desk, "s-1") {
			t.Fatalf("reload %d dropped the Stale Session: %s", reload, desk)
		}
		if got := stampOn(t, desk, "last answered at "); !got.Equal(first) {
			t.Errorf("reload %d moved the stamp from %s to %s", reload, first, got)
		}
		if strings.Contains(desk, "asked at ") {
			t.Errorf("reload %d invented an asked-at time beside a real one", reload)
		}
	}
}
