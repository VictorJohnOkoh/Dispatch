package hub_test

import (
	"net/http"
	"strings"
	"testing"

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

// A Host is never hidden for being unreachable. Its Sessions keeping their
// last-known state beside it is the half this build owes: the rail reads a Host's
// Sessions live, so a Host that says nothing contributes none, and the Hub has
// nowhere to keep what it last said until it tracks presence.
func TestAHostThatIsNotAnsweringKeepsItsCardAndItsSessions(t *testing.T) {
	body := machines(t)

	attic := section(t, body, "attic")
	if !strings.Contains(attic, `data-host-state="Down"`) {
		t.Errorf("the Host that said nothing is drawn as %q", attic)
	}
	// Down dims and stamps. The stamp is when this Hub asked and got nothing, which
	// is what it can say: it remembers nothing about a Host between reads, so there
	// is no earlier content and no earlier time to put on one.
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

	for _, host := range []string{"desk", "attic"} {
		card := section(t, body, host)
		if !strings.Contains(card, `data-vendors="`+host+`"`) {
			t.Errorf("%s has no Vendor row", host)
		}
		if !strings.Contains(card, "waiting for this Host's Vendors") {
			t.Errorf("%s draws a Vendor row the server made up", host)
		}
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
