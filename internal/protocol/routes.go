package protocol

import "strings"

// The endpoints on the Daemon's leg, written as net/http ServeMux patterns so
// that a handler registers one exactly as it is spelled here. {session} is a
// wildcard the handler reads back by name.
//
// The Client's are these under one more path segment; see OnHost.
const (
	StreamEvents  = "GET /v1/events"   // the Host's Event stream. Last-Event-ID resumes it
	ListSessions  = "GET /v1/sessions" // this Host's Sessions, with a Cursor
	ListModels    = "GET /v1/models"   // the Vendor catalogue, which may be shown Stale
	SessionEvents = "GET /v1/sessions/{session}/events"

	// ListHarnesses is what a start may name, with the Gates each one declares. It
	// is separate from the catalogue rather than folded into it: a Catalogue is
	// large, changes when a human pulls a Model and may be shown Stale, and this
	// list is none of those. It is fixed for the life of the Daemon.
	ListHarnesses = "GET /v1/harnesses"

	StartSession   = "POST /v1/sessions" // Admission runs here
	SubmitPrompt   = "POST /v1/sessions/{session}/prompts"
	DecideApproval = "POST /v1/sessions/{session}/approvals" // decide one held Tool Call
	SetPolicy      = "POST /v1/sessions/{session}/policy"
	Interrupt      = "POST /v1/sessions/{session}/interrupt" // abandon the Prompt, keep the Session

	// StopSession runs the shutdown ladder. It is a command rather than a DELETE
	// because a stopped Session is not deleted: its history stays readable, and a
	// method name that says deleted while the thing stays is a lie.
	StopSession = "POST /v1/sessions/{session}/stop"
)

// ListHosts is the one thing only the Hub can answer, so it is not in Routes and
// the Hub serves it itself.
const ListHosts = "GET /v1/hosts"

// Routes is all of them, so a server registers them in one loop.
var Routes = [11]string{
	StreamEvents, ListSessions, ListModels, SessionEvents, ListHarnesses,
	StartSession, SubmitPrompt, DecideApproval, SetPolicy, Interrupt, StopSession,
}

// hostSegment is where a Host id lands on the Client's leg.
const hostSegment = "/v1/hosts/{host}"

// OnHost is one of Routes as the Client's leg spells it, which names a Host. The
// Hub serves every one from one handler, and it would serve one more unchanged.
func OnHost(route string) string {
	method, path, _ := strings.Cut(route, " ")
	return method + " " + hostSegment + strings.TrimPrefix(path, "/v1")
}
