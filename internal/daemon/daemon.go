// Package daemon is the Host role. One Daemon owns one Host's Sessions, its Event
// log, its Harness processes and its Vendor poll. It knows nothing about any other
// Host and has no field that could hold one.
//
// Configuration never reaches here. main.go reads daemon.json and hands this
// package plain values, which is ADR 0010's first fence.
//
// ADR 0010 owns the package tree and ADR 0011 owns the role split.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/admission"
	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/eventlog"
	"github.com/VictorJohnOkoh/Dispatch/internal/harness"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
	"github.com/VictorJohnOkoh/Dispatch/internal/vendors"
	"github.com/VictorJohnOkoh/Dispatch/internal/workspace"
)

// shutdownGrace is how long a Serve that is stopping waits for the requests it is
// already answering.
const shutdownGrace = 5 * time.Second

// Harness is one Harness this Host serves, under the name daemon.json gave it.
// That name is what a Session start asks for.
type Harness struct {
	Name string

	// Exe is the program the Daemon spawns for this Harness, as the Host config
	// wrote it. It is empty for a Harness that spawns no process, which is
	// passthrough alone.
	Exe string

	Adapter harness.Adapter
}

// Daemon is one Host's server.
type Daemon struct {
	log       *slog.Logger
	events    *eventlog.Log
	root      workspace.Root
	vendors   *Vendors
	harnesses []Harness

	// admit is the seam ADR 0008 named, and SingleSession is the only policy v1
	// ships. It is built here rather than passed in, because no configuration
	// chooses it.
	admit admission.Policy

	// keepalive is the beat every Event stream writes its comment on. It is a field
	// only so a test may shorten it.
	keepalive time.Duration

	// stopWait is the ladder's fixed short wait before the kill. It is a field only
	// so a test may shorten it, and no configuration names it.
	stopWait time.Duration

	sessions sessions

	// starting makes admission and the registry entry it decided on one step.
	starting sync.Mutex

	// commanding makes a command's fold and the Event that changes it one step, so
	// two Prompts arriving together cannot both read an Idle Session.
	commanding sync.Mutex

	// writing keeps a Session's recorded Events in the order the log gave them
	// Sequence Numbers, which is the order the fold reads them in.
	writing sync.Mutex

	// base is the context every Session hangs off, which is Serve's. A handler
	// exercised without Serve gets the background one this starts as.
	base context.Context
}

// New builds the Daemon from the values main.go resolved. The Vendor adapters and
// the Harnesses are each one per entry the config named, in that order.
func New(log *slog.Logger, events *eventlog.Log, root workspace.Root, adapters []vendors.Adapter, harnesses []Harness) *Daemon {
	return &Daemon{
		log:       log,
		events:    events,
		root:      root,
		vendors:   newVendors(adapters, log),
		harnesses: harnesses,
		admit:     admission.SingleSession{},
		keepalive: protocol.KeepaliveInterval,
		stopWait:  stopWait,
		base:      context.Background(),
	}
}

// harness is the Harness this Host serves under that name, or nil. It is a scan
// over three entries at most, which is cheaper to read than an index.
func (d *Daemon) harness(name string) *Harness {
	for i, h := range d.harnesses {
		if h.Name == name {
			return &d.harnesses[i]
		}
	}
	return nil
}

// write appends one Event for a Session and records it against that Session. A
// write that fails is the one thing the Event log cannot report, so it goes to the
// operational log and cancels the Session.
func (d *Daemon) write(s *Session, kind event.Kind, payload any) (event.Event, error) {
	d.writing.Lock()
	defer d.writing.Unlock()

	e, err := d.events.Append(event.Event{Session: s.id, At: time.Now().UTC(), Kind: kind, Payload: payload})
	if err != nil {
		d.writeFailed(s, kind, err)
		return event.Event{}, err
	}
	d.sessions.record(s, e)
	return e, nil
}

// writeFailed is the Event log refusing. The Session cannot be trusted to describe
// itself after this, so it is cancelled.
func (d *Daemon) writeFailed(s *Session, kind event.Kind, err error) {
	d.log.Error("the Event log refused a write", "session", s.id, "kind", kind, "err", err)
	s.cancel()
}

// Serve binds listen, starts the Vendor poll and answers until ctx is done.
func (d *Daemon) Serve(ctx context.Context, listen string) error {
	if err := loopbackOnly(listen); err != nil {
		return err
	}
	// The boot sweep runs before anything can be answered, so a reconnecting Hub
	// never reads a Session that is Working in the log and dead in reality.
	if err := d.sweep(); err != nil {
		return err
	}

	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("daemon: %w", err)
	}
	d.log.Info("daemon listening", "addr", ln.Addr().String())

	// Every Session hangs off this one, so a Daemon that stops cancels the launches
	// and the Runs it is holding.
	ctx, cancel := context.WithCancel(ctx)
	d.base = ctx
	polled := make(chan struct{})
	go func() { defer close(polled); d.vendors.Run(ctx) }()
	defer func() { cancel(); <-polled }()

	// The listener is bound, but nothing is answered until the first beat has been
	// taken: a Session start arriving immediately would otherwise meet an empty
	// Catalogue and be told the Model does not exist.
	select {
	case <-d.vendors.Polled():
	case <-ctx.Done():
	}

	srv := &http.Server{Handler: d.handler()}
	stop := context.AfterFunc(ctx, func() {
		grace, done := context.WithTimeout(context.Background(), shutdownGrace)
		defer done()
		if err := srv.Shutdown(grace); err != nil {
			d.log.Warn("daemon shut down on requests still open", "err", err)
		}
	})
	defer stop()

	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("daemon: %w", err)
	}
	d.log.Info("daemon stopped")
	return nil
}

// loopbackOnly refuses an address the Daemon must never bind. The Hub reaches this
// listener through an SSH tunnel and nothing else reaches it at all, so an address
// off the loopback interface is a mistake worth finding at start. A host name is
// refused with the rest, because a name can resolve anywhere.
func loopbackOnly(listen string) error {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("daemon: listen %q is not host:port", listen)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("daemon: listen %q is not a loopback address, and the Daemon binds nothing else", listen)
	}
	return nil
}
