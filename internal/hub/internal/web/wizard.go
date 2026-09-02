package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// Starting a Session, in four deliberate steps: Host, then Model, then Harness,
// then Approval Policy. Two selects would fit on one screen and the prototype
// argued for them, and the decision went the other way: the thing being started
// runs for an hour on a machine in another room, and the wizard's shape says so.
//
// Every step is a page of its own with the choices so far in its address, so going
// back is the browser's back button and nothing here has to remember anything.

// steps are the four, in order. The wizard is on the last step it has every answer
// for, so a step that has not been answered is the one drawn.
var steps = []string{"host", "model", "harness", "policy"}

// wizard is one drawing of the four steps.
type wizard struct {
	Step string

	// Chosen is what has been answered so far, and it is what every link and form
	// carries forward.
	Host    string
	Model   string
	Harness string

	Hosts     []hostChoice
	Models    []modelChoice
	Harnesses []harnessChoice
	Slots     []slotChoice

	// Dir is the working directory the user typed, kept so a refusal that is
	// answered with "stop that one" starts the Session the user filled in rather
	// than a different one.
	Dir string

	// Refusal is the Host saying no. It names the Session holding the slot, and the
	// wizard offers to stop that one and start this one. It is never a queue
	// position, because there is no queue.
	Refusal  string
	Blocking []string
}

type hostChoice struct {
	Host string

	// Answering is whether this Host answered just now, and it is deliberately not
	// called Ready: Ready is one of four Host States and comes from the liveness of
	// the Hub's Event stream. A start on a Host that is not answering is disabled
	// rather than failing silently once the user has filled in four steps.
	Answering bool
}

type modelChoice struct {
	ID     string
	Vendor string
	// Capabilities are the Vendor's three-valued answers, drawn as answers. Unknown
	// is one, and a Session on an Unknown Model runs anyway.
	Capabilities []string
}

type harnessChoice struct {
	Name  string
	Tools bool
	Gates []string
}

// slotChoice is one slot of the Approval Policy. Gated says the Harness can hold a
// Tool Call of that kind; a slot that cannot is drawn as auto and cannot be
// changed, which is what the Client owes ADR 0008.
type slotChoice struct {
	Kind   string
	Rule   string
	Gated  bool
	Choose []string
}

