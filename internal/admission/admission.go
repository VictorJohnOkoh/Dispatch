// Package admission holds the Daemon's decision on whether one more Session may
// start on its Host.
//
// It is asked once, before the Session exists, so a refusal writes no Event and
// produces no Session. It goes to the Daemon's own operational log instead, and to
// the user as a 409 carrying the Refusal.
//
// A refused start is an error naming the Session that holds the slot, never a
// queue position. A queue is a second lifecycle: queued Sessions have to be
// cancellable, ordered, visible and survive a restart, which is a lot of machinery
// for a limit of one where the only person in the queue is the person looking at
// the Session holding the slot.
//
// Admission is per Host. A Daemon knows only its own Host and never learns about
// its peers, and VRAM is a per-Host resource anyway, so a global limit would bound
// the wrong thing.
//
// ADR 0008 owns this.
package admission

import (
	"context"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/vendors"
)

// Policy decides whether one more Session may start on this Host. It is asked
// once, before the Session exists, and never again.
type Policy interface {
	// Admit returns nil to allow. A Refusal reaches the user unchanged.
	Admit(ctx context.Context, req Request) *Refusal
}

// Request is everything a policy may look at.
type Request struct {
	Harness string

	// Model is the Model id, as the Vendor spells it.
	Model string

	// Vendor is this Host's Vendor, on loopback. It is the Adapter rather than a
	// snapshot of what the Vendor holds, because reachability is never stored and a
	// snapshot is stale before the policy reads it. Nothing in v1 reads this field:
	// it is here so a later VRAM policy can call Catalogue and Resident itself, at
	// the moment it decides, without a new field.
	Vendor vendors.Adapter

	// Dir is the working directory, already contained.
	Dir string

	// Live is every Session on this Host that has not ended.
	Live []Live
}

// Live is one Session already running on this Host.
type Live struct {
	Session event.SessionID
	Harness string
	Model   string
	Since   time.Time
}

// Refusal is why a Session may not start.
type Refusal struct {
	Reason string

	// Blocking names the Sessions the user would have to stop, and is empty when
	// stopping something would not help. It is what lets the Client offer "stop that
	// one and start this one" as a single action.
	Blocking []event.SessionID
}

// SingleSession allows one Session at a time on a Host. It is the only policy v1
// ships. There is no configurable count, because a knob nobody sets is complexity
// with a default.
type SingleSession struct{}

func (SingleSession) Admit(_ context.Context, req Request) *Refusal {
	if len(req.Live) == 0 {
		return nil
	}
	blocking := make([]event.SessionID, len(req.Live))
	for i, live := range req.Live {
		blocking[i] = live.Session
	}
	return &Refusal{
		Reason:   "one Session at a time on this Host",
		Blocking: blocking,
	}
}
