package web

import "net/http"

// The landing page is the front door: what a person sees when they type the Hub's
// address and nothing else. It answers the two questions that need no id typed
// anywhere. What is running, and where do I start something new.
//
// It holds neither. Starting a Session is the wizard's and the machines are the
// Hosts view's, and a front door that did either would be a third place to do
// them. It links to both and draws the way in to every Session that exists.
//
// It is drawn once, when it is asked for, and it opens no stream. A page a person
// leaves within seconds does not need to follow one, and the Session page and the
// Hosts view both do.

const indexRoute = "GET /{$}"

// running is one Session on the front page.
type running struct {
	Host    string
	Session string
	Cwd     string
	Harness string
	Model   string
	State   string
}

// indexView is the page. Quiet names the Hosts that did not answer, because a
// Session on a machine nobody can reach is not on this list and the absence has
// to be accounted for.
type indexView struct {
	Running []running
	Quiet   []string
	Hosts   int
}

func (c *client) landing(w http.ResponseWriter, r *http.Request) {
	view := indexView{Hosts: len(c.hosts.All())}
	answering := map[string]bool{}
	for _, e := range c.rail(r.Context(), "", "") {
		if e.Answering {
			answering[e.Host] = true
		}
		if e.Session == "" {
			continue
		}
		view.Running = append(view.Running, running{
			Host: e.Host, Session: e.Session, Cwd: e.Cwd,
			Harness: e.Harness, Model: e.Model, State: e.SessionState,
		})
	}
	for _, host := range c.hosts.All() {
		if !answering[host] {
			view.Quiet = append(view.Quiet, host)
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	index.Execute(w, view)
}
