// Package hub is the multi-Host role. It connects the Client to each configured
// Host without storing Events or Session state.
package hub

import (
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

type Host = hostset.Host
type HostID = hostset.HostID
type HostDialer = hostset.HostDialer
type SSHHost = hostset.SSHHost

// SSHDialer is the reach ADR 0004 chose: an in-process SSH client opening a
// direct-tcpip channel to each Daemon's loopback port. It is re-exported here
// because hostset is internal to this package and main builds the dialer.
type SSHDialer = hostset.SSHDialer

func NewSSHDialer(hosts []SSHHost, timeout time.Duration) (*SSHDialer, error) {
	return hostset.NewSSHDialer(hosts, timeout)
}

type Hub struct {
	hosts     hostset.Table
	dialer    hostset.HostDialer
	keepalive time.Duration
}

func New(hosts []Host, dialer HostDialer) *Hub {
	return &Hub{hosts: hostset.New(hosts), dialer: dialer, keepalive: protocol.KeepaliveInterval}
}
