// Package hub is the multi-Host role. It connects the Client to each configured
// Host without storing Events or Session state.
package hub

import (
	"sync"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
)

type Hub struct {
	hosts  hostset.Table
	dialer hostset.HostDialer
	mu     sync.Mutex
	logIDs map[hostset.HostID]string
}

func New(hosts []hostset.Host, dialer hostset.HostDialer) *Hub {
	return &Hub{hosts: hostset.New(hosts), dialer: dialer, logIDs: make(map[hostset.HostID]string)}
}

func (h *Hub) logID(id hostset.HostID) string { h.mu.Lock(); defer h.mu.Unlock(); return h.logIDs[id] }
func (h *Hub) setLogID(id hostset.HostID, value string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logIDs[id] = value
}
