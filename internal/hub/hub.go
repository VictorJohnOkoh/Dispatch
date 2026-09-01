// Package hub is the multi-Host role. It connects the Client to each configured
// Host without storing Events or Session state.
package hub

import (
	"net/http"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/web"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

type Host = hostset.Host
type HostID = hostset.HostID
type HostDialer = hostset.HostDialer
type SSHProfile = hostset.SSHProfile

// SSHDialer is the reach ADR 0004 chose: an in-process SSH client opening a
// direct-tcpip channel to each Daemon's loopback port. It is re-exported here
// because hostset is internal to this package and main builds the dialer.
type SSHDialer = hostset.SSHDialer

func NewSSHDialer(hosts []SSHProfile, timeout time.Duration) (*SSHDialer, error) {
	return hostset.NewSSHDialer(hosts, timeout)
}

type Hub struct {
	hosts     hostset.Table
	dialer    hostset.HostDialer
	keepalive time.Duration

	// page is the Client. It reads a Host through the Hub rather than dialling one
	// itself, which is why it is built here and not in main.
	page http.Handler
}

func New(hosts []Host, dialer HostDialer) *Hub {
	h := &Hub{hosts: hostset.New(hosts), dialer: dialer, keepalive: protocol.KeepaliveInterval}
	h.page = web.New(h)
	return h
}
