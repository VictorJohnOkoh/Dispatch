package hub_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub"
	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// Starting a Session from the browser, in four deliberate steps, and the refusal
// when the Host is busy. The Hosts here answer the four reads the wizard makes
// and record the commands it sends.

// startingHost is a Host with one Model, two Harnesses and whatever it says about
// a start. Every command it took is kept, because what the wizard did is the
// thing under test.
type startingHost struct {
	http.Handler
	took *[]string
}

func newStartingHost(t *testing.T, answer func(w http.ResponseWriter)) startingHost {
	t.Helper()
	took := &[]string{}
	mux := http.NewServeMux()
	mux.HandleFunc(protocol.ListSessions, func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"sessions": []any{}, "cursor": 0})
	})
	mux.HandleFunc(protocol.ListModels, func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"vendors": []any{map[string]any{
			"kind": "ollama", "base": "http://127.0.0.1:11434", "at": 1,
			"models": []any{
				map[string]any{"id": "qwen3.5-9b", "caps": map[string]any{"chat": "yes", "tools": "yes"}},
			},
		}}})
	})
	mux.HandleFunc(protocol.ListHarnesses, func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"harnesses": []any{
			map[string]any{"name": "opencode", "tools": true, "gates": []string{"edit", "execute"}},
			map[string]any{"name": "passthrough", "tools": false},
		}})
	})
	mux.HandleFunc(protocol.StartSession, func(w http.ResponseWriter, r *http.Request) {
		body := said(r)
		*took = append(*took, "start "+body)
		answer(w)
	})
	mux.HandleFunc(protocol.StopSession, func(w http.ResponseWriter, r *http.Request) {
		*took = append(*took, "stop "+r.PathValue("session"))
		w.WriteHeader(protocol.StatusAccepted)
	})
	return startingHost{Handler: mux, took: took}
}

func said(r *http.Request) string {
	body, _ := io.ReadAll(r.Body)
	return string(body)
}

func started(w http.ResponseWriter) {
	w.WriteHeader(protocol.StatusStarted)
	json.NewEncoder(w).Encode(map[string]any{"session": "s-new", "seq": 1})
}

func busy(w http.ResponseWriter) {
	w.WriteHeader(protocol.StatusConflict)
	json.NewEncoder(w).Encode(protocol.Refusal{
		Reason:   protocol.ReasonAdmission,
		Detail:   "this Host runs one Session at a time",
		Blocking: []string{"s-busy"},
	})
}

func wizardOn(t *testing.T, host startingHost) http.Handler {
	t.Helper()
	return hub.New([]hostset.Host{{ID: "desk"}}, pipeDialer{
		handlers: map[hostset.HostID]http.Handler{"desk": host},
	}).Handler()
}

