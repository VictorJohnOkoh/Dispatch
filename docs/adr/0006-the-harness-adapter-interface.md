# The Daemon owns the Harness process and the Adapter owns the conversation

A Harness Adapter could have owned its child process end to end, spawning it, holding its pipes and
emitting Events, which would have made the Daemon a thin router. Instead the Daemon spawns, holds and
kills the process, and the Adapter gets two pipes and a place to put facts. We chose this because every
process defect this project found is shared by both Harnesses and belongs to none of them, while
everything that differs is protocol. Three Adapters each owning stdin is three chances to write the bug
that took the longest to find in this project's research.

Two smaller decisions follow from the same reading of the bytes. An Adapter may repair correlation and
may never invent a fact. An Approval Policy slot the Adapter cannot gate is refused when it is set,
rather than degraded while the Session runs.

## What the captures decide

Four facts pick the seam, and none of them is about parsing.

**The process quirks are shared.** Pi's `-p` blocks on an open stdin even when the prompt is an
argument, and `--mode rpc` truncates a run mid-turn when stdin closes. Hermes deadlocked on Windows
because a spawned grandchild inherited the pipe, for 118.83s against a 120s client timeout, and eight
runs tracked the timeout to within 6s. OpenCode could not be spawned by its PATH name at all
(`WinError 2`); the supervisor had to name `node_modules/opencode-ai/bin/opencode.exe`. Three
Harnesses, one class of bug, and it is stdin and executable resolution every time.

**The protocol differences are real but small.** `opencode acp` and `hermes acp` are JSON-RPC 2.0 over
ndjson. `pi --mode rpc` is LF-delimited JSONL with an echoed `id`. Both are line-oriented and both are
bidirectional.

**Both Harnesses need answers written back, so an Adapter is not a parser.** OpenCode blocks on a
`session/request_permission` response and calls `fs/write_text_file` on its client. Pi blocks on an
`extension_ui_response`. A parsing-only Adapter cannot answer, so both wire formats would end up in the
Daemon, which is the seam not existing.

**Neither reports a refusal the Daemon can trust.** Hermes returns `denied by ACP client` for a human's
refusal and for an internal scheduling failure alike, and Pi reports refusal as `isError: true` with
free text an extension author chose. So the Approval Policy records its own decision and reads the
Harness only as corroboration.

## The interface

```go
package harness

// Adapter is everything one Harness contributes. One value per Harness, made once
// at Daemon start and shared by every Session.
type Adapter interface {
	// Capabilities is fixed for the life of the Daemon and is read before a Session
	// starts, so an Approval Policy it cannot honour is refused rather than degraded.
	Capabilities() Capabilities

	// Start brings up one Session and returns once the Harness is running the Model
	// it was given. It returns an error rather than a degraded Session.
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

// Capabilities is what a Harness can do, declared rather than discovered.
type Capabilities struct {
	// Tools is false only for passthrough. A Session with no tools has no Approval
	// Policy at all, which is an absence rather than five empty slots.
	Tools bool

	// Gates says, per ToolKind, whether this Adapter can hold a Tool Call until the
	// Daemon has decided. A slot that is false may only be set to Auto.
	Gates [event.NumToolKinds]bool
}

// SessionSpec is everything the Daemon hands an Adapter. It carries no Approval
// Policy: an Adapter never learns what the user allowed, so it can never answer a
// gate on the Daemon's behalf.
type SessionSpec struct {
	Session event.SessionID
	Model   string          // the Model id, spelled as the Vendor spells it
	Vendor  vendor.Endpoint // base URL and API style, on this Host's loopback
	Dir     string          // the working directory, already inside the Workspace Root

	// Spawn starts the Harness executable that the Host config names. The Daemon
	// owns what comes back: it holds stdin, drains stderr into the Session's
	// transcript, and kills the process group at shutdown. Passthrough never calls it.
	Spawn Spawner

	// Files is contained file access, for a Harness that delegates writes to its
	// client. Paths resolve against Dir and never leave the Workspace Root.
	Files Files
}

// Spawner starts the Harness with arguments the Adapter chooses. The executable
// itself is named by the Host config and never guessed from PATH.
type Spawner func(ctx context.Context, l Launch) (Pipes, error)

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

// Sink is the Daemon, as an Adapter sees it. Only Approve returns an error. If the
// Daemon cannot write an Event it cancels the Session's context, and the Adapter's
// reader returns from that instead of from a return value it might ignore.
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
```

