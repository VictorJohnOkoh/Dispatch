package event

import "encoding/json"

// SessionStarted opens a Session. The Daemon writes it rather than translating it,
// because the Daemon chose the Harness, the Model and the Vendor.
type SessionStarted struct {
	Harness string `json:"harness"`
	Model   string `json:"model"`
	Vendor  string `json:"vendor"`
	Cwd     string `json:"cwd"`
}

// SessionReady says the launch finished, and carries the Model the Harness reported
// back in the Harness's own spelling. That spelling is kept verbatim so a human can
// see a Harness that selected a Model nobody asked for.
type SessionReady struct {
	Model string `json:"model"`
}

// ApprovalPolicySet is every value the Approval Policy ever holds. The fold reads
// the policy from the last one of these and from nowhere else.
type ApprovalPolicySet struct {
	Policy Policy `json:"policy"`
	SetBy  SetBy  `json:"setBy"`
}

// SetBy is who put the Approval Policy in this state.
type SetBy string

const (
	SetByDefault SetBy = "default" // the Host config's default, clipped by the Gates
	SetByUser    SetBy = "user"
)

type PromptSubmitted struct {
	Text string `json:"text"`
}

// Reasoning is an appendable Event. Text arrives as Deltas and Complete is false
// until the final one is written, so a Session that dies mid-message leaves a torn
// message rather than a finished one.
type Reasoning struct {
	Text     string `json:"text"`
	Complete bool   `json:"complete"`
}

// AssistantMessage is the other appendable Event. See Reasoning.
type AssistantMessage struct {
	Text     string `json:"text"`
	Complete bool   `json:"complete"`
}

// ToolCallRequested is one attempt by a Harness to run one tool. Exactly one
// ToolCallEnded carries the same ToolCallID.
type ToolCallRequested struct {
	ToolCallID string          `json:"toolCallId"`
	Name       string          `json:"name"`
	ToolKind   ToolKind        `json:"toolKind"`
	Title      string          `json:"title"`
	Args       json.RawMessage `json:"args"`
}

// ApprovalRequested is the question. It always carries a ToolCallID, because an
// Adapter whose Harness supplies none attaches the id of the call that is open.
type ApprovalRequested struct {
	ToolCallID string `json:"toolCallId"`
	Title      string `json:"title"`
	Detail     string `json:"detail"`
}

// ApprovalDecided is the answer, and it is written even when no human was involved.
type ApprovalDecided struct {
	ToolCallID string    `json:"toolCallId"`
	Decision   Decision  `json:"decision"`
	By         DecidedBy `json:"by"`
}

type Decision string

const (
	DecisionAllowed Decision = "allowed"
	DecisionRefused Decision = "refused"
)

// DecidedBy is what answered the question.
type DecidedBy string

const (
	ByUser           DecidedBy = "user"
	ByPolicy         DecidedBy = "policy"         // an auto slot answered immediately
	ByDaemonRestart  DecidedBy = "daemon_restart" // the Harness died with the Daemon
	BySessionStopped DecidedBy = "session_stopped"
)

// ToolCallEnded closes a Tool Call. Every Tool Call ends: the Daemon synthesises
// one with OutcomeUnknown for anything still open at PromptCompleted or at
// SessionEnded, whichever comes first.
type ToolCallEnded struct {
	ToolCallID string  `json:"toolCallId"`
	Outcome    Outcome `json:"outcome"`
	Content    string  `json:"content"`
}

type Outcome string

const (
	OutcomeOK      Outcome = "ok"
	OutcomeError   Outcome = "error"
	OutcomeRefused Outcome = "refused" // from the Daemon's own ApprovalDecided, never from the Harness
	OutcomeUnknown Outcome = "unknown" // the Harness went quiet and never reported a result
)

type PromptCompleted struct {
	StopReason StopReason `json:"stopReason"`
	Usage      Usage      `json:"usage"`
}

// StopReason is the Vendor's own word, passed through rather than mapped, because
// Ollama sends values outside the OpenAI set such as "load" and "unload".
type StopReason string

// The two stop reasons this project names itself, for the two ways a Prompt ends
// without the Vendor saying why. Neither invents a fact about the Vendor: both
// name something the Daemon or an Adapter did, and a Prompt that is never bounded
// leaves the Session Working and so refusing every Prompt after it.
const (
	StopError       StopReason = "error"
	StopInterrupted StopReason = "interrupted"
)

// Usage is the token count for one Prompt.
type Usage struct {
	Input     int `json:"input"`
	Output    int `json:"output"`
	CacheRead int `json:"cacheRead"`
	Total     int `json:"total"`
}

// Error is never terminal. A Session that must end writes SessionEnded after it.
type Error struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

type ErrorCode string

const (
	ErrVendor          ErrorCode = "vendor_error"     // the Vendor refused or failed
	ErrStreamTruncated ErrorCode = "stream_truncated" // output stopped mid-message with no terminator
	ErrHarnessFailed   ErrorCode = "harness_failed"   // the Harness process died or gave up
	ErrAdapterFailed   ErrorCode = "adapter_failed"   // the Daemon received something it could not translate
)

// SessionEnded is always terminal and always last.
type SessionEnded struct {
	Reason EndReason `json:"reason"`
}

type EndReason string

const (
	EndStopped EndReason = "stopped"
	EndFailed  EndReason = "failed"
	EndLost    EndReason = "lost" // the Daemon that supervised the Session restarted
)

// NoPayload is the payload of the three Kinds that carry nothing: HubDetached,
// HubAttached and DaemonStarted. Each is the Daemon writing about its own
// observer, or about itself, into every open Session's log.
type NoPayload struct{}
