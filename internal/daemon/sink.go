package daemon

import (
	"context"
	"encoding/json"
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
//
// The mutex is the fence between the Adapter and the Session ending. Every report
// goes through it, and so does end, so a report cannot land between the ledger
// reading a Tool Call as open and the ledger writing the end that call is owed.
type sink struct {
	d *Daemon
	s *Session

	mu     sync.Mutex
	open   uint64     // the appendable Event still taking text, or 0
	kind   event.Kind // that Event's Kind
	closed bool       // the Session has ended, so nothing more is reported
}

// end is the ledger's second trigger and the Session's last word from its Adapter.
// Every open Tool Call is ended and nothing the Adapter says afterwards is written.
//
// It is one step under the one mutex. The Session ends on the request that stopped
// it and the Adapter reports on its own reader, so a real end arriving beside this
// either lands before it and is folded out, or lands after it and is dropped.
//
// The open message is left open and torn, which is what a stopped Prompt is. The
// log tears it when SessionEnded lands, and text arriving after that would find no
// open message and cancel a Session that has already ended.
func (k *sink) end() {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.closed {
		return
	}
	k.closed, k.open = true, 0
	k.d.closeCalls(k.s)
}

// report runs one Sink call under the fence. Everything after the Session's end is
// the Harness talking to nobody, so it is dropped rather than written below a
// SessionEnded.
func (k *sink) report(call func()) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.closed {
		call()
	}
}

// Message and Reasoning add text to the open Event of that Kind. The log holds the
// accumulated text and sends the Deltas, so nothing is buffered here.
func (k *sink) Message(text string, end bool) {
	k.report(func() { k.appendable(event.KindAssistantMessage, text, end) })
}

func (k *sink) Reasoning(text string, end bool) {
	k.report(func() { k.appendable(event.KindReasoning, text, end) })
}

// appendable opens an Event of this Kind, or adds to the one that is already open.
// Calling the other Kind closes the open one first, which is the rule that keeps
// one message from swallowing the next. The caller holds the mutex.
func (k *sink) appendable(kind event.Kind, text string, end bool) {
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
	k.report(func() {
		k.d.write(k.s, event.KindToolCallRequested, &event.ToolCallRequested{
			ToolCallID: id, Name: name, ToolKind: kind, Title: title, Args: args,
		})
	})
}

// ToolCallEnded is the Harness reporting a result, which is the one end the ledger
// never has to invent. It goes through the fence so that it and the ledger cannot
// both end the same call, and the ledger drops it for a Tool Call the Daemon
// already refused, because a refusal it decided is not overwritten by what the
// Harness says afterwards.
func (k *sink) ToolCallEnded(id string, o event.Outcome, content string) {
	k.report(func() { k.d.endCall(k.s, id, o, content) })
}

// Completed ends the Prompt, and is the first of the ledger's two triggers. A
// Tool Call the Harness announced and never reported a result for ends unknown
// here, before the boundary the Client reads as the end of the work.
func (k *sink) Completed(stop event.StopReason, u event.Usage) {
	k.report(func() {
		k.d.closeCalls(k.s)
		k.d.write(k.s, event.KindPromptCompleted, &event.PromptCompleted{StopReason: stop, Usage: u})
	})
}

func (k *sink) Failed(code event.ErrorCode, msg string) {
	k.report(func() { k.d.write(k.s, event.KindError, &event.Error{Code: code, Message: msg}) })
}

// Approve is the one call that blocks until the Daemon decides. The Approval
// Policy is what decides it, and a wait slot means the user does.
func (k *sink) Approve(ctx context.Context, id, title, detail string) (event.Decision, error) {
	return k.d.approve(ctx, k.s, id, title, detail)
}
