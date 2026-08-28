# Thirteen Event kinds, a per-Daemon sequence number, and text that streams as Deltas the log never keeps

Every Harness writes its own vocabulary, and the Client renders only Events, so something has to
translate. That translation could have been thin, keeping a `raw` field on every Event so nothing was
ever lost. Instead the adapter translates into a closed set of thirteen kinds and drops the rest, and
the raw bytes go to a file beside the Session rather than into the log. We chose this because an
Event kind is a thing the Client draws, and a field nothing draws is a field nobody maintains.

The second decision is about text. Pi sends 143 `message_update` frames for one short turn and
OpenCode sends one `agent_message_chunk` per word, so storing each one as an Event would make the log
mostly punctuation. Whole messages only would leave the screen still for the length of a generation.
So the log stores one Event per message and the stream carries **Deltas** that are never written
down.

## What the captures bound

Three sets of bytes decide most of what follows.

**Pi** (`captures/pi/`) gives the complete tool lifecycle on one stable `toolCallId`, streaming
partial tool output, per turn `usage`, and the provider and model named in the stream. Reasoning is a
content block inside the assistant message.

**OpenCode 1.18.23** (`captures/opencode/`) gives `tool_call` and `tool_call_update` on one
`toolCallId`, `session/request_permission` for `edit` and `execute`, and a terminal status on all
four tool calls captured. Reasoning arrives as `agent_thought_chunk` under its **own** `messageId`,
different from the `agent_message_chunk` that follows it. The vocabulary is identical against all
three Vendors, frame for frame.

**Hermes 0.19.0** (`captures/hermes-linux/`) emits `tool_call_update` only for `execute`. Across four
Linux runs, nine `read` and `edit` calls never reached a terminal state. Hermes is a test fixture
under ADR 0003 rather than a v1 Harness, and its job here is to be the Harness that goes quiet.

Two traps sit underneath all three. OpenCode moves a tool call to `in_progress` **before** it asks
permission, and Pi fires `tool_execution_start` before its gate resolves. A start is not evidence
that anything ran, on either Harness.

## The envelope

Five fields, the same on every kind.

```go
type Event struct {
    Seq     uint64          // per Daemon, from 1, no gaps
    Session SessionID
    At      time.Time       // the Daemon's clock
    Kind    Kind
    Payload any             // one struct per kind
}
```

**`Seq` is per Daemon, not per Session.** ADR 0004 requires one Event stream per Host carrying every
Session, and `Last-Event-ID` on that stream has to be a single number. One counter serves both jobs:
it orders every Event on the Host, and a Session's own order is that counter restricted to the
Session. A per-Session counter would need a second number for the stream, and two numbers that must
agree are a bug waiting to be written.

