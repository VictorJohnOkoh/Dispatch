// Package hostset holds the Hub's configured Hosts and the connection seam used
// to reach each Daemon.
package hostset

import (
	"context"
	"net"
)

type HostID string

type Host struct {
	ID HostID `json:"id"`
}

type HostDialer interface {
	Dial(context.Context, HostID) (net.Conn, error)
}

type Table struct {
	hosts []Host
}

func New(hosts []Host) Table {
	return Table{hosts: append([]Host(nil), hosts...)}
}

func (t Table) All() []Host { return append([]Host(nil), t.hosts...) }

func (t Table) Find(id HostID) (Host, bool) {
	for _, host := range t.hosts {
		if host.ID == id {
			return host, true
		}
	}
	return Host{}, false
}