// newSession draws the wizard at whichever step the address has reached.
func (c *client) newSession(w http.ResponseWriter, r *http.Request) {
	ask := r.URL.Query()
	view := wizard{
		Host:     ask.Get("host"),
		Model:    ask.Get("model"),
		Harness:  ask.Get("harness"),
		Dir:      ask.Get("dir"),
		Refusal:  ask.Get("refusal"),
		Blocking: ask["blocking"],
	}
	view.Step = stepFor(view)

	switch view.Step {
	case "host":
		view.Hosts = c.hostChoices(r.Context())
	case "model":
		view.Models = c.modelChoices(r.Context(), view.Host)
	case "harness":
		view.Harnesses = c.harnessChoices(r.Context(), view.Host).Harnesses
	case "policy":
		view.Slots = chosenSlots(c.harnessChoices(r.Context(), view.Host), view.Harness, ask)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := start.Execute(w, view); err != nil {
		fmt.Fprintf(w, "<!-- the wizard could not be finished: %v -->", err)
	}
}

// stepFor is the first step with no answer, so a wizard that is sent back to with
// three answers draws the fourth.
func stepFor(v wizard) string {
	answered := map[string]string{"host": v.Host, "model": v.Model, "harness": v.Harness}
	for _, step := range steps {
		if answered[step] == "" {
			return step
		}
	}
	return "policy"
}

func (c *client) hostChoices(ctx context.Context) []hostChoice {
	rail := c.rail(ctx, "", "")
	seen := map[string]bool{}
	var out []hostChoice
	for _, e := range rail {
		if seen[e.Host] {
			continue
		}
		seen[e.Host] = true
		out = append(out, hostChoice{Host: e.Host, Answering: e.Answering})
	}
	return out
}

// modelChoices is one Host's Vendor catalogue. A Model whose capabilities are all
// Unknown is drawn with Unknown beside it rather than with a blank, because
// Unknown is an answer: llama-swap reports it for every Model it has not loaded,
// and every Session runs anyway.
func (c *client) modelChoices(ctx context.Context, host string) []modelChoice {
	var body struct {
		Vendors []struct {
			Kind   string `json:"kind"`
			Models []struct {
				ID   string `json:"id"`
				Caps struct {
					Chat      string `json:"chat"`
					Tools     string `json:"tools"`
					Reasoning string `json:"reasoning"`
					Vision    string `json:"vision"`
				} `json:"caps"`
			} `json:"models"`
		} `json:"vendors"`
	}
	if !c.read(ctx, host, path(protocol.ListModels), &body) {
		return nil
	}

	var out []modelChoice
	for _, v := range body.Vendors {
		for _, m := range v.Models {
			out = append(out, modelChoice{ID: m.ID, Vendor: v.Kind, Capabilities: []string{
				capability("chat", m.Caps.Chat),
				capability("tools", m.Caps.Tools),
				capability("reasoning", m.Caps.Reasoning),
				capability("vision", m.Caps.Vision),
			}})
		}
	}
	return out
}

// capability draws one of a Model's four answers. Unknown is drawn as the answer
// it is rather than as a blank: a Vendor that carries no answer is not a Vendor
// that said no, and a Session on an Unknown Model runs anyway.
func capability(name, answer string) string {
	if answer == "" {
		answer = "unknown"
	}
	return name + " " + answer
}

// harnesses is what one Host answers about what a start may name: the Adapters it
// serves, and the Approval Policy default the Host config carries.
type harnesses struct {
	Harnesses     []harnessChoice `json:"harnesses"`
	PolicyDefault event.Policy    `json:"policyDefault"`
}

func (c *client) harnessChoices(ctx context.Context, host string) harnesses {
	var body harnesses
	if !c.read(ctx, host, path(protocol.ListHarnesses), &body) {
		return harnesses{}
	}
	return body
}

// chosenSlots is the Approval Policy step with whatever the user already chose
// still chosen. A refusal sends the wizard back here, and a step that forgot the
// answers would make the user set them again to say yes to the same question.
func chosenSlots(from harnesses, name string, ask url.Values) []slotChoice {
	out := slots(from, name)
	for i, slot := range out {
		if chose := ask.Get("policy." + slot.Kind); chose != "" && slot.Gated {
			out[i].Rule = chose
		}
	}
	return out
}

// slots is the Approval Policy step for one Harness. A slot with no Gate is auto
// and cannot be changed. A Harness with no tools has no Approval Policy at all, so
// it draws no slots: that is ugly and it is true, which is the correct pairing.
func slots(harnesses harnesses, name string) []slotChoice {
	var chosen harnessChoice
	for _, h := range harnesses.Harnesses {
		if h.Name == name {
			chosen = h
		}
	}
	if !chosen.Tools {
		return nil
	}

	out := make([]slotChoice, 0, event.NumToolKinds)
	for kind := range event.NumToolKinds {
		slot := slotChoice{Kind: event.ToolKind(kind).String(), Rule: string(event.RuleAuto)}
		for _, gated := range chosen.Gates {
			slot.Gated = slot.Gated || gated == slot.Kind
		}
		if slot.Gated {
			slot.Choose = []string{string(event.RuleAuto), string(event.RuleWait), string(event.RuleRefuse)}
			// The Host config's own default, which is the one the user may always
			// override and the Daemon would have used had the start named none.
			if rule := harnesses.PolicyDefault[kind]; rule != "" {
				slot.Rule = string(rule)
			}
		}
		out = append(out, slot)
	}
	return out
}

// read is one JSON answer from a Host, or false when the Host did not give one.
// A Host that says nothing draws an empty step rather than an error page: the
// wizard is where a user goes to find out what a Host has.
func (c *client) read(ctx context.Context, host, at string, into any) bool {
	ctx, done := context.WithTimeout(ctx, hostWait)
	defer done()

	resp, err := c.hosts.Get(ctx, host, at)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	return json.NewDecoder(resp.Body).Decode(into) == nil
}

// path is a route's path, without the method the route table spells it with.
func path(route string) string {
	_, at, _ := strings.Cut(route, " ")
	return at
}

// startSession is the wizard's last step doing what it came to do. A start that is
// refused draws the wizard again with the refusal on it, and one that is accepted
// leaves the browser on the Session it made.
func (c *client) startSession(w http.ResponseWriter, r *http.Request) {
	// A start changes a machine in another room, so it is taken only from this
	// Client's own pages. A browser sends Origin on every cross-site form post, and
	// one that does not match is a page somebody else wrote.
	if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r.Host) {
		http.Error(w, "a start comes from this Client's own page", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "that form could not be read", http.StatusBadRequest)
		return
	}
	host := r.FormValue("host")

	// Stopping first is the refusal's one click. The stop is a command like any
	// other, and the start that follows it is the one the user already filled in.
	//
	// A stop that was itself refused ends here. Carrying on would meet the same
	// refusal again and tell the user nothing about why their click did nothing.
	if blocking := r.FormValue("stopFirst"); blocking != "" {
		if why := c.stop(r.Context(), host, blocking); why != "" {
			http.Redirect(w, r, backTo(r.Form, protocol.Refusal{Detail: why}), http.StatusSeeOther)
			return
		}
	}

	body, err := json.Marshal(map[string]any{
		"harness": r.FormValue("harness"),
		"model":   r.FormValue("model"),
		"dir":     r.FormValue("dir"),
		"policy":  chosenPolicy(r.Form),
	})
	if err != nil {
		http.Error(w, "that form could not be read", http.StatusBadRequest)
		return
	}

	resp, err := c.hosts.Post(r.Context(), host, path(protocol.StartSession), body)
	if err != nil {
		http.Error(w, "this Host could not be reached", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == protocol.StatusStarted {
		var started struct {
			Session string `json:"session"`
		}
		if json.NewDecoder(resp.Body).Decode(&started) == nil && started.Session != "" {
			http.Redirect(w, r, sessionAt(host, started.Session), http.StatusSeeOther)
			return
		}
	}

	// Every other answer is the Host saying no, and the wizard is where the user
	// reads it. The refusal names the Session holding the slot and never a queue
	// position, because there is no queue.
	var refusal protocol.Refusal
	json.NewDecoder(resp.Body).Decode(&refusal)
	http.Redirect(w, r, backTo(r.Form, refusal), http.StatusSeeOther)
}

// stop ends one Session and answers why it could not, or "" when it did. The
// ladder runs before the Daemon answers, so a stop that answered has finished.
func (c *client) stop(ctx context.Context, host, id string) string {
	resp, err := c.hosts.Post(ctx, host, stopPath(id), nil)
	if err != nil {
		return "this Host could not be reached to stop " + id
	}
	defer resp.Body.Close()
	if resp.StatusCode == protocol.StatusAccepted {
		return ""
	}

	var refusal protocol.Refusal
	json.NewDecoder(resp.Body).Decode(&refusal)
	if refusal.Detail != "" {
		return id + " was not stopped: " + refusal.Detail
	}
	return fmt.Sprintf("%s was not stopped, and this Host answered %d", id, resp.StatusCode)
}

// sameOrigin says whether an Origin header names the host this request came in
// on. It compares the host and not the scheme, because a Hub reached over an SSH
// tunnel is http on one side and may be anything on the other.
func sameOrigin(origin, host string) bool {
	at, err := url.Parse(origin)
	return err == nil && at.Host == host
}

// chosenPolicy is the five slots the form carried. A Session whose Harness runs no
// tools has no Approval Policy at all, and the form has no slots for it, so
// nothing is sent and the Daemon's own default stands.
func chosenPolicy(form url.Values) *event.Policy {
	var policy event.Policy
	for kind := range event.NumToolKinds {
		rule := form.Get("policy." + event.ToolKind(kind).String())
		if rule == "" {
			return nil
		}
		policy[kind] = event.Rule(rule)
	}
	return &policy
}

// backTo is the wizard again, with the answers the user gave and the refusal it
// met. The Blocking Sessions ride along, because the offer to stop one and start
// this one is what the wizard draws next.
func backTo(form url.Values, refusal protocol.Refusal) string {
	ask := url.Values{
		"host":    {form.Get("host")},
		"model":   {form.Get("model")},
		"harness": {form.Get("harness")},
		"dir":     {form.Get("dir")},
		"refusal": {refusal.Detail},
	}
	if refusal.Detail == "" {
		ask.Set("refusal", string(refusal.Reason))
	}
	for _, id := range refusal.Blocking {
		ask.Add("blocking", id)
	}
	for kind := range event.NumToolKinds {
		slot := "policy." + event.ToolKind(kind).String()
		if rule := form.Get(slot); rule != "" {
			ask.Set(slot, rule)
		}
	}
	return newRoutePath + "?" + ask.Encode()
}

func sessionAt(host, id string) string {
	return "/hosts/" + url.PathEscape(host) + "/sessions/" + url.PathEscape(id)
}

func stopPath(id string) string {
	return strings.Replace(path(protocol.StopSession), "{session}", url.PathEscape(id), 1)
}