`Seq` is unique inside one Daemon and nowhere else. Two Hosts both have a Seq 41. The Hub therefore
tracks a `Last-Event-ID` per Host, and whether the Hub stamps Host identity onto an Event in transit
is [#10](https://github.com/VictorJohnOkoh/Capstone/issues/10)'s call. The Daemon cannot do it,
because a Host's identity is a named profile in the Hub's config and a Daemon never reads that.

No gaps, so a Client detects loss by subtraction rather than by protocol. The Daemon must allocate
`Seq` inside the same transaction that writes the Event, which is a constraint on #10's write path.

`At` comes from the Daemon. Pi stamps epoch milliseconds, OpenCode stamps nothing, and a passthrough
Vendor stamps nothing useful. The Daemon's clock is the only one present on every path, and it is the
clock that put the Events in order.

There is no `harness` field and no `host` field. The Harness is fixed for the life of a Session and
is named once in `SessionStarted`. There is no `v` field either, for the reason in
[Compatibility](#compatibility).

## The thirteen kinds

`written by` matters. Some Events describe the Harness, some describe what the Daemon decided, and
two describe the Daemon's observer.

| kind | written by | payload | appendable |
| --- | --- | --- | --- |
| `SessionStarted` | Daemon | harness, model, vendor, cwd, approvalPolicy | no |
| `PromptSubmitted` | Daemon | text | no |
| `Reasoning` | adapter | text, complete | **yes** |
| `AssistantMessage` | adapter | text, complete | **yes** |
| `ToolCallRequested` | adapter | toolCallId, name, toolKind, title, args | no |
| `ApprovalRequested` | Daemon | toolCallId, title, detail | no |
| `ApprovalDecided` | Daemon | toolCallId, decision, by | no |
| `ToolCallEnded` | adapter or Daemon | toolCallId, outcome, content | no |
| `PromptCompleted` | adapter | stopReason, usage | no |
| `Error` | Daemon | code, message | no |
| `SessionEnded` | Daemon | reason | no |
| `HubDetached` | Daemon | none | no |
| `HubAttached` | Daemon | none | no |

Measured against the set the ticket offered: `SessionStarted`, `AssistantMessage`, `Error` and
`SessionEnded` survive unchanged. `ToolCallPending` becomes `ToolCallRequested` and `ToolResult`
becomes `ToolCallEnded`, both renamed so the pairing is visible. `ToolCallDecided` splits into
`ApprovalRequested` and `ApprovalDecided`, because a question and its answer are two facts and the
gap between them is the whole feature. **`ToolCallStarted` is deleted.** Neither Harness can produce
it honestly, since both announce a start before the gate resolves, so the kind would carry a claim
the bytes do not support.

Four kinds are new. `PromptSubmitted` because a fold without the user's own words replays answers to
questions nobody asked. `Reasoning` for the reason below. `PromptCompleted` because `stopReason` and
`usage` need somewhere to land and every Harness produces both exactly once per prompt.
`HubDetached` and `HubAttached` come from ADR 0004, and they are the first Events with no Harness
origin at all.

`SessionStarted` is written by the **Daemon**, not translated from the Harness. Pi names its provider
and model in the stream and OpenCode does not, but the Daemon chose both, so the field is populated
the same way on every Harness. The Daemon is the authority on what a Session is; the Harness is only
the authority on what happened inside it.

`toolKind` is a closed set: `read`, `edit`, `execute`, `fetch`, `other`. OpenCode and Hermes both
supply `kind` directly. Pi supplies a tool name and no kind, so the adapter maps it. The set is closed
because two rules key on it: the Approval Policy, and the synthesis rule below.

## Events and Deltas

An `AssistantMessage` or a `Reasoning` Event is written to the log the moment the Harness starts
producing it, with empty text and its `Seq` allocated. Text then arrives on the stream as Deltas:

```go
type Delta struct {
    Seq   uint64  // the Event this text belongs to
    N     uint32  // how many Deltas were already applied to that Event
    Text  string
    Final bool
}
```

**A Delta is never stored and never has its own `Seq`.** It is a frame on the Event stream, not an
Event. The rule that makes this safe:

> A Delta never carries information its Event will not eventually hold.

So a Client that receives every Delta and a Client that receives none end up showing the same text.
The first one watches it arrive. Dropping Deltas costs liveliness, never content, which is what lets
them stay out of the log.

The Client applies a Delta only when `N` equals its own count for that Event. **The final Delta
carries the Event's whole text and replaces rather than appends**, so a Client that missed a Delta
repairs itself at the end of the message without asking for anything. Sending the text twice costs a
few kilobytes per message and removes an entire re-request path from the protocol.

`complete` on the stored Event is set when the final Delta is written. If a Session dies mid-message
it stays false, and the Client draws a torn message rather than a finished one. How much of an open
message survives a crash is #10's write path question. The Event model needs only that `complete` is
false unless the final text was stored.

Deltas apply to exactly two kinds. Pi also streams partial **tool** output through
`tool_execution_update`, and that is dropped, under the rule in
[What is Harness specific](#what-is-harness-specific). OpenCode's `tool_call_update` carries status
and no partial output, Hermes has no equivalent, and passthrough has no tools. It is a real loss on
long `bash` commands and it is named as one. If a second Harness gains it, `ToolCallEnded` becomes
appendable and the shape is already here.

## Reasoning has its own kind

Three homes were possible: a field on `AssistantMessage`, its own kind, or the bin.

The bytes settle it. OpenCode gives reasoning a **different `messageId`** from the assistant text
that follows it, so folding the two into one Event means inventing a join the Harness did not make.
Pi does the opposite and puts `thinking` in the same message's content array. Its own kind maps both
without lying: OpenCode's `agent_thought_chunk` run becomes one `Reasoning` Event, and Pi's
`thinking` block becomes one beside its `AssistantMessage`.

Passthrough decides it a second time. All three Vendors emit reasoning and all three name the field
differently, `reasoning` on Ollama and LM Studio and `reasoning_content` on llama.cpp, so a
passthrough Session produces reasoning too. A separate kind means the Vendor adapter maps one field
name to one Event and nothing downstream cares which name it was.

The Client also draws it differently, folded away by default. That is the third argument and the
weakest, but all three point the same way.

## Tool calls, and the Harness that goes quiet

Every `ToolCallRequested` is followed by exactly one `ToolCallEnded` with the same `toolCallId`.
That is an invariant of the model, not a hope about Harnesses.

`outcome` is one of `ok`, `error`, `refused`, `unknown`.

`refused` never comes from the Harness. Hermes reports a human's refusal and an internal failure with
the same string, and Pi reports a refusal as a tool error whose text the extension author chose. So
the Daemon takes `refused` from its own `ApprovalDecided` and treats whatever the Harness says as
corroboration. This is the same rule #9 inherits for the Approval Policy.

`unknown` is the synthesised one. **When the Daemon writes `PromptCompleted`, it first writes
`ToolCallEnded{outcome: unknown}` for every tool call still open in that Session.** One trigger, one
place, no per tool kind special case. Hermes' nine quiet `read` and `edit` calls close there, and a
Client renders "no result reported" instead of a spinner that never stops.

The prompt's completion is the trigger because it is the only honest one. The next `tool_call` is not
evidence the previous one ended, since parallel calls exist. A timer would be the Daemon treating its
own impatience as a measurement, which #9 already rules out for Session health.

The rule was chosen over marking `read` and `edit` fire-and-forget by kind. Fire-and-forget encodes
one Harness's bug into the model's shape, and OpenCode completes both kinds cleanly, so the model
would carry a rule that is wrong for the Harness that ships. Synthesis-on-completion is about the
Harness going quiet, whatever the kind, and it costs nothing when nothing is quiet: in the OpenCode
captures it never fires.

## Approval is a question and an answer

`ApprovalRequested` goes out on the stream. The decision comes back as a POST. `ApprovalDecided` is
then itself an Event. Confirmed, with three additions.

**Both Events are written even when no human is involved.** Under an `auto` policy the Daemon answers
immediately and still writes `ApprovalDecided{allowed, by: policy}`. The fold then has one shape, and
the log shows what ran without being asked about. Two rows is a cheap audit trail.

**The adapter repairs correlation, not the Client.** Pi's approval request carries no `toolCallId`;
the command sits inside a display string with emoji in it. The adapter attaches the id of the tool
call that is open, since Pi asks between its own start and end frames. So `ApprovalRequested` always
carries a `toolCallId` and the Client never learns that one Harness could not supply one.

**A read on OpenCode is never gated, and the log shows that plainly.** No `ApprovalRequested` appears,
because none was asked for. ADR 0003 already priced this; the Event model does not hide it by
inventing an approval that did not happen.

The three awkward cases:

| case | what happens |
| --- | --- |
| The Client disconnects | Nothing. The Daemon holds the question, not the Client. The Event log has an `ApprovalRequested` with no answer, and on reconnect the Client replays it and asks again. |
| The Daemon restarts | The Harness child died with it, so the call can never run. On boot the Daemon writes `ApprovalDecided{refused, by: daemon_restart}`, then `ToolCallEnded{refused}`, then `SessionEnded{lost}`. |
| The user never answers | It waits, with no timeout, forever. |

Waiting forever is deliberate. A timeout that allows is a gate that opens when nobody is watching. A
timeout that refuses throws away work the user may have wanted, on a Session they may have walked
away from for ten minutes. The Session is visibly stuck in the Client, and stopping it is one click,
which produces `ApprovalDecided{refused, by: session_stopped}`.

## Errors and endings

The three cases the Client draws differently are three different shapes, not three wordings of one.

`Error` is never terminal and carries a closed `code`:

| code | when |
| --- | --- |
| `vendor_error` | The Vendor refused or failed. Ollama's raw JSON mid-SSE, LM Studio's bare string under HTTP 200, and llama.cpp's typed body all normalise here. |
| `stream_truncated` | Output stopped mid-message with no terminator. |
| `harness_failed` | The Harness process died, or reported a failure it could not recover from. |
| `adapter_failed` | The Daemon received something it could not translate. |

`SessionEnded` is always terminal, always last, and carries `stopped`, `failed` or `lost`. A Session
in ACP mode does not end by itself, so a Harness that exits unprompted is `failed` and not a clean
finish.

That gives the three cases the ticket names distinct shapes. **The Harness reported an error** is an
`Error` with the Session still running. **The stream died mid-message** is
`Error{stream_truncated}`, then a synthesised `ToolCallEnded` for anything open, then
`SessionEnded{failed}`, with the last `AssistantMessage` left `complete: false`. **A clean end** is
`SessionEnded{stopped}` and no `Error` at all.

The malformed cases are ordinary. Ollama writes error JSON into an SSE body without the `data:`
prefix and never sends `[DONE]`; LM Studio can answer a routing miss with a bare string under HTTP
200. Neither is exceptional to the adapter. Both become `Error{vendor_error}` with the original text
in `message`, and the stream is then closed.

## What is Harness specific

The rule, in one sentence:

> A kind or a field exists when at least two of the three surfaces (Pi, OpenCode, passthrough) produce
> the fact **and** the Client draws it. Everything else is dropped, and the raw bytes are kept in a
> per-Session transcript file next to the log.

Dropping means dropping. There is no `raw` field on an Event and no `HarnessSpecific` kind. A `raw`
field makes the log store every Harness's private format forever, which drags shapes nobody in this
project controls into the compatibility rule below. A `HarnessSpecific` kind leaves the Client
choosing between rendering JSON at the user and ignoring it, and ignoring it is dropping with extra
steps and a bigger database.

What is lost for debugging is not lost. The Daemon already holds the Harness's stdio and writes it to
a rotating transcript beside the Session, which is exactly how the research in this repo was done:
`*-raw.log` beside `*-frames.jsonl`. Raw belongs in a file, not in a typed log.

The rule doing work in both directions:

| native output | verdict |
| --- | --- |
| `usage_update` / `usage` | **Typed.** Pi, OpenCode and Hermes all report per prompt usage, and the Client shows tokens. Lands on `PromptCompleted` as `input`, `output`, `cacheRead`, `total`. |
| `cost` | **Dropped.** Pi and OpenCode both send it, so it clears the first half of the rule and fails the second. It is always zero on a local Vendor, and the Client is not going to draw `$0.00` forever. |
| `available_commands_update` | **Dropped.** ACP Harnesses only, and v1 has no slash command UI. |
| `agent_settled` | **Dropped.** Pi only, and it says what `agent_end` already said. |
| Pi's `agent_end` message array | **Dropped.** It repeats the whole conversation, about 6% of stream bytes and growing with history. The log already holds it once. |
| `tool_execution_update` partial output | **Dropped.** Pi only. Named as a real loss above. |
| Hermes' `usage_update` outside the ACP schema | **Typed anyway.** A Harness extending the protocol it speaks is normal, so the adapter reads unknown native kinds rather than rejecting the frame. |

Promotion is the same rule read forwards. When a second Harness starts producing a dropped fact and
the Client has somewhere to put it, the fact becomes a field. Nothing is deleted from the log to make
that happen, because nothing was ever written.

## Folding a Session

Session state is derivable from its Events, with one exception.

| question | answer from the fold |
| --- | --- |
| Transcript | Every `PromptSubmitted`, `AssistantMessage`, `Reasoning`, `ToolCallRequested` and `ToolCallEnded` in `Seq` order |
| Is it over | A `SessionEnded` exists |
| Is it waiting on me | An `ApprovalRequested` has no `ApprovalDecided` |
| Is it working | A `PromptSubmitted` has no `PromptCompleted` |
| Which Model and Vendor | `SessionStarted` |
| Tokens used | Sum the `PromptCompleted` payloads |
| What did I miss while offline | Between `HubDetached` and `HubAttached` |

The exception is the **Harness operating system process**. A handle to a child process cannot be
folded out of a log, and this is why a Daemon restart makes every live Session `lost` rather than
resumable. That is Session machinery and #9's problem, not Session content.

Keeping the fold honest needs one standing rule:

> Any Daemon decision that changes how a Session behaves is itself an Event.

The Approval Policy is the live test. If #9 lets it change mid-Session, that change is an Event, or
`SessionStarted` alone stops describing the Session and the fold quietly lies.

## Compatibility

Events are persisted, so they outlive the taxonomy that wrote them. The rule is append-only
semantics:

- A kind's meaning never changes. A new meaning is a new kind.
- Fields are only added, and only as optional. Nothing is removed and nothing is retyped.
- A kind may be retired, meaning the Daemon stops writing it. Readers keep handling it forever.
- A reader ignores fields it does not know and draws an unknown kind as a neutral row rather than
  failing.

There is no version number on an Event, which is what the rules above buy. The Handshake in ADR 0004
already refuses a Hub and Daemon that disagree, and the Hub and the Client come from the same binary,
so version skew never exists across a live connection. It exists only across time, inside one
program's own history, which is exactly the case append-only semantics handle.

## Passthrough

A passthrough Session produces `SessionStarted`, `PromptSubmitted`, `Reasoning`,
`AssistantMessage`, `PromptCompleted`, `Error`, `SessionEnded`, and the two Hub Events. Seven of
thirteen, a strict subset, with no kind bent to fit and no field left permanently empty that an agent
Session fills.

The ticket set a fair test: if passthrough feels forced, the abstraction is wrong. It does not. The
one thing that could have forced it was reasoning, since a passthrough Session with reasoning folded
into `AssistantMessage` would have had to either concatenate the two or drop one. Giving reasoning
its own kind removes the problem from both Session types at once.

## Considered options

- **One Event per delta.** Simplest possible model, perfect fold, no second frame type. Rejected: 143
  Events for one short Pi turn, a log that is mostly single words, and a replay that resends a
  generation token by token.
- **Whole messages only.** Smallest log, nothing to invalidate. Rejected: the screen stays still for
  the length of a generation, on local models that are not fast.
- **Appended Events with non-durable Deltas.** Chosen.
- **A `raw` field on every Event.** Nothing is ever lost. Rejected: it stores three Harnesses' private
  formats forever, and the compatibility rule then has to cover shapes this project does not control.
- **A `HarnessSpecific` kind.** Keeps the typed kinds clean while losing nothing. Rejected: the Client
  either shows the user JSON or ignores it, and ignoring it is dropping with a bigger database.
- **Per Session sequence numbers.** The obvious scope, and it makes per Session replay a scan from
  zero. Rejected: ADR 0004's Host level stream needs one `Last-Event-ID`, so this needs a second
  counter, and two counters that must agree will disagree.
- **`read` and `edit` marked fire-and-forget by kind.** Fixes the Hermes hang with no synthesis.
  Rejected: it writes one Harness's defect into the model's shape, and OpenCode completes both kinds.
- **Approval timeout that allows.** Rejected: a gate that opens when nobody is watching.
- **Approval timeout that refuses.** Rejected: it throws away work over a coffee break.

## Consequences

- The Harness adapter in [#7](https://github.com/VictorJohnOkoh/Capstone/issues/7) has a fixed output
  type and three obligations that are not translation: pair every `ToolCallRequested` with a
  `ToolCallEnded`, attach a `toolCallId` to an approval request that arrived without one, and map
  native tool names to the five `toolKind` values.
- [#10](https://github.com/VictorJohnOkoh/Capstone/issues/10) inherits a fixed envelope, a per Daemon
  gapless `Seq` that must be allocated inside the write transaction, a second non-durable frame type
  on the stream beside Events and ADR 0004's keepalive, and two appendable Events whose rows are
  updated after they are first written.
- [#9](https://github.com/VictorJohnOkoh/Capstone/issues/9) inherits the synthesis trigger on
  `PromptCompleted`, an approval that waits forever, the restart sequence that writes
  `ApprovalDecided{daemon_restart}` then `ToolCallEnded` then `SessionEnded{lost}`, and the rule that
  any Daemon decision changing Session behaviour is an Event. Its `daemon_started` follows
  `HubDetached`'s shape: written by the Daemon about itself, into each Session's log.
- [#8](https://github.com/VictorJohnOkoh/Capstone/issues/8) owns mapping three reasoning field names
  onto one `Reasoning` Event, and three error bodies onto `Error{vendor_error}`, including Ollama's
  unframed JSON and LM Studio's bare string under HTTP 200.
- [#11](https://github.com/VictorJohnOkoh/Capstone/issues/11) renders thirteen kinds, applies Deltas
  by `N`, draws `complete: false` as a torn message, draws `ToolCallEnded{unknown}` as "no result
  reported", and never draws a tool call as running before its `ApprovalDecided`.
- The Daemon writes a raw transcript per Session beside the Event log, and that file needs its own
  rotation and retention. It is not covered by #10's log retention.
- Pi's streaming tool output does not reach the Client in v1.
- The Client cannot show cost, on any Harness. Local Vendors report zero, so nothing is lost yet, and
  a remote Vendor would make this the first thing the rule promotes.
- `CONTEXT.md` gains **Delta**, **Sequence Number**, **Tool Call** and **Prompt**, and the thirteen
  kinds are listed under **Event**.

This does not reopen ADR 0004. `HubDetached` and `HubAttached` are kinds here under the names ADR 0004
gave them. It does not reopen ADR 0003 either: the terminal Event rule that ADR named is the synthesis
rule above, now with a trigger.
