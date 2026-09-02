package daemon

import (
	"context"
	"encoding/json"
	"slices"
	"sync"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
)

// sink is the Daemon as one Session's Adapter sees it. Every call becomes an Event
// in that Session's log, and it is the only path an Adapter has to report
// anything.
//
// Nothing here returns a write error to the Adapter. A write that fails cancels
// the Session's context instead, so the Adapter's reader returns from that rather
// than from a value it might ignore.
type sink struct {
	d *Daemon
	s *Session

	mu   sync.Mutex
	open uint64     // the appendable Event still taking text, or 0
	kind event.Kind // that Event's Kind
}

// Message and Reasoning add text to the open Event of that Kind. The log holds the
// accumulated text and sends the Deltas, so nothing is buffered here.
func (k *sink) Message(text string, end bool)   { k.appendable(event.KindAssistantMessage, text, end) }
func (k *sink) Reasoning(text string, end bool) { k.appendable(event.KindReasoning, text, end) }

// appendable opens an Event of this Kind, or adds to the one that is already open.
// Calling the other Kind closes the open one first, which is the rule that keeps
// one message from swallowing the next.
func (k *sink) appendable(kind event.Kind, text string, end bool) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.open != 0 && k.kind != kind {
		k.closeOpen()
	}
	if k.open == 0 {
		// An end with no text has nothing to open and nothing to close.
		if end && text == "" {
			return
		}
		e, err := k.d.write(k.s, kind, newAppendable(kind, text, end))
		if err != nil || end {
			return
		}
		k.open, k.kind = e.Seq, kind
		return
	}
	if _, err := k.d.events.AppendText(k.open, text, end); err != nil {
		k.d.writeFailed(k.s, k.kind, err)
		return
	}
	if end {
		k.open = 0
	}
}

// closeOpen ends the open Event with no further text. The caller holds the mutex.
func (k *sink) closeOpen() {
	if _, err := k.d.events.AppendText(k.open, "", true); err != nil {
		k.d.writeFailed(k.s, k.kind, err)
	}
	k.open = 0
}

// newAppendable is the payload of the two Kinds whose text arrives over time.
func newAppendable(kind event.Kind, text string, complete bool) any {
	if kind == event.KindReasoning {
		return &event.Reasoning{Text: text, Complete: complete}
	}
	return &event.AssistantMessage{Text: text, Complete: complete}
}

func (k *sink) ToolCallRequested(id, name string, kind event.ToolKind, title string, args json.RawMessage) {
	k.d.write(k.s, event.KindToolCallRequested, &event.ToolCallRequested{
		ToolCallID: id, Name: name, ToolKind: kind, Title: title, Args: args,
	})
}

// ToolCallEnded is what the Harness said happened, and it is dropped for a Tool
// Call that is already closed. The Daemon closes one itself when it refuses it,
// and a refusal it decided is not overwritten by what the Harness reports about
// the call afterwards.
func (k *sink) ToolCallEnded(id string, o event.Outcome, content string) {
	if !slices.Contains(k.d.sessions.openCalls(k.s), id) {
		return
	}
	k.d.write(k.s, event.KindToolCallEnded, &event.ToolCallEnded{
		ToolCallID: id, Outcome: o, Content: content,
	})
}

// Completed ends the Prompt, and is the first of the ledger's two triggers. A
// Tool Call the Harness announced and never reported a result for ends unknown
// here, before the boundary the Client reads as the end of the work.
func (k *sink) Completed(stop event.StopReason, u event.Usage) {
	k.d.closeCalls(k.s, nil)
	k.d.write(k.s, event.KindPromptCompleted, &event.PromptCompleted{StopReason: stop, Usage: u})
}

func (k *sink) Failed(code event.ErrorCode, msg string) {
	k.d.write(k.s, event.KindError, &event.Error{Code: code, Message: msg})
}

// Approve is the one call that blocks until the Daemon decides. The Approval
// Policy is what decides it, and a wait slot means the user does.
func (k *sink) Approve(ctx context.Context, id, title, detail string) (event.Decision, error) {
	return k.d.approve(ctx, k.s, id, title, detail)
}
