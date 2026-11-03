package daemon

import (
 "testing"
 "github.com/VictorJohnOkoh/Dispatch/internal/event"
 "github.com/VictorJohnOkoh/Dispatch/internal/harness"
 "github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

func TestClosedSessionReopensOnPrompt(t *testing.T) {
 for _, reason := range []string{"stopped", "lost", "failed"} {
  t.Run(reason, func(t *testing.T) {
   h := newHost(t)
   h.chat.serve(helloStream, false)
   id := h.idle(t)
   h.command(t, id, "prompts", promptBody)
   h.waitState(t, id, "Idle")
   if reason == "lost" {
    d := killed(t, h)
    d.harnesses = []Harness{{Name: "passthrough", Adapter: harness.NewPassthrough(h.chat)}}
    d.vendors.pollAll(t.Context())
    h.Daemon = d
   } else if reason == "failed" {
    s, _, _ := h.sessions.find(id)
    h.endFailed(s, event.ErrHarnessFailed, "test crash")
   } else { h.command(t, id, "stop", "") }
   w := h.post(t, "/v1/sessions/"+string(id)+"/prompts", `{"text":"remember"}`)
   if w.Code != protocol.StatusAccepted { t.Fatalf("reopen: %d: %s", w.Code, w.Body.String()) }
   h.waitState(t, id, "Idle")
   if got := h.list(t); len(got) != 1 { t.Fatalf("reopen duplicated Session: %+v", got) }
   h.command(t, id, "stop", "")
  })
 }
}

func TestAdmissionRefusesReopenWithoutEvents(t *testing.T) {
 h := newHost(t)
 closed := h.idle(t)
 h.command(t, closed, "stop", "")
 live := h.idle(t)
 before := h.events.Cursor()
 w := h.post(t, "/v1/sessions/"+string(closed)+"/prompts", promptBody)
 refusal := h.refusal(t, w, protocol.StatusConflict)
 if refusal.Reason != protocol.ReasonAdmission || len(refusal.Blocking) != 1 || refusal.Blocking[0] != string(live) { t.Fatalf("refusal: %+v", refusal) }
 if h.events.Cursor() != before { t.Fatal("refused reopen wrote Events") }
 h.command(t, live, "stop", "")
}
