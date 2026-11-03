package daemon

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/admission"
	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/harness"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// The caller holds commanding. Release it during launch so Stop can cancel a load.
func (d *Daemon) reopen(w http.ResponseWriter, r *http.Request) bool {
	id := event.SessionID(r.PathValue("session"))
	if s, _, _ := d.sessions.find(id); s != nil {
		return true
	}
	history, err := d.events.SessionEvents(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return false
	}
	// A Session with no Events is unknown, and allow answers that.
	if len(history) == 0 {
		return true
	}
	var first *event.SessionStarted
	s := &Session{id: id, started: time.Now().UTC()}
	for _, e := range history {
		s.view.Apply(e)
		switch p := e.Payload.(type) {
		case *event.SessionStarted:
			if first == nil {
				first = p
			}
		case *event.SessionReady:
			if p.Reference != "" {
				s.reference = p.Reference
			}
		case *event.PromptSubmitted:
			if first != nil && first.Harness == "passthrough" {
				s.history = append(s.history, harness.Message{Role: "user", Content: p.Text})
			}
		case *event.AssistantMessage:
			if first != nil && first.Harness == "passthrough" {
				s.history = append(s.history, harness.Message{Role: "assistant", Content: p.Text})
			}
		}
	}
	if first == nil {
		http.Error(w, "the Session has no SessionStarted", http.StatusInternalServerError)
		return false
	}
	s.harness, s.model, s.vendor, s.dir = first.Harness, first.Model, first.Vendor, first.Cwd
	h := d.harness(s.harness)
	vendor := d.vendors.at(s.vendor)
	if h != nil {
		s.caps = h.Adapter.Capabilities()
	}
	dir, containmentErr := d.root.Contain(d.root.String(), s.dir)
	if containmentErr == nil {
		s.dir = dir
	}
	d.starting.Lock()
	if no := d.admit.Admit(r.Context(), admission.Request{Harness: s.harness, Model: s.model, Vendor: vendor, Dir: s.dir, Live: d.sessions.live()}); no != nil {
		d.starting.Unlock()
		refuse(w, protocol.StatusConflict, protocol.Refusal{Reason: protocol.ReasonAdmission, Detail: no.Reason, Blocking: blockingNames(no.Blocking)})
		return false
	}
	ctx, cancel := context.WithCancel(d.base)
	s.cancel = cancel
	s.sink = &sink{d: d, s: s}
	if _, err := d.write(s, event.KindSessionStarted, first); err != nil {
		cancel()
		d.starting.Unlock()
		logRefused(w)
		return false
	}
	d.sessions.add(s)
	d.starting.Unlock()
	d.commanding.Unlock()
	var run harness.Run
	switch {
	case containmentErr != nil:
		d.endFailed(s, event.ErrHarnessFailed, containmentErr.Error())
	case h == nil:
		d.endFailed(s, event.ErrHarnessFailed, fmt.Sprintf("this Host serves no Harness named %q", s.harness))
	case vendor == nil:
		d.endFailed(s, event.ErrVendor, "the Session's Vendor is no longer configured")
	case s.harness != "passthrough" && s.reference == "":
		d.endFailed(s, event.ErrHarnessFailed, "the Harness history reference is missing")
	default:
		run = d.launchRun(ctx, s, h, vendor)
	}
	d.commanding.Lock()
	if run != nil {
		d.ready(s, run)
	}
	return true
}