Seven Sink methods, one per fact an Adapter can produce. `Message` and `Reasoning` are separate rather
than one call taking a Kind, because a Kind argument would have twelve values that are mistakes.

## Why the Daemon owns the process

The test is which side each rule belongs to. Sort the findings and they do not mix.

| the rule | Pi | OpenCode | Hermes | side |
| --- | --- | --- | --- | --- |
| Own stdin explicitly, never inherit | yes | yes | yes | Daemon |
| Resolve the executable, not the PATH name | yes | yes | yes | Daemon |
| Do not let a grandchild inherit the pipe | n/a | n/a | yes | Daemon |
| Drain stderr or the pipe fills | yes | yes | yes | Daemon |
| Kill the group, not the process | yes | yes | yes | Daemon |
| Answer a blocking permission request | yes | yes | yes | Adapter |
| Read the wire format | JSONL | ndjson | ndjson | Adapter |
| Map a tool name to a ToolKind | yes | no | no | Adapter |
| Repair a missing correlation id | yes | no | yes | Adapter |

Every rule in the first block is the same on all three Harnesses, and every rule in the second differs.
That is the seam, and it is not a judgement call.

The Adapter still chooses the arguments, because `acp` and `--mode rpc` are the one Harness-specific
part of a launch. It does not choose the executable. The Host config names an absolute path per
Harness, which is the only thing that survives an npm shim on the PATH that answers a shell and nothing
else.

`Spawn` being a function the Adapter calls, rather than a step the Daemon performs before `Start`, is
what lets passthrough fit. Spawning is a service offered, not a stage every Adapter must pass through.

## An Adapter may invent correlation and never a fact

The rule, in one sentence:

> An Adapter may decide what belongs with what. It may not report something that did not happen.

Allowed, because each of these needs Harness-specific knowledge that exists nowhere else:

- Attaching a `toolCallId` to Pi's approval request, which carries only `id`, `method`, `title` and
  `options`, with the command inside a display string that has emoji and newlines in it. Pi asks
  between its own start and end frames, so the open call is the answer.
- Mapping a native tool name to one of the five ToolKind values. Pi sends `toolName` and no kind.
  OpenCode and Hermes send `kind` directly, so their Adapters map nothing.
- Deciding where one message ends and the next begins. OpenCode gives reasoning its own `messageId`,
  and Pi puts `thinking` in the same message's content array.

Not allowed: synthesising the `ToolCallEnded` that Hermes never sends for `read` and `edit`. ADR 0005
put that on the Daemon with one trigger, when a Prompt completes. The reason to keep it there rather
than push it down is bookkeeping. The Daemon already holds the ledger of open calls per Session, three
Adapters holding the same ledger is three chances to leak one, and only one Harness in the repo needs
it. Synthesis is also the only case where an Event describes an absence, so it belongs where absences
are already handled.

## Stdin, stderr and shutdown

**Stdin.** The Daemon opens it as a pipe, holds it open for the life of the Session, and closes it
once. It is never inherited, and the process is spawned so that no grandchild inherits it either. The
type says so: `Pipes.In` is an `io.Writer`, so an Adapter that wanted to close stdin cannot.

**Stderr.** Never given to an Adapter, always drained, always written to the Session's raw transcript.
Draining is not optional, since a full pipe stops the child. Parsing it is forbidden, and Hermes is the
reason. Its phantom denial produced no stderr warning at all, so stderr was silent in exactly the case
a supervisor would have wanted it to speak.

**Crash against clean exit.** A Harness in RPC or ACP mode does not end by itself. So any exit before
`Close` is a failure whatever the exit code, and the Daemon writes `Error{harness_failed}` then
`SessionEnded{failed}`. Exit code 0 is not evidence of a clean finish here. The Daemon decides this
rather than the Adapter, because the Adapter sees EOF on a pipe and the Daemon sees the process.

