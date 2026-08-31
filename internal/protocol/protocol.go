// Package protocol holds the wire: the ten paths, the seven Frame types, the two
// envelopes, the Handshake, the status codes and the Cursor a reader resumes on.
// It is a leaf package and imports nothing else in this project, which is what
// lets both roles speak it.
//
// Client to Hub and Hub to Daemon are the same protocol. The Client's version
// names a Host and the Daemon's does not, and that one difference is the whole of
// it: a path gains a segment, a Frame gains a field, and the Cursor gains an entry
// per Host. Nothing here knows what a Hub is.
//
// ADR 0009 owns it.
package protocol

import "net/http"

// Version is the protocol version this build speaks. It bumps for a change to the
// transport itself: a new Frame type the reader must understand, a changed Cursor
// format, a removed endpoint, or a newly required field on a command. A new Event
// Kind or a new optional field bumps nothing, because the Event model's own
// append-only rules already cover those.
const Version = 1

// VersionHeader carries it on every request. The Hub sends it and a Daemon that
// cannot serve the version answers StatusUpgradeRequired.
const VersionHeader = "Dispatch-Protocol"

// ServedVersions is the set a Daemon can serve, so today the check is an exact
// match. It is a set rather than one number because widening it later costs one
// line, and a Daemon that refuses a Hub it could have served is a Host the user has
// to walk over to.
var ServedVersions = [1]int{Version}

// Hello is the first Frame on every connection and the only one that arrives
// exactly once. The Handshake runs on the stream rather than on an endpoint of its
// own, because the stream is the connection presence is measured on, so anything
// the connection needs to establish belongs on it.
type Hello struct {
	Protocol int `json:"protocol"`

	// LogID is the identity of the log this stream reads from. A reader whose
	// Cursor came from a different one throws that Cursor away. It is omitted while
	// a Daemon has none, so a reader compares nothing rather than comparing "".
	LogID string `json:"logId,omitempty"`

	// Latest is the log's highest Seq, which tells a Cursor that is merely behind
	// from one that is impossible before a byte of replay is sent.
	Latest uint64 `json:"latest"`
}

// The status codes this protocol uses. A command's only successful answer is that
// it was accepted; everything that happens because of it arrives on the stream.
const (
	// StatusStarted answers POST /v1/sessions, the one command with a body in its
	// answer, because the caller needs the Session id to watch and cancel a launch.
	StatusStarted = http.StatusCreated

	// StatusAccepted answers every other command.
	StatusAccepted = http.StatusAccepted

	StatusNoSession = http.StatusNotFound

	// StatusConflict is the current state saying no: change the state and ask
	// again. Admission refused, a second Prompt while Working, a decision on a
	// question that is not open.
	StatusConflict = http.StatusConflict

	// StatusUnprocessable is the request itself being wrong: change the request. A
	// wait or refuse slot with no Gate, a directory outside the Workspace Root, an
	// unknown Model.
	StatusUnprocessable = http.StatusUnprocessableEntity

	// StatusUpgradeRequired is the Handshake failing, and it is the one refusal
	// that never retries.
	StatusUpgradeRequired = http.StatusUpgradeRequired

	// StatusHostNotReady is the Hub's alone: this Host is not Ready.
	StatusHostNotReady = http.StatusServiceUnavailable
)

// Refusal is the body of every refusal, and it is the same shape every time. The
// two optional fields belong to one case each, so a reader that does not know the
// Reason still gets a sentence it can show.
type Refusal struct {
	Reason Reason `json:"reason"`
	Detail string `json:"detail,omitempty"`

	// Blocking is admission's: the Sessions holding the slot. It is what lets the
	// Client offer "stop that one and start this one" as a single action, which is
	// the whole argument against a queue.
	Blocking []string `json:"blocking,omitempty"`

	// Speaks is the Handshake's: the versions this Daemon does serve.
	Speaks []int `json:"speaks,omitempty"`
}

// Reason is why a command was refused. A ticket that adds a refusal adds the word
// for it here, so the set grows with the endpoints rather than ahead of them.
type Reason string

const (
	ReasonAdmission      Reason = "admission"       // one more Session may not start on this Host
	ReasonProtocol       Reason = "protocol"        // the Handshake failed
	ReasonMalformed      Reason = "malformed"       // the request body could not be read
	ReasonUnknownHarness Reason = "unknown_harness" // this Host serves no Harness by that name
	ReasonUnknownModel   Reason = "unknown_model"   // no Vendor on this Host serves that Model
	ReasonWorkspace      Reason = "workspace"       // the directory is outside the Workspace Root
	ReasonUnknownSession Reason = "unknown_session" // this Host has no Session by that id
	ReasonState          Reason = "state"           // the Session's State does not allow this command
	ReasonNoGate         Reason = "no_gate"         // the Harness cannot hold that class of Tool Call
	ReasonNoQuestion     Reason = "no_question"     // no held Tool Call is waiting on that decision
)
