# A Session outlives its Harness process, and a Closed Session reopens on its next Prompt

A Daemon restart is routine. The user takes the Daemon down to update it, or shuts the Host down for
maintenance. Under ADR 0008 each of those ends every live Session `lost`, and the user starts again
with an empty conversation. That is the third item of #117. This ADR makes a Session the
conversation and not the process. A Session now has one or more **Runs**, and each Run is one
Harness process. When a Session has no Run, it is `Closed`. The user's next Prompt starts a new Run,
and the Harness loads its own history of the conversation.

## Why ADR 0008's answer changes

ADR 0008 rejected this for v1 on two premises. The repo's own research shows that both are false.

**"The Harness holds the message history in its own process memory."** Pi writes every session to a
JSONL file, and Dispatch does not pass `--no-session`. The Pi Adapter already sends `get_state` at
launch (`internal/harness/pi.go:176`), and every captured answer carries `sessionFile` and
`sessionId`. OpenCode stores its sessions on disk as well.

**"OpenCode has `session/resume` and Pi has nothing."** Pi has `--session <path|id>`, and its RPC mode
has `switch_session` (`docs/research/harness-control-surfaces.md`, Pi's session section). OpenCode's
`initialize` advertises `loadSession: true` and `sessionCapabilities.resume`.

So both Harnesses can load their own history, and the objection that only one Harness could do it
is gone. ADR 0008 also named the one field this needs: "the Harness's native session id lands on
`SessionReady` beside the Model".

## The decision

**A Session is one conversation, and a Run is one Harness process that serves it.** A Session has at
most one Run at a time. "One Session is one Harness process" becomes "one Run is one Harness
process", and every rule ADR 0006 and ADR 0008 set for the process now applies to the Run. The
Daemon spawns it, supervises it, kills it, and never restarts it on its own.

**`Ended` becomes `Closed`, and `Closed` is not terminal.** There are still five states. `Closed`
carries `stopped`, `failed` or `lost` as before. A new Run moves the Session from `Closed` to
`Starting`. The Event Kind keeps the name `SessionEnded`, because the log never deletes an Event and
a rename would break every log already on disk. It now means that the Run ended.

**The user's next Prompt reopens a Closed Session.** The Daemon never reopens a Session on its own,
not at boot and not after a crash. A Prompt to a Closed Session does this:

1. Admission runs. Admission now counts live Runs and not Sessions. A refusal writes no Event and
   names the Session that holds the slot, as before.
2. `SessionStarted`, with the same Harness, Model and working directory as the first Run.
3. The Harness starts and loads its own history. Then `SessionReady`.
4. `PromptSubmitted`, and the Prompt continues as usual.

Any `Closed` Session can reopen, whatever its reason. A Session that the user stopped can reopen too.

**`SessionReady` carries the Harness's own session reference.** For Pi this is `sessionFile` from
`get_state`, and the next Run launches with `--session <file>`. For OpenCode it is the `sessionId`
from `session/new`, and the next Run loads that session instead of calling `session/new`.
Passthrough has no Harness history. For passthrough, the Daemon gives the new Run the text of every
`PromptSubmitted` and `AssistantMessage` from the log. That is the whole of a passthrough
conversation.

**History that a Harness sends again during a load is not written as Events.** The log already holds
that conversation. An Adapter drops what the Harness replays, and it writes Events again only for
work that starts after the load.

**Same Harness, same Model, same working directory.** A change of Model is item 5 of #117, and it is
a separate decision. The Workspace Root check runs again at the start of each Run, because the Host
config can change while a Session is `Closed`. The Approval Policy carries over with no new code,
because the fold reads it from the log.

**A reopen that fails leaves the Session `Closed`.** The Harness history can be gone, or the Model can
be gone from the Vendor, or the Harness can refuse the load. Each of these writes `Error`, then
`SessionEnded{failed}`, and the transcript keeps the Harness's stderr. There is no archive action. A
Session ends forever only when its Harness history is gone.

## What does not change

- The boot sweep. It still closes every Session that had a live Run with `SessionEnded{lost}`,
  refuses every open question, closes every open Tool Call and unloads the Model.
- A Harness crash still surfaces and stops. The Daemon does not restart the Harness. The user's next
  Prompt starts a new Run.
- The Event log, the Envelope and the sixteen Event Kinds. `SessionStarted` and `SessionReady` appear
  once per Run and not once per Session.

## The log and the Harness history can disagree at the end

A Run that dies during a Prompt leaves two records. The log closes every open Tool Call as `unknown`
or `refused`. The Harness history can hold a partial turn, or a tool result that the log never saw.
The log is what the user sees. The Harness history is what the Model sees. Dispatch does not
reconcile them, because it cannot edit a Harness's history and it must not edit its own log.

## Considered options

- **A new Session with a new id that loads the old history.** This keeps "a Session never survives
  the Daemon" true, and it changes the least code. Rejected: one conversation would be split across a
  new page after each update or maintenance restart, and item 3 asks for the Session itself to
  survive.
- **The Daemon reopens every lost Session at boot.** Rejected: with one Session per Host, only one can
  start, so the Daemon would have to choose. It would also load Models while nobody is watching.
- **A Resume button.** Rejected: it adds a step and gives the same result as the next Prompt.
- **Keep the name `Ended`.** Rejected: the word says the opposite of what the state now does.
- **Only Pi and OpenCode Sessions survive.** Rejected: the log already holds all of a passthrough
  conversation, so passthrough costs one short function.

## Consequences

- `SPEC.md` scoped this out of v1. Its scope row, its failure table and behaviour 5 change with this
  ADR, and ADR 0008 carries a correction note.
- Before the build, two captures must confirm what the research took from documentation. The first
  is Pi's `--session <file>` in `--mode rpc`. The second is which OpenCode call loads a session
  without a replay: `session/resume` or `session/load`.
- The Client draws a `Closed` Session with an open Prompt box, and a Prompt to it can get an
  Admission refusal. Before this ADR, only a start could get one.