**Shutdown is graceful then forced, in that order.**

1. `Run.Close`, so the Adapter sends the protocol's own goodbye. `session/close` on ACP. Pi has none,
   so Pi's Adapter sends nothing.
2. The Daemon closes stdin. This is the signal both Harnesses actually answer. Pi exits on EOF, and the
   Hermes deadlock went from 118.83s to 1.18s when stdin was closed before the tool ran.
3. The Daemon waits a fixed, short time.
4. The Daemon kills the process group. The group, not the process, because Hermes' hang lived in a
   grandchild.

Step 3 is the only timer in the design, and the rule that keeps it honest:

> A timer may end a Session. It may never diagnose one.

Hermes' tool duration tracked its client's timeout across eight runs at three different values, so a
timeout used as a measurement measures itself. A timeout used to stop waiting is fine, because nothing
is being concluded from it.

## Gates, and failing when the policy is set

`Capabilities.Gates` is five booleans indexed by ToolKind, the same shape as the Approval Policy's five
slots. The rule joining them:

> A slot with no Gate may only be set to Auto. Setting it to Wait or Refuse fails, at Session start and
> at every change while the Session runs.

One rule, two call sites, and it is the same code both times. The Session that would have degraded
silently now never exists.

This is stronger than ADR 0003 required and it does not reopen it. ADR 0003 said the Approval Policy
cannot honour Wait or Refuse for reads on OpenCode, and that a Client "has to say so". Refusing the
setting is how it says so, in the one place that cannot be forgotten. What ADR 0005 asked for, that the
Client show a slot as ungated rather than as whatever the array says, still stands and now has a
source:

**`SessionStarted` gains a `gates` field**, the Adapter's five booleans, written by the Daemon at
Session start. The Client folds it out of the log with everything else and renders an ungated slot
without knowing which Harness it is looking at. This adds an optional field to an existing kind, which
is what ADR 0005's compatibility rule allows, so it extends the Event model rather than reopening it.

Capabilities are declared, not read out of the Harness's own handshake, and OpenCode is why. Its
`initialize` advertises `loadSession`, `mcpCapabilities`, `promptCapabilities` and four session
capabilities, identically in every captured run, and none of that mentions gating. The one thing
the Daemon has to know, that `read` never asks, appears nowhere in what the Harness says about itself.
It was learned by counting frames: `edit` 1 started, 1 ended, 1 asked. `execute` the same. `read` 2
started, 2 ended, **0 asked**.

## The Model is selected, never configured and hoped for

`Start` returns only once the Harness is running `spec.Model`. An Adapter that cannot confirm the Model
returns an error, and no Session exists.

This is not a general principle reached for. The captures show the same config key honoured in one run
and overridden in another.

Every run writes a working-directory `opencode.json` naming a Model, and `session/new` reports back
what the Session will actually use. In the 2026-08-27 captures it reported a Model nobody asked for:

```json
{"id": "model", "currentValue": "opencode/big-pickle", ...}
```

`opencode/big-pickle` is a hosted OpenCode Zen model, not the local Vendor, and the config had named
`capstone/qwen3.5:9b`. In the 2026-08-28 llama-swap capture, with OpenCode Zen no longer in the
provider list, the same field reports `capstone/qwen3.5-9b`, which is what the config asked for. Its
`read` run took 29.70s to first chunk against 1.26s and 1.27s for the other two, which is llama-swap
loading the model on first use, so the local Vendor served that Session.

So the config key works and is not authoritative. Something in the global config outranked it once
already, silently, and the only reason we know is that `session/new` reports the answer. A Daemon that
writes the file and assumes would have run a Session on the wrong Model, against a hosted endpoint, and
logged the Model it intended.

Hence the rule. A config file makes a Vendor reachable. Selecting the Model is a separate act, and the
Adapter reads back what the Harness says it selected before `Start` returns. OpenCode reports it on
`session/new`. Pi reports `api`, `provider` and `model` on every `message_end`.

Checking that turned up a second problem, recorded here because it is what the re-capture fixed.
Until 2026-08-28, **`captures/opencode/llamaswap/` held the LM Studio run.** It was one capture stored
twice, not two runs that happened to agree:

