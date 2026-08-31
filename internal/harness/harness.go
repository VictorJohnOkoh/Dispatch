// Package harness holds the Harness Adapter interface, the types it is given and
// the Sink it reports through, plus the Adapter for each Harness this project
// ships. A Harness is a program that turns a Model into an agent; an Adapter is
// the code that speaks one Harness's protocol and turns what it says into Events.
//
// The Daemon owns the Harness process and the Adapter owns the conversation. So
// nothing here spawns, kills or drains anything: an Adapter reaches the outside
// world only through Spawn, Files and Sink, all three supplied by the caller,
// which is what lets a test supply all three and start no process.
//
// The process supervisor, the ledger of open Tool Calls and the transcript writer
// sit outside this package, in the Daemon.
//
// ADR 0006 owns this.
package harness

import (
	"context"
	"encoding/json"
	"io"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/vendors"
)

// Adapter is everything one Harness contributes. One value per Harness, made once
// at Daemon start and shared by every Session.
type Adapter interface {
	// Capabilities is fixed for the life of the Daemon and is read before a Session
	// starts, so an Approval Policy it cannot honour is refused rather than degraded.
	Capabilities() Capabilities

	// Start brings up one Session and returns once the Harness is running the Model
	// it was given. It returns an error rather than a degraded Session. ctx is the
	// Session's, not the start call's: the Adapter's reader returns when it is
	// cancelled.
	Start(ctx context.Context, spec SessionSpec, out Sink) (Run, error)
}

// Run is one live Session, as the Daemon holds it.
type Run interface {
	// Prompt submits one Prompt. It returns when the Harness has accepted it, not
	// when the Prompt completes. Completion arrives on the Sink.
	Prompt(ctx context.Context, text string) error

	// Interrupt abandons the Prompt in flight and leaves the Session usable.
	Interrupt(ctx context.Context) error

	// Close ends the Session and returns once the Adapter's reader has stopped.
	// Closing stdin and killing the process happen after this, in the Daemon.
	Close() error
}

// Capabilities is what a Harness can do, declared rather than discovered. OpenCode's
// own handshake never mentions gating, so reading this out of the Harness would read
// the one thing it does not say.
type Capabilities struct {
	// Tools is false only for passthrough. A Session with no tools has no Approval
	// Policy at all, which is an absence rather than five empty slots.
	Tools bool

	// Gates says, per ToolKind, whether this Adapter can hold a Tool Call until the
	// Daemon has decided. A slot that is false may only be set to RuleAuto.
	Gates [event.NumToolKinds]bool
}

// SessionSpec is everything the Daemon hands an Adapter. It carries no Approval
// Policy: an Adapter never learns what the user allowed, so it can never answer a
// gate on the Daemon's behalf.
type SessionSpec struct {
	Session event.SessionID
	Model   string           // the Model id, spelled as the Vendor spells it
	Vendor  vendors.Endpoint // base URL and API style, on this Host's loopback
	Dir     string           // the working directory, already inside the Workspace Root

	// Spawn starts the Harness executable that the Host config names. The Daemon
	// owns what comes back: it holds stdin, drains stderr into the Session's
	// transcript, and kills the process group at shutdown. Passthrough never calls it.
	Spawn Spawner

	// Files is contained file access, for a Harness that delegates writes to its
	// client. Paths resolve against Dir and never leave the Workspace Root.
	Files Files
}

// Spawner starts the Harness with arguments the Adapter chooses. The executable
// itself is named by the Host config and never guessed from PATH, because an npm
// shim on the PATH answers a shell and nothing else.
type Spawner func(ctx context.Context, l Launch) (Pipes, error)

// Launch is the Harness-specific half of a spawn. It is what an Adapter knows and
// the Daemon does not: acp on one Harness and --mode rpc on another.
type Launch struct {
	Args []string
	Env  []string // added to the Daemon's own environment
}

// Pipes is the Adapter's whole view of the process.
type Pipes struct {
	// In is the Harness's stdin. It is deliberately not an io.WriteCloser: closing
	// stdin is the first step of shutdown, and shutdown is the Daemon's.
	In io.Writer

	// Out is the Harness's stdout. There is no stderr field. Stderr is evidence for
	// a human and never a signal, so an Adapter is not given it.
	Out io.Reader
}

// Files is the Daemon's contained file access. Only an Adapter whose Harness
// delegates writes ever calls it.
type Files interface {
	WriteTextFile(path, content string) error
}

// Sink is the Daemon, as an Adapter sees it, and the only way an Adapter reports
// anything. Only Approve returns an error. If the Daemon cannot write an Event it
// cancels the Session's context, and the Adapter's reader returns from that instead
// of from a return value it might ignore.
type Sink interface {
	// Message and Reasoning add text to the open Event of that kind. Calling the
	// other one closes the open Event and opens the next, and end closes it with no
	// further text. The Daemon holds the accumulated text, allocates the Seq and
	// sends the Deltas, so an Adapter never buffers a message.
	Message(text string, end bool)
	Reasoning(text string, end bool)

	// ToolCallRequested reports a Tool Call the Harness announced. id must match the
	// Ended that follows, repaired by the Adapter when the Harness supplies no id.
	ToolCallRequested(id, name string, k event.ToolKind, title string, args json.RawMessage)

	// ToolCallEnded reports what the Harness said happened, so only OutcomeOK or
	// OutcomeError. Refused comes from the Daemon's own ApprovalDecided, and unknown
	// is the Daemon's synthesis when a Prompt completes with calls still open.
	ToolCallEnded(id string, o event.Outcome, content string)

	// Completed ends the Prompt. The Daemon closes any Tool Call still open first.
	Completed(stop event.StopReason, u event.Usage)

	// Approve blocks until the Daemon decides, which may be never. The Adapter turns
	// the answer into whatever its Harness accepts and never reads a decision back
	// out of the Harness's own output.
	Approve(ctx context.Context, id, title, detail string) (event.Decision, error)

	// Failed reports something the Adapter could not translate, or a Vendor failure
	// on a passthrough Session. It is not terminal.
	Failed(code event.ErrorCode, msg string)
}
