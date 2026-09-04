package hub_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub"
	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
)

// The landing page is the front door. It is what a person who typed the Hub's
// address sees, so it has to answer two questions with no id typed anywhere: what
// is running now, and where do I start something new.

func landing(t *testing.T, hosts ...hostset.Host) string {
	t.Helper()
	if len(hosts) == 0 {
		hosts = []hostset.Host{{ID: "desk"}, {ID: "attic"}}
	}
	h := hub.New(hosts, pipeDialer{
		handlers: map[hostset.HostID]http.Handler{
			"desk":  railHost("s-1 Working", "s-2 Idle"),
			"attic": silent(),
		},
	}).Handler()
	body, resp := get(t, h, "/")
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.Code, body)
	}
	return body
}

// A Session is reachable from the front door without knowing its id. Before this
// page the only way in was to type one into the address bar.
func TestTheLandingPageLinksEverySessionThatIsRunning(t *testing.T) {
	body := landing(t)

	for _, want := range []string{
		`href="/hosts/desk/sessions/s-1"`,
		`href="/hosts/desk/sessions/s-2"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("no way in to %s: %s", want, body)
		}
	}
	// The Session's working directory is what a person recognises. An id is what
	// the address needs and not what they remember.
	if !strings.Contains(body, "/home/victor/s-1") {
		t.Errorf("a Session is drawn with nothing a person would recognise: %s", body)
	}
	if !strings.Contains(body, "Working") {
		t.Errorf("the Session States are not on the page: %s", body)
	}
}

// The two doors. Starting a Session is the wizard's, and the machines are the
// Hosts view's, so this page holds neither and links to both.
func TestTheLandingPageOffersTheTwoDoors(t *testing.T) {
	body := landing(t)

	for _, want := range []string{`href="/new"`, `href="/hosts"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the landing page does not link to %s: %s", want, body)
		}
	}
	if strings.Contains(body, `action="/start"`) {
		t.Error("the landing page starts a Session, and that is the wizard's")
	}
}

// A Host that is not answering is named rather than left out, which is the rule
// every view in this Client keeps: nothing is hidden for failing.
func TestTheLandingPageNamesAHostThatIsNotAnswering(t *testing.T) {
	body := landing(t)

	if !strings.Contains(body, "attic") {
		t.Errorf("the Host that did not answer is not on the page: %s", body)
	}
}

// A Hub with nothing running says so and says what to do about it. An empty page
// is the one a person cannot act on.
func TestALandingPageWithNoSessionsPointsAtTheWizard(t *testing.T) {
	h := hub.New([]hostset.Host{{ID: "desk"}}, pipeDialer{
		handlers: map[hostset.HostID]http.Handler{"desk": railHost()},
	}).Handler()
	body, resp := get(t, h, "/")
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.Code, body)
	}

	if !strings.Contains(body, "no Sessions") {
		t.Errorf("a Hub with nothing running does not say so: %s", body)
	}
	if !strings.Contains(body, `href="/new"`) {
		t.Errorf("a Hub with nothing running does not say where to start one: %s", body)
	}
}