- The three `*-frames.jsonl`, the three `*-raw.log` and `session-opencode.json` are byte-identical to
  `lmstudio/`'s by sha256.
- The OpenCode session ids match, and OpenCode mints a fresh one on every `session/new`:
  `ses_fbaced840ffecF2q3xGDWdrzPa` for the edit run in both directories.
- The frame timestamps match to the microsecond, starting `2026-08-27T21:47:22.916397+00:00`.
- `llamaswap/manifest.txt` reads `Vendor: lmstudio at http://127.0.0.1:1234`. llama-swap is on
  `127.0.0.1:8080` on this Host, which `captures/pi-vendors/llamacpp/manifest.txt` records.

Sharing a Model id would have been ordinary, since llama-swap can serve the GGUF LM Studio downloaded.
Sharing a session id and a microsecond is not. The cause was the script's Vendor fallback: llama-swap
was not serving, the run silently used LM Studio instead, and the result was filed under the Vendor
that had been asked for rather than the one that answered.

The 2026-08-28 re-capture is a real llama-swap run, and all three gates pass on it. So Vendor coverage
now rests on three Vendors rather than two. **A fallback that warns is a fallback that mislabels**, and
that is a rule for the Daemon as much as for a capture script: a Session must record the Vendor that
served it, never the one it was configured with. This is the same failure the capture README already describes, where each run
overwrote the last and LM Studio had to be recovered from the Host; the recovery landed in the wrong
directory as well. ADR 0003's gates survive it, because Ollama alone passes all three. "The Event
vocabulary is Vendor-independent, compared frame by frame across all three" does not.

For the interface the answer is the same either way. A config file makes a Vendor reachable. Selecting
the Model is a separate act through whatever the Harness offers, an argument on the command line or a
call in the protocol, and the Adapter checks the answer before `Start` returns. Pi reports `api`,
`provider` and `model` on every `message_end`, so its selection is checkable. OpenCode reports the
current Model on `session/new`, which is precisely what would have caught this.

## Passthrough

Passthrough implements the interface with no special case anywhere.

| what it does | how |
| --- | --- |
| `Capabilities` | `Tools: false`, every Gate false |
| `Start` | dials `spec.Vendor`. Never calls `spec.Spawn`, so no process exists |
| `Prompt` | posts a completion and reads the SSE body |
| `Reasoning` and `Message` | one call per chunk, from `reasoning`, `reasoning_content` or whichever field this Vendor uses |
| `Completed` | the final usage, once |
| `Failed` | `vendor_error` for Ollama's unframed error JSON and LM Studio's bare string under HTTP 200 |
| `Interrupt` | cancels the request |
| `Close` | closes the response body |

Nothing is bent to fit. It never calls `Spawn` or `Files`, and never touches the four tool methods,
which is the same strict subset ADR 0005 found when it checked passthrough against the Event kinds.

One asymmetry, named rather than papered over: **a passthrough Session has no raw transcript.** The
transcript exists to record what a Harness said, and there is no Harness. Its Vendor errors still reach
the Event log with the original text, which ADR 0005 already requires. This is the same shape as its
missing Approval Policy, an absence rather than an empty file.

## Testing without a Harness

The seam is where it is partly to make this true. An Adapter reaches the outside world through `Spawn`,
`Files` and `Sink`, all three supplied by the caller, so a test supplies all three and no process
starts.

```go
// A recorded run, replayed from the bytes in docs/research/captures/.
func TestOpenCodeNeverAsksAboutReads(t *testing.T) {
	s := scriptFrom(t, "captures/opencode/ollama/read-frames.jsonl")
	spec := SessionSpec{
		Model: "capstone/qwen3.5:9b",
		Dir:   t.TempDir(),
		Spawn: func(context.Context, Launch) (Pipes, error) {
			return Pipes{In: s.Stdin(), Out: s.Stdout()}, nil
		},
	}
	// ... Start, Prompt, then assert on what the Sink recorded.
}
```