func post(t *testing.T, h http.Handler, path, form string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// Four steps, in order, and each one reached by an address, so going back is the
// browser's own button.
func TestTheWizardIsFourStepsAndEachOneHasAnAddress(t *testing.T) {
	h := wizardOn(t, newStartingHost(t, started))

	first, _ := get(t, h, "/new")
	if !strings.Contains(first, `<li class="on">Host</li>`) {
		t.Error("the wizard does not start on the Host step")
	}
	if !strings.Contains(first, `href="/new?host=desk"`) {
		t.Error("the Host step does not lead to the Model step")
	}

	second, _ := get(t, h, "/new?host=desk")
	if !strings.Contains(second, `<li class="on">Model</li>`) || !strings.Contains(second, "qwen3.5-9b") {
		t.Error("the Model step does not list the Host's catalogue")
	}

	third, _ := get(t, h, "/new?host=desk&model=qwen3.5-9b")
	if !strings.Contains(third, `<li class="on">Harness</li>`) {
		t.Error("the Harness step is not third")
	}
	for _, want := range []string{"opencode", "passthrough"} {
		if !strings.Contains(third, want) {
			t.Errorf("the Harness step does not name %q, and the Host config does", want)
		}
	}

	fourth, _ := get(t, h, "/new?host=desk&model=qwen3.5-9b&harness=opencode")
	if !strings.Contains(fourth, `<li class="on">Approval policy</li>`) {
		t.Error("the Approval Policy step is not fourth")
	}
	// Every earlier answer is still on the page, so a step gone back to is the step
	// it was.
	for _, want := range []string{"desk", "qwen3.5-9b", "opencode"} {
		if !strings.Contains(fourth, want) {
			t.Errorf("the last step forgot %q", want)
		}
	}
}

// Unknown is an answer, not a blank. Until the LM Studio and llama-swap Adapters
// land, every Model in the list answers Unknown for most of it.
func TestTheModelStepDrawsUnknownAsAnAnswer(t *testing.T) {
	h := wizardOn(t, newStartingHost(t, started))

	body, _ := get(t, h, "/new?host=desk")
	for _, want := range []string{"chat yes", "tools yes", "reasoning unknown", "vision unknown"} {
		if !strings.Contains(body, want) {
			t.Errorf("the Model step does not say %q", want)
		}
	}
}

// Five slots, and one the Harness cannot gate is fixed at auto. A policy that says
// wait and behaves like auto is the one lie this project cannot afford, and the
// Client is where the user finds that out.
func TestTheApprovalPolicyStepFixesASlotWithNoGate(t *testing.T) {
	h := wizardOn(t, newStartingHost(t, started))

	body, _ := get(t, h, "/new?host=desk&model=qwen3.5-9b&harness=opencode")
	for _, kind := range []string{"read", "edit", "execute", "fetch", "other"} {
		if !strings.Contains(body, `name="policy.`+kind+`"`) {
			t.Errorf("the Approval Policy step has no %s slot", kind)
		}
	}
	// edit and execute are gated, so they are chosen from.
	for _, kind := range []string{"edit", "execute"} {
		if !strings.Contains(body, `<select name="policy.`+kind+`">`) {
			t.Errorf("the %s slot cannot be chosen, and this Harness gates it", kind)
		}
	}
	// read, fetch and other are not, so they are auto and cannot be changed.
	for _, kind := range []string{"read", "fetch", "other"} {
		if !strings.Contains(body, `<input type="hidden" name="policy.`+kind+`" value="auto">`) {
			t.Errorf("the %s slot can be changed, and this Harness has no Gate for it", kind)
		}
	}

	// A Harness with no tools has no Approval Policy at all. That is ugly and it is
	// true, which is the correct pairing.
	none, _ := get(t, h, "/new?host=desk&model=qwen3.5-9b&harness=passthrough")
	if strings.Contains(none, `name="policy.read"`) {
		t.Error("a Harness that runs no tools was given an Approval Policy")
	}
	if !strings.Contains(none, "no Approval Policy at all") {
		t.Error("the wizard does not say why there are no slots")
	}
}

// The busy Host, in full: the 409 is drawn, the blocking Session is named, and one
// click stops it and starts this one.
func TestABusyHostIsRefusedAndOneClickStopsTheSessionHoldingTheSlot(t *testing.T) {
	host := newStartingHost(t, busy)
	h := wizardOn(t, host)

	refused := post(t, h, "/start", "host=desk&model=qwen3.5-9b&harness=opencode&dir=work&policy.read=auto&policy.edit=wait&policy.execute=refuse&policy.fetch=auto&policy.other=auto")
	if refused.Code != http.StatusSeeOther {
		t.Fatalf("the refusal answered %d", refused.Code)
	}
	body, _ := get(t, h, refused.Header().Get("Location"))
	if !strings.Contains(body, "this Host runs one Session at a time") {
		t.Error("the wizard does not say why the start was refused")
	}
	if !strings.Contains(body, "s-busy") {
		t.Error("the refusal does not name the Session holding the slot")
	}
	if !strings.Contains(body, "Stop s-busy and start this one") {
		t.Error("the refusal offers no way out of itself")
	}
	// A queue position is never shown, because there is no queue.
	for _, never := range []string{"position", "queue", "waiting for a slot"} {
		if strings.Contains(strings.ToLower(body), never) {
			t.Errorf("the refusal says %q, and there is no queue", never)
		}
	}

	// The click carries everything the user filled in, so saying yes to the same
	// question does not make them choose it all again.
	for _, want := range []string{
		`name="dir" value="work"`,
		`name="policy.edit" value="wait"`,
		`name="policy.execute" value="refuse"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the refusal dropped %s", want)
		}
	}

	// The one click: stop that Session, then start this one, in that order.
	*host.took = nil
	again := post(t, h, "/start", "host=desk&model=qwen3.5-9b&harness=opencode&dir=work&stopFirst=s-busy&policy.read=auto&policy.edit=wait&policy.execute=refuse&policy.fetch=auto&policy.other=auto")
	if again.Code != http.StatusSeeOther {
		t.Fatalf("the second start answered %d", again.Code)
	}
	if len(*host.took) != 2 || (*host.took)[0] != "stop s-busy" {
		t.Fatalf("the Host was told %v, want the stop and then the start", *host.took)
	}
	if !strings.HasPrefix((*host.took)[1], "start ") {
		t.Fatalf("the Host was told %q second", (*host.took)[1])
	}
	// And it is the Session the user filled in, not a different one.
	for _, want := range []string{`"dir":"work"`, `"execute":"refuse"`} {
		if !strings.Contains((*host.took)[1], want) {
			t.Errorf("the second start asked for %s, and it is missing %s", (*host.took)[1], want)
		}
	}
}

// A start that is accepted leaves the browser on the Session it made.
func TestAStartThatIsAcceptedLandsOnItsSession(t *testing.T) {
	host := newStartingHost(t, started)
	h := wizardOn(t, host)

	w := post(t, h, "/start", "host=desk&model=qwen3.5-9b&harness=opencode&dir=work&policy.read=auto&policy.edit=wait&policy.execute=wait&policy.fetch=auto&policy.other=auto")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("the start answered %d", w.Code)
	}
	if got := w.Header().Get("Location"); got != "/hosts/desk/sessions/s-new" {
		t.Errorf("the browser was sent to %q", got)
	}
	// The Approval Policy the user chose is what was asked for.
	if len(*host.took) != 1 || !strings.Contains((*host.took)[0], `"edit":"wait"`) {
		t.Fatalf("the Host was asked for %v", *host.took)
	}
	if !strings.Contains((*host.took)[0], `"dir":"work"`) {
		t.Error("the working directory was not carried")
	}
}

// A Host that is not answering cannot be started on, and it says so rather than
// failing when the user gets to the end.
func TestAHostThatIsNotAnsweringCannotBeStartedOn(t *testing.T) {
	h := hub.New([]hostset.Host{{ID: "desk"}, {ID: "attic"}}, pipeDialer{
		handlers: map[hostset.HostID]http.Handler{
			"desk":  newStartingHost(t, started),
			"attic": silent(),
		},
	}).Handler()

	body, _ := get(t, h, "/new")
	if !strings.Contains(body, `href="/new?host=desk"`) {
		t.Error("the Host that is answering cannot be chosen")
	}
	if strings.Contains(body, `href="/new?host=attic"`) {
		t.Error("the Host that is not answering can be chosen, and a start there fails at the end")
	}
	if !strings.Contains(body, `<span class="off">attic</span>`) {
		t.Error("the Host that is not answering was hidden rather than disabled")
	}
}

// A stop that is itself refused ends there. Carrying on would meet the same
// refusal again and tell the user nothing about why the click did nothing.
func TestAStopThatIsRefusedIsSaidRatherThanRetried(t *testing.T) {
	took := &[]string{}
	mux := http.NewServeMux()
	mux.HandleFunc(protocol.ListSessions, func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"sessions": []any{}, "cursor": 0})
	})
	mux.HandleFunc(protocol.ListHarnesses, func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"harnesses": []any{
			map[string]any{"name": "opencode", "tools": true, "gates": []string{"edit"}},
		}})
	})
	mux.HandleFunc(protocol.StopSession, func(w http.ResponseWriter, r *http.Request) {
		*took = append(*took, "stop "+r.PathValue("session"))
		w.WriteHeader(protocol.StatusNoSession)
		json.NewEncoder(w).Encode(protocol.Refusal{
			Reason: protocol.ReasonUnknownSession, Detail: "this Host has no Session \"s-gone\"",
		})
	})
	mux.HandleFunc(protocol.StartSession, func(w http.ResponseWriter, _ *http.Request) {
		*took = append(*took, "start")
		started(w)
	})
	h := wizardOn(t, startingHost{Handler: mux, took: took})

	w := post(t, h, "/start", "host=desk&model=m&harness=opencode&stopFirst=s-gone")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("the click answered %d", w.Code)
	}
	if len(*took) != 1 || (*took)[0] != "stop s-gone" {
		t.Fatalf("the Host was told %v, and the stop was refused", *took)
	}
	body, _ := get(t, h, w.Header().Get("Location"))
	if !strings.Contains(body, "was not stopped") {
		t.Error("the wizard does not say the stop was refused")
	}
}

// A step can be gone back to from the page itself, with the answers before it
// kept and the ones after it dropped.
func TestEachAnsweredStepLinksBackToItself(t *testing.T) {
	h := wizardOn(t, newStartingHost(t, started))

	body, _ := get(t, h, "/new?host=desk&model=qwen3.5-9b&harness=opencode")
	for _, want := range []string{
		`<a class="pill" href="/new">desk</a>`,
		`<a class="pill" href="/new?host=desk">qwen3.5-9b</a>`,
		`<a class="pill" href="/new?host=desk&amp;model=qwen3.5-9b">opencode</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the wizard has no way back to %s", want)
		}
	}
}
