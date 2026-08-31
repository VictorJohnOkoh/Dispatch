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
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/vendors"
)

// shutdownGrace is how long a Serve that is stopping waits for the requests it is
// already answering.
const shutdownGrace = 5 * time.Second

// Daemon is one Host's server.
type Daemon struct {
	vendors *Vendors
	log     *slog.Logger
}

// New builds the Daemon from the values main.go resolved. The adapters are one per
// Vendor the config named, in that order.
func New(adapters []vendors.Adapter, log *slog.Logger) *Daemon {
	return &Daemon{vendors: newVendors(adapters, log), log: log}
}

// Serve binds listen, starts the Vendor poll and answers until ctx is done.
func (d *Daemon) Serve(ctx context.Context, listen string) error {
	if err := loopbackOnly(listen); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("daemon: %w", err)
	}
	d.log.Info("daemon listening", "addr", ln.Addr().String())

	ctx, cancel := context.WithCancel(ctx)
	polled := make(chan struct{})
	go func() { defer close(polled); d.vendors.Run(ctx) }()
	defer func() { cancel(); <-polled }()

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