A plain byte replay is not enough, because both Harnesses stop and wait for the client. OpenCode will
not proceed past `session/request_permission` until it has a response, and Pi will not proceed past
`extension_ui_request`. So the fixture is a **scripted** transport rather than a recording. It feeds the
next lines when it sees the write it was expecting, and fails the test on a write it was not. The
repo's captures are already in that shape, with `*-frames.jsonl` carrying a `dir` of `in` or `out` on
every frame, which is the script.

Three tests worth naming, because they are the ones that would have caught real defects:

- Replay Hermes' twelve-tool Linux run. Nine `read` and `edit` calls go quiet, the Adapter reports
  nothing for them, and the Daemon's synthesis writes nine `ToolCallEnded{unknown}` when the Prompt
  completes. Hermes is in the repo to be the Harness that goes quiet.
- Replay Pi's deny capture. The gate arrives with no `toolCallId`, the Adapter attaches the open call's
  id, and `ApprovalRequested` carries an id the Client never learns was invented.
- Replay OpenCode's read run. Two reads, both completing, and `Approve` never called.

No Harness, no Vendor, no GPU, no network. This is a partial answer to the map's open question about
testing without a GPU. It covers translation and correlation completely, and covers process supervision
not at all, because supervision is the part with a real process in it.

## What the third Adapter costs

Hermes is the estimate, because it is already the third Adapter and it is in the repo.

A third **ACP** Harness costs a `Capabilities` value and a `Launch`. The ACP Adapter is the protocol,
not the Harness, so OpenCode and Hermes share it and differ by a struct literal. Hermes' literal is
`Tools: true` with `execute` gated and `read` and `edit` not, plus the argument `acp`. That is the whole
of it, and its incomplete tool lifecycle needs no code at all, because a Harness that goes quiet is
already handled by the Daemon's synthesis.

A third Harness with its **own** wire format costs what Pi's costs. One file: a line reader, a switch
over about ten event types, a tool name to ToolKind mapping, and a one-entry ledger for correlation
repair.

What no Adapter ever writes: spawning, stdin, executable resolution, stderr, the transcript, the
shutdown ladder, the process group kill, sequence numbers, timestamps, the open Tool Call ledger,
synthesis, the Approval Policy, and Workspace Root containment.

## Considered options

- **The Adapter owns its child process.** Deep Adapter, thin Daemon, and the ticket's own framing.
  Rejected: sort the findings and every process rule is identical across all three Harnesses while
  every protocol rule differs. Three Adapters would each own stdin, executable resolution, stderr
  draining and the kill ladder. Getting exactly those wrong is what produced the Hermes deadlock, and
  what stopped OpenCode from starting at all until the supervisor stopped spawning its PATH name.
- **The Adapter parses and nothing else.** Trivial to test, and the ticket's other option. Rejected:
  OpenCode blocks until its client answers `session/request_permission` and calls `fs/write_text_file`
  on it, and Pi blocks on `extension_ui_response`. A parser cannot answer, so the Daemon would hold both
  wire formats and the seam would have moved rather than existed.
- **Events on a channel instead of a Sink.** The idiomatic Go shape for a stream. Rejected: approval is
  a question with an answer, so it needs a reply channel per question and a table to match them. A
  blocking method call is the same thing with the bookkeeping deleted.
- **The Adapter synthesises the missing `ToolCallEnded`.** Puts the repair next to the Harness that
  needs it. Rejected: it is a fact that did not happen, ADR 0005 already gave it one trigger and one
  home, and three Adapters keeping the same ledger is three chances to leak an open call.
- **Read Capabilities from the Harness's own handshake.** ACP advertises `agentCapabilities` on
  `initialize`, so it is right there. Rejected: it exists only for ACP, and it says nothing about
  gating. OpenCode's handshake is identical in every captured run and none of it hints that `read`
  never asks. The one thing the Daemon needs is the one thing the Harness does not say.
- **Start the Session and mark the unhonourable slot ungated.** What ADR 0005 assumed. Rejected: that is
  silent degradation of an approval gate, which the ticket named as the worst option available. Refusing
  the setting says the same thing loudly and earlier.
- **Give the Adapter the Approval Policy so it can answer without a round trip.** Rejected: two places
  would then hold what may run unattended, and the Adapter could answer a gate differently from the
  Daemon that logged the decision.
