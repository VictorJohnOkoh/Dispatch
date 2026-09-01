// Package hostset holds the Hub's configured Hosts and the connection seam used
// to reach each Daemon.
package hostset

import (
	"context"
	"encoding/json"
	"net"
	"sync"

	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

type HostID string

type Host struct {
	ID HostID `json:"id"`
}

type HostDialer interface {
	Dial(context.Context, HostID) (net.Conn, error)
}

type Table struct {
	mu    *sync.Mutex
	hosts []Host
	logs  map[HostID]string
}

func New(hosts []Host) Table {
	return Table{mu: &sync.Mutex{}, hosts: append([]Host(nil), hosts...), logs: make(map[HostID]string)}
}

func (t Table) LogID(id HostID) string { t.mu.Lock(); defer t.mu.Unlock(); return t.logs[id] }
func (t Table) SetLogID(id HostID, value string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.logs[id] = value
}

// ObserveHello keeps the identity of the log a Host is serving and returns the
// Hello, so a caller that needs the same fields parses them once.
func (t Table) ObserveHello(id HostID, data []byte) protocol.Hello {
	var hello protocol.Hello
	if json.Unmarshal(data, &hello) == nil {
		t.SetLogID(id, hello.LogID)
	}
	return hello
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
