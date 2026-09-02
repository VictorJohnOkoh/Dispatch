package protocol

import (
	"encoding/json"
	"time"
)

// Frame is one thing sent on an Event stream, named by SSE's event: field. An
// Event is one Frame; a Delta, a Vendor push, a Resync and a keepalive are Frames
// that are not Events. A Frame lives for one connection and an Event lives in the
// log, which is why the Hub may add a Host id to a Frame and never to an Event.
//
// There are seven. Five are the Daemon's, below. The sixth is the Keepalive, which
// has no name because it is an SSE comment. The seventh is FrameHost, and it is
// the only one the Hub originates rather than forwards.
type Frame string

const (
	// FrameHello carries the protocol version, the log's identity and its highest
	// Seq. Exactly one per connection, first, and it never advances the Cursor.
	FrameHello Frame = "hello"

	// FrameEvent carries one Event. It has an id: only when it advances the Cursor,
	// which an open appendable Event does not.
	FrameEvent Frame = "event"

	// FrameDelta carries text for an open appendable Event. It has an id: on the
	// final Delta only, because that is the moment the log holds the whole text.
	FrameDelta Frame = "delta"

	// FrameVendors carries this Host's Vendor reachability and its resident Models.
	// It is a Frame and not an Event because a Vendor's reachability belongs to no
	// Session, and it is pushed rather than fetched because it is worthless when
	// old. The first one on a new stream carries the whole current state rather
	// than a change, so a Client that attached between two changes has something to
	// draw.
	FrameVendors Frame = "vendors"

	// FrameResync says a Cursor is outside the log. It is a Frame rather than an
	// error so the Host stays Ready, and the Client answers it by discarding what
	// it holds for that Host and refetching.
	FrameResync Frame = "resync"

	// FrameHost carries Host State, and the Client's leg is the only one that has
	// it. It cannot be an Event: every Event carries a Session id, and a Host that
	// is down has no Session to carry one.
	FrameHost Frame = "host"
)

// HostState is the Hub's view of one Host, and the only place that view exists.
// It is derived from the liveness of the Hub's Event stream to that Host and it is
// never stored.
type HostState string

const (
	// Connecting is a Host the Hub is reaching for. It absorbs a blink: a Client
	// keeps its content at full strength here, because the connection is usually
	// back before the wait that makes it Down.
	Connecting HostState = "Connecting"

	// Ready is a Host whose Event stream is live. Nothing else makes a Host Ready:
	// there is no health check and no ping endpoint.
	Ready HostState = "Ready"

	// Down is a Host that is not answering, and it always carries a cause.
	Down HostState = "Down"

	// Incompatible is a Host that failed the Handshake. The Hub never retries one.
	Incompatible HostState = "Incompatible"
)

// Cause is why a Host is Down, and the two are different problems for the user:
// one is a machine that is not there and the other is a machine that is.
type Cause string

const (
	Unreachable Cause = "unreachable" // nothing answered at all
	NoDaemon    Cause = "no-daemon"   // the tunnel opened and nothing was behind it
)

// HostStateFrame is the body of a host frame. It is the whole reason the Client
// can draw the pair on a Session row: Session State comes from Events, and this
// comes from nowhere but here.
type HostStateFrame struct {
	Host  string    `json:"host"`
	State HostState `json:"state"`
	Cause Cause     `json:"cause,omitempty"`
}

// StaleAfter is how long the Hub waits on a live connection before it calls the
// Host Down. The Daemon beats every KeepaliveInterval, so this is two beats and a
// half: long enough that one late beat is not a failure, short enough that a
// pulled cable reaches the user while they are still looking at the screen.
const StaleAfter = 25 * time.Second

// OriginatedByHub reports whether the Hub makes this Frame rather than forwarding
// it. Only FrameHost is. Everything else the Hub reads, stamps with a Host and
// writes out without parsing, which is what lets an Event Kind it has never heard
// of still reach the Client.
func (f Frame) OriginatedByHub() bool { return f == FrameHost }

// Keepalive is the sixth Frame, written out whole because it has no event: line to
// name it. It is an SSE comment on purpose: the Hub reads raw SSE and sees it, so
// it gets the liveness signal Host State is derived from, while a browser's
// EventSource discards comments before any handler runs, so the Client is never
// handed a measurement it has no business making.
const Keepalive = ": keepalive\n\n"

// KeepaliveInterval is the beat. The Hub sends its own to the browser on the same
// one, because an idle connection through anything in the middle should not be
// allowed to look alive.
const KeepaliveInterval = 10 * time.Second

// Resync says a Cursor cannot be served, and carries what a reader needs to start
// again: the identity of the log it is actually reading and where that log stands.
type Resync struct {
	LogID  string `json:"logId"`
	Latest uint64 `json:"latest"`
}

// Delta is text for an open appendable Event the log already holds. It is never
// stored, never given a Sequence Number of its own, and never carries information
// its Event will not eventually hold.
type Delta struct {
	// Seq is the Event this text belongs to, not the Delta's own. A Delta has none.
	Seq uint64 `json:"seq"`

	// N is the length of that Event's text before this Delta, so a reader appends
	// at N. The final Delta replaces rather than appends, so a Client that dropped
	// one repairs itself, and there N is simply the whole length.
	N int `json:"n"`

	Text string `json:"text"`

	// Final says the Event now holds its whole text. It is also what carries the
	// id: that lets the Cursor catch up with the message.
	Final bool `json:"final,omitempty"`
}

// Event is one Event on the wire. Payload is never unmarshalled by anything
// between the Daemon that wrote it and the reader that draws it, which is what
// lets the Hub forward a Kind it has never heard of.
//
// This is not the typed envelope the write path and the fold use. The two meet in
// the SQLite row, whose five columns are already this shape, so a replay reads rows
// and writes them out with no JSON parsing at all.
type Event struct {
	Seq     uint64          `json:"seq"`
	Session string          `json:"session"`
	At      int64           `json:"at"` // Unix microseconds
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

// HostFrame is the Client's leg, and its name is the point. The Hub may add a Host
// id to a Frame and may never add one to an Event, so the thing that gains a host
// field is a Frame that carries an Event, not an Event with one more field. It
// flattens on the wire because host sits beside the other five, and no Daemon ever
// constructs one or receives one.
type HostFrame struct {
	Event
	Host string `json:"host"`
}