- **Give the Adapter stderr as well as stdout.** Rejected: nothing in three Harnesses' stderr is a
  signal. Hermes' phantom denial wrote nothing there at all.
- **Passthrough as a separate path rather than an Adapter.** Rejected in `CONTEXT.md` before this
  ticket, and the fit above is the confirmation the ticket asked for.

## Consequences

- [#12](https://github.com/VictorJohnOkoh/Capstone/issues/12) gets a package boundary it did not have to
  invent. `harness` holds this interface and the Pi, ACP and passthrough Adapters. The process
  supervisor, the ledger of open Tool Calls, and the transcript writer are the Daemon's and sit outside
  it. `Sink` is the Daemon's implementation, so `harness` imports the Event types and nothing else of
  the Daemon.
- [#9](https://github.com/VictorJohnOkoh/Capstone/issues/9) inherits the whole supervision contract: an
  absolute executable path per Harness in the Host config, spawning so that no grandchild inherits
  stdin, draining stderr into the transcript, the four-step shutdown, killing the process group, one
  fixed timer that may end a Session and never diagnose one, and `harness_failed` on any unprompted exit
  whatever its exit code. It also owns the two call sites of the Gate rule, and the `Files`
  implementation that keeps a delegated write inside the Workspace Root.
- [#11](https://github.com/VictorJohnOkoh/Capstone/issues/11) reads `gates` off `SessionStarted` and
  renders an ungated slot from the log, so no Client hardcodes that OpenCode does not gate reads. It
  also must not offer Wait or Refuse on a slot with no Gate, since setting it will be refused.
- [#8](https://github.com/VictorJohnOkoh/Capstone/issues/8) owns `vendor.Endpoint`, the one type this
  interface takes from it, and the passthrough Adapter is its only Harness-side caller. If a passthrough
  Session ever needs a raw capture, it is #8's to keep and not the Session transcript's.
- `SessionStarted` gains an optional `gates` field. Under ADR 0005's compatibility rule this is an
  addition, so the Event model is extended rather than reopened.
- `CONTEXT.md` gains **Harness Adapter** and **Gate**, and **Approval Policy** gains the sentence that a
  slot with no Gate may only be Auto.
- The `harness` package's tests need a scripted transport that reads the repo's own `*-frames.jsonl`
  captures. That fixture is what makes every Adapter testable, and it is small: read frames, expect the
  `out` ones, feed the `in` ones in order.
- **`docs/research/opencode-acp-host.md` and #16 need correcting.** Their Vendor coverage table lists
  three Vendors on evidence that held two, because `llamaswap/` was the LM Studio run. The 2026-08-28
  re-capture makes the claim true, so the fix is to land those bytes and say which run supports which
  row, not to withdraw the conclusion.
- The Model that a Session actually used belongs in the Event log, not just in the Daemon's intent.
  `SessionStarted` already carries `model`, and the Daemon writes it from what the Adapter confirmed
  rather than from what it asked for. The two differed once already.
- An Adapter tolerates a native kind that is **absent**, not only one it does not recognise. ADR 0005
  took the second rule from Hermes' out-of-schema `usage_update`. The first comes from the llama-swap
  capture, which emits no `usage_update` at all where Ollama and LM Studio do. It costs nothing here,
  because per-Prompt `usage` comes from the `session/prompt` response on all three, but an Adapter that
  waited for a notification would have hung on one Vendor and not the others.
- Pi's provider registration lives in a Host-level `models.json` written once at Host setup, and the
  Model is chosen per Session by `--model`. `PI_CODING_AGENT_DIR` would give real per-Session isolation
  and has never been tested, so it is not designed against.

This does not reopen ADR 0003 or ADR 0005. ADR 0003 asked that a Client say when a gate is missing, and
the Gate rule is that requirement made unforgettable. ADR 0005 gave the Adapter a fixed output type and
three obligations, and all three are here: the correlation repair and the ToolKind mapping are the
Adapter's, and every `ToolCallRequested` is paired with a `ToolCallEnded` because the Daemon closes what
the Adapter leaves open.
