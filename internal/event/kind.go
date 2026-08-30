package event

// Kind is the type of one Event, from a closed set of sixteen. It is a string on
// the wire and in the log so that a Daemon writing a seventeenth Kind reaches an
// older reader as a name rather than as a number it cannot decode.
type Kind string

// The five Kinds a Harness Adapter writes. It is the authority on what happened
// inside a Session.
const (
	KindReasoning         Kind = "Reasoning"
	KindAssistantMessage  Kind = "AssistantMessage"
	KindToolCallRequested Kind = "ToolCallRequested"
	KindToolCallEnded     Kind = "ToolCallEnded"
	KindPromptCompleted   Kind = "PromptCompleted"
)

// The eleven Kinds the Daemon writes. It is the authority on what a Session is and
// on every decision that changes how the Session behaves.
const (
	KindSessionStarted    Kind = "SessionStarted"
	KindSessionReady      Kind = "SessionReady"
	KindApprovalPolicySet Kind = "ApprovalPolicySet"
	KindPromptSubmitted   Kind = "PromptSubmitted"
	KindApprovalRequested Kind = "ApprovalRequested"
	KindApprovalDecided   Kind = "ApprovalDecided"
	KindError             Kind = "Error"
	KindSessionEnded      Kind = "SessionEnded"
	KindHubDetached       Kind = "HubDetached"
	KindHubAttached       Kind = "HubAttached"
	KindDaemonStarted     Kind = "DaemonStarted"
)

// WrittenByAdapter reports whether a Harness Adapter writes this Kind. Every other
// known Kind is written by the Daemon.
func (k Kind) WrittenByAdapter() bool {
	switch k {
	case KindReasoning, KindAssistantMessage, KindToolCallRequested, KindToolCallEnded, KindPromptCompleted:
		return true
	}
	return false
}

// NewPayload returns an empty payload to decode into, or nil for a Kind this build
// does not know. It is the one place a Kind becomes a type.
func NewPayload(k Kind) any {
	switch k {
	case KindSessionStarted:
		return &SessionStarted{}
	case KindSessionReady:
		return &SessionReady{}
	case KindApprovalPolicySet:
		return &ApprovalPolicySet{}
	case KindPromptSubmitted:
		return &PromptSubmitted{}
	case KindReasoning:
		return &Reasoning{}
	case KindAssistantMessage:
		return &AssistantMessage{}
	case KindToolCallRequested:
		return &ToolCallRequested{}
	case KindApprovalRequested:
		return &ApprovalRequested{}
	case KindApprovalDecided:
		return &ApprovalDecided{}
	case KindToolCallEnded:
		return &ToolCallEnded{}
	case KindPromptCompleted:
		return &PromptCompleted{}
	case KindError:
		return &Error{}
	case KindSessionEnded:
		return &SessionEnded{}
	case KindHubDetached, KindHubAttached, KindDaemonStarted:
		return &NoPayload{}
	}
	return nil
}
