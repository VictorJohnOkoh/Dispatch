package protocol

import "strings"

// The ten endpoints on the Daemon's leg, written as net/http ServeMux patterns so
// that a handler registers one exactly as it is spelled here. {session} is a
// wildcard the handler reads back by name.
//
// The Client's ten are these under one more path segment; see OnHost.
const (
	StreamEvents  = "GET /v1/events"   // the Host's Event stream. Last-Event-ID resumes it
	ListSessions  = "GET /v1/sessions" // this Host's Sessions, with a Cursor
	ListModels    = "GET /v1/models"   // the Vendor catalogue, which may be shown Stale
	SessionEvents = "GET /v1/sessions/{session}/events"

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

// ListHosts is the eleventh, and it is the one thing only the Hub can answer.
const ListHosts = "GET /v1/hosts"

// Routes is the ten, so a server registers them in one loop.
var Routes = [10]string{
	StreamEvents, ListSessions, ListModels, SessionEvents,
	StartSession, SubmitPrompt, DecideApproval, SetPolicy, Interrupt, StopSession,
}

// hostSegment is where a Host id lands on the Client's leg.
const hostSegment = "/v1/hosts/{host}"

// OnHost is one of Routes as the Client's leg spells it, which names a Host. The
// Hub serves all ten from one handler, and it would serve an eleventh unchanged.
func OnHost(route string) string {
	method, path, _ := strings.Cut(route, " ")
	return method + " " + hostSegment + strings.TrimPrefix(path, "/v1")
}
