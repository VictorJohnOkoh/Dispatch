# Dispatch v1 build spec

This is the destination of the architecture map. Everything below is decided. Building it should be
execution, and where it is not, that is a defect in this document rather than a question to reopen.

## How to read this

`CONTEXT.md` is the vocabulary. Read it first, and use its words. This spec does not repeat it: where
a term is capitalised here it means exactly what `CONTEXT.md` says it means.

`docs/adr/` holds the arguments. Twelve ADRs, and each title is its decision, so the index in
`CLAUDE.md` is usually the whole answer. Open an ADR when you change the area it owns.

What this spec adds is the assembly plus the last five decisions, which are the ones no earlier
ticket owned: how many Harnesses, how many Vendors, the config format, the error taxonomy, and the
build order. Those are marked **Decided here** so a reader can tell them apart from the summaries.

## The frozen v1 feature list

### In

**Reach and deployment**

- Many Hosts, each running a Daemon on loopback, reached by the Hub over SSH with ed25519 key auth.
- Manual Daemon install. Copy one binary and one JSON file to the Host and start it by hand.
- One binary, two roles. `dispatch daemon` and `dispatch hub`.

**Sessions**

- One Session at a time per Host, enforced by `admission.SingleSession`.
- Five Session states, folded from the Event log, with `Ended` carrying `stopped`, `failed` or `lost`.
- Three Harnesses: passthrough, OpenCode and Pi. **Decided here**, argued below.
- Two Vendors: Ollama and llama-swap. **Decided here**, argued below.
- Per-Session Model choice, made before the Session starts.
- Approval Policy, five slots, one per `toolKind`, settable at start and changeable while running.
- Workspace Root containment on the working directory and on every delegated write.
- Interrupt, which abandons the Prompt and keeps the Session. Stop, which runs the shutdown ladder.

**The Event log and the wire**

- Sixteen Event Kinds, a per-Daemon Sequence Number, and Deltas that are never stored.
- SQLite, one `events` table, write-ahead logging, committed before sent, nothing ever deleted.
- Ten endpoints on the Daemon, the same ten under a `/v1/hosts/{host}` prefix on the Hub, plus
  `GET /v1/hosts`.
- One merged SSE stream to the Client, one stream per Host to the Hub, six frame types the last of
  which is a keepalive comment, and the Hub's `host` field on each.
- Replay from a cursor, `resync` when the cursor is unservable, and a per-Session transcript file
  beside the log holding the Harness's raw bytes.

**The Client**

- Server-rendered HTML with vanilla JS. One conversation fills the screen, other Sessions are a rail.
- A read-only Hosts view carrying the cause, the Stale stamp, the Vendors and the resident Models.
- A four-step start wizard: Host, Model, Harness, Approval Policy.
- A Session row that renders the pair, Session State and Host State, because neither alone is enough.
- A toast for an `ApprovalRequested` that arrives from a Session that is not on screen.

### Out

Each of these has a reason recorded somewhere. Nothing here is out because it was forgotten.

| out of v1 | why, and where it is argued |
| --- | --- |
| Cross-Host Sessions | [ADR 0012](docs/adr/0012-the-same-host-invariant.md). It is a redesign, not a setting |
| Self-rolled internet-facing auth or TLS | rejected in charting. SSH key auth is the whole boundary |
| Multi-user or multi-tenant operation | single user throughout |
| A native Client, React, or any heavyweight frontend | rejected in charting |
| The TUI as a second Client | wanted, and it proves the Hub's API boundary is real. Not v1 |
| Hermes as a shipped Harness | [ADR 0003](docs/adr/0003-opencode-replaces-hermes-as-the-second-harness.md). It stays as a test fixture |
| LM Studio as a Vendor | **Decided here**, argued below |
| Concurrent Sessions, queueing, eviction, VRAM accounting | [ADR 0008](docs/adr/0008-session-lifecycle-admission-and-containment.md). The seam is `admission.Policy` and only `SingleSession` is written |
| Client-driven Daemon install over SSH | deferred in charting. Manual install is the v1 shape |
| Tailscale reach | deferred in charting. It is a `HostDialer` swap, which is why it stays cheap |
| Retention, log rotation, deletion of anything | [ADR 0009](docs/adr/0009-wire-protocol-and-event-log.md). Removing it shrank the design |
| Metrics and tracing | **Decided here**. The Event log already is a Session's trace |
| Resuming a Session across a Daemon restart | [ADR 0005](docs/adr/0005-the-event-model.md). The process does not fold, so live Sessions become `lost` |
| A `raw` field or a `HarnessSpecific` Event Kind | [ADR 0005](docs/adr/0005-the-event-model.md). Raw bytes go to the transcript file |
| Streaming tool output | [ADR 0005](docs/adr/0005-the-event-model.md). Pi sends it, nothing else does, and losing it hurts on long `bash` commands |
| A Vendor Health method, or an idle/loading/busy ladder | [ADR 0007](docs/adr/0007-the-vendor-adapter-interface.md). No two Vendors expose the same one |
| Showcasing data structures and algorithms | ruled out in charting. The problem contains none naturally |

## Every deferred seam, and the simple thing that ships behind it

The map's standing rule was design the seam, ship the simple implementation. Here is every seam and
its exact v1 implementation, so nobody has to guess how simple is simple enough.

| seam | v1 implementation | what replaces it later |
| --- | --- | --- |
| `admission.Policy` | `SingleSession`. Refuses when `len(req.Live) > 0` and names the live Session in `Refusal.Blocking`. No configurable count | a count limit reads `len(req.Live)`. A VRAM policy calls `req.Vendor.Catalogue` and `req.Vendor.Resident` itself. Neither needs a new field |
| `hostset.HostDialer` | an in-process SSH client, `x/crypto/ssh`, opening a `direct-tcpip` channel to the Daemon's loopback port | Tailscale, which is a dialer returning a plain `net.Conn` |
| Daemon install | manual. Copy the binary and `daemon.json`, start it by hand | a Client-driven install over the SSH connection the Hub already holds |
| `harness.Adapter` | three: `passthrough.go`, `acp.go` for OpenCode, `pi.go` | a fourth adapter file. Nothing else moves |
| `vendors.Adapter` | two: `ollama.go`, `llamaswap.go` | `lmstudio.go`, which the research already covers |
| `harness.Spawner` | `exec.Command` with the executable path the Host config names, in its own process group | unchanged. The seam exists so tests can pass a stub, not so production can vary |
| The Client | `internal/hub/internal/web`, server-rendered, embedded | a TUI is a second `web` against the same `protocol` |
| Event compatibility | append-only semantics, no version field on an Event. The Handshake refuses a Hub and Daemon that disagree | a second protocol version in the Handshake's served set |

## Decided here: three Harnesses

**v1 ships passthrough, OpenCode and Pi.**

The ticket framed this as passthrough plus one real Harness for the minimum, or two real ones to
prove the abstraction properly. Two real ones wins, and the research is what decides it.

The Event model was shaped against Pi and degraded for Hermes. Pi is therefore the Harness the
abstraction was fitted to, and an abstraction fitted to one program proves nothing by holding for
that program. OpenCode is the one it was not fitted to, and
[#16](https://github.com/VictorJohnOkoh/Dispatch/issues/16) showed it passing all three gates against
all three Vendors, with every tool call reaching a terminal status. So the pair is not two arbitrary
Harnesses. It is the one the model was drawn from and the one that tested the drawing.

The marginal cost is one file. `harness/pi.go` sits beside `acp.go` in a package that already owns
`Sink`, `SessionSpec`, `Pipes` and the process supervision, and this repo already holds Pi captures
in `docs/research/captures/pi-vendors/` for the fixture tests. The whole cost of the second real
Harness is translation, which is the work the seam exists to isolate.

**Pi's Gate declaration is a runtime value, not a scope question.** ADR 0008 requires a Gate that
depends on a loadable component to announce itself before `Start` returns, and whether Pi's
permission-gate extension can announce has not been captured. Both answers keep Pi in v1. If it can
announce, the Adapter declares Gates and a failed load fails the launch. If it cannot, the Adapter
declares no Gates, every slot is forced to `auto`, and the Client draws a Session with no gates
anywhere. That second outcome is ugly and it is true, which is the correct pairing.

The capture is a task for the build, not a blocker on this freeze. It is listed under holes below.

## Decided here: two Vendors, and LM Studio is the one that goes

**v1 ships Ollama and llama-swap.**

The map's shorthand was one Vendor. Two is right, because one Vendor makes the Vendor Adapter a
rename rather than an abstraction. [ADR 0007](docs/adr/0007-the-vendor-adapter-interface.md) made
every reported capability three-valued because llama-swap answers `Unknown` for every Model it has
not loaded. With only Ollama in v1, `Unknown` is a value nothing ever returns, and a three-valued
design nothing exercises is unmotivated code.

Ollama and llama-swap are the two ends of the axis the research found. Ollama has rich per-Model
metadata. llama-swap has none until a Model is resident, and it is the only one of the three with a
real load and unload story, which is what `Load` and `Unload` exist for. Building against both means
the interface is exercised at both ends on the first day, rather than on the day a second Vendor
arrives and the shape turns out to have been Ollama's all along.

LM Studio goes, and it is the right one to drop for two reasons. Its metadata is the same shape as
Ollama's, so it is a third point on a line already drawn. And it needs a desktop application running
on the Host, which is the worst fit for a headless machine in another room. The adapter is small and
the research already covers it, so adding it later is an afternoon rather than a project.

## The system

Three programs and two paths.

```
  browser                    the Hub                        a Host
  +--------+   HTTP + SSE   +----------+   SSH tunnel    +-----------------------+
  | Client | -------------> |   Hub    | --------------> | Daemon                |
  |        | <------------- |          | <-------------- |  - Session registry   |
  +--------+  one merged    +----------+  one stream     |  - Event log (SQLite) |
              stream                      per Host       |  - Harness process    |
                                                         |  - Vendor poll        |
                                                         +-----------------------+
                                                            Harness <-> Vendor
                                                            over loopback only
```

The Control Plane is the top row: Session commands and Events, Client to Hub to Daemon, across the
network. It never carries a prompt for a Harness-backed Session.

The Data Plane is the bottom right, and it does not appear in the top row at all. A Harness reaches a
Vendor on its own Host over loopback, and the tokens never leave the machine.
[ADR 0012](docs/adr/0012-the-same-host-invariant.md) is why no type in the system can express
otherwise.

## Component responsibilities

[ADR 0011](docs/adr/0011-one-binary-two-roles.md) holds the table and the argument. The short version:

**The Daemon** owns everything on one Host. The Session registry, the Harness processes, the Event
log and its Sequence Numbers, the Vendor poll, admission, Workspace Root and the Approval Policy. It
is the only writer of the log. It knows nothing about any other Host and has no field that could hold
one.

**The Hub** holds a connection to every configured Host, owns Host State, merges the streams into one
for the browser, and serves the Client. It stores nothing. It adds a `host` field to a Frame and
never to an Event, and it forwards payloads byte for byte without parsing them, which is what lets an
Event Kind the Hub has never heard of still reach the Client.

**The Client** renders Events and never raw Harness output. It applies live Events itself, so it
folds Session State in JS against the same JSON fixture `session.Fold` is tested with.

## The interfaces

Four, and each is already written in the ADR that owns it. Do not redesign them from the summary.

| interface | package | ADR | shape |
| --- | --- | --- | --- |
| `harness.Adapter` | `internal/harness` | [0006](docs/adr/0006-the-harness-adapter-interface.md) | `Capabilities()`, `Start(ctx, spec, out) (Run, error)`. `Run` has `Prompt`, `Interrupt`, `Close`. The Adapter owns the conversation and never the process |
| `harness.Sink` | `internal/harness` | [0006](docs/adr/0006-the-harness-adapter-interface.md) | the calls an Adapter may make, and the only way it can report anything. `Approve` is the one that blocks, and it is how the Daemon answers a gated Tool Call |
| `vendors.Adapter` | `internal/vendors` | [0007](docs/adr/0007-the-vendor-adapter-interface.md) | `Endpoint()`, `Catalogue()`, `Resident()`, `Load()`, `Unload()`. No `Health`, because reachability is a call succeeding |
| `admission.Policy` | `internal/admission` | [0008](docs/adr/0008-session-lifecycle-admission-and-containment.md) | `Admit(ctx, Request) *Refusal`. Asked once, before the Session exists |

Two smaller seams matter as much and are easy to miss. `harness.Spawner` is a function the Daemon
passes into `SessionSpec`, which is what keeps process ownership out of the Adapter. And
`hostset.HostDialer` is the SSH seam, which is what makes the tier-three test possible over
`net.Pipe`.

## The protocol and the log

[ADR 0009](docs/adr/0009-wire-protocol-and-event-log.md) owns this. Ten endpoints on the Daemon:

| method | path | what it does |
| --- | --- | --- |
| `GET` | `/v1/events` | the Host's Event stream. `Last-Event-ID` resumes it |
| `POST` | `/v1/sessions` | start a Session. Admission runs here. The one command with a body in its answer |
| `POST` | `/v1/sessions/{id}/prompts` | submit a Prompt |
| `POST` | `/v1/sessions/{id}/approvals` | decide one held Tool Call |
| `POST` | `/v1/sessions/{id}/policy` | set the Approval Policy |
| `POST` | `/v1/sessions/{id}/interrupt` | abandon the Prompt, keep the Session |
| `POST` | `/v1/sessions/{id}/stop` | stop the Session |
| `GET` | `/v1/sessions` | this Host's Sessions |
| `GET` | `/v1/sessions/{id}/events` | one Session's Events, paged |
| `GET` | `/v1/models` | the Vendor catalogue |

The Client's ten are the same under `/v1/hosts/{host}`, plus `GET /v1/hosts`. The Hub's command
handler is one function serving all of them, and it would serve an eleventh unchanged.

Frames on the stream: `hello`, `event`, `delta`, `vendors`, `resync`, and a keepalive comment every
10 seconds. The Hub adds `host` to each and sends its own keepalive on the same beat. `id:` appears
only where a frame advances the cursor, and the Hub rewrites it to the whole compound cursor, because
an `EventSource` keeps only one `Last-Event-ID`.

The log is SQLite with write-ahead logging, one `events` table whose five columns are the wire shape,
one index. An Event is committed before it is sent. An open message flushes every 4 KiB, so a crash
keeps what it had. Nothing is ever deleted, so a cursor is never too old, and a `resync` means the
log's identity changed or the cursor is above anything ever allocated.

## The package tree

[ADR 0010](docs/adr/0010-go-package-structure-and-seams.md) owns it. Fourteen packages in five
levels, one module, one binary, and the rule that proves the graph is acyclic: **no package imports
another at its own level.** Ten of the fourteen are named after a `CONTEXT.md` term. Go 1.24 is the
floor, because ADR 0007's own test example calls `t.Context()`.

## The Client shape

Settled by the prototype on `prototype/multi-host-client`. The artefact is the argument and it stays
off `main`.

- **The primary object is the conversation.** One transcript fills the screen. Other Sessions are a
  rail. The competing answer, one list of every Session on every Host, reads as a thing to administer.
- **The Hosts view is read only.** It shows machines. It does not start Sessions.
- **The start flow is four deliberate steps**, not two selects, because the thing being started runs
  for an hour on a machine in another room.
- **A Session row renders the pair.** `Working` on a `Ready` Host and `Working` on a Host that stopped
  answering ten minutes ago look identical and mean different things. No Event carries the difference,
  because it is Host State and that lives only in the Hub.
- **The toast is load-bearing.** The chosen layout hides every Session but one, so a toast is the only
  path an `ApprovalRequested` on another Host has to the user. Whatever replaces it must keep that job.
- **`Connecting` keeps its content at full strength with a mark. Only `Down` dims and stamps.**
- **Two levels of nesting is the ceiling.** `ToolCallRequested` carries no parent id, so no Client can
  draw a tree. A Prompt holds Tool Calls, a Tool Call has an end, and that is all the structure there is.
- **Never draw a Tool Call as running before its `ApprovalDecided`.** OpenCode moves a call to
  `in_progress` before it asks. `ToolCallEnded{outcome: unknown}` is grey, not red.

## Decided here: configuration

**JSON, with unknown fields rejected, and an example file beside each.**

Two files, `daemon.json` and `hub.json`, decoding into `config.Daemon` and `config.Hub`. Use
`json.Decoder.DisallowUnknownFields`, which is what turns fence one from a convention into a startup
error: a `peers` key in `daemon.json` fails to load rather than being quietly ignored.

The standard library parses it, so there is no dependency. The cost is that a human edits it without
comments, and the answer to that is `daemon.example.json` and `hub.example.json` in the repo rather
than a parser that supports comments.

The rule that matters more than the format: **no package under `internal/` imports `internal/config`.**
`main.go` reads the file, validates it, and constructs plain values. `config` may import `vendors`, so
a `VendorProfile` decodes into a `vendors.Endpoint`, and it may import nothing else from `internal/`.
`WorkspaceRoot` stays a plain string, because resolving it touches the filesystem and can fail, so
`main.go` reads the string and calls `workspace.NewRoot` with it.

## Decided here: observability, and where a failure reaches the user

**`slog` beside the Event log, `net/http/pprof` on the Daemon's loopback listener, no metrics and no
tracing.** A Session's Event log already is its trace: ordered, timestamped on one clock, complete
and durable. A second timeline over the same events gives two answers to when something happened.
What that leaves uncovered is Host-level and process-level, and `pprof` covers it for one import.

The rule that keeps the two logs from competing is ADR 0005's: **any Daemon decision that changes how
a Session behaves is itself an Event.** Everything else is an `slog` line. A package logs only where
it holds something the Event log cannot: a process, a socket, or a decision that produced no Session.

That also settles the map's **error taxonomy** entry, which turns out to need no new mechanism. Every
failure reaches the user through one of four surfaces that already exist, and which one it is follows
from two questions: does a Session exist yet, and is the Host answering.

| failure | surface | what the user sees |
| --- | --- | --- |
| Host unreachable, or nothing behind the tunnel | Host State `Down{unreachable}` or `Down{no-daemon}` | the Host card dims and stamps. Its Sessions keep their last-known state beside a Host that is not answering |
| Hub and Daemon disagree on protocol version | Host State `Incompatible` | the Host stays listed and is never retried. It is the only state the Hub stops working on |
| Admission refuses a start | HTTP `409` carrying a `Refusal` | the wizard names the blocking Session and offers to stop it and start this one. No Event is written, because no Session exists, so the refusal goes to the operational log |
| A malformed or impossible command | HTTP `422` | the Client's own bug, or a stale form. Not drawn as a system failure |
| The Harness will not launch, or its Gate fails to announce | `SessionEnded{failed}` | the Session exists, is `Ended`, and its transcript holds the process's stderr |
| The Vendor stops answering mid-Session | an `Error` Event, then whatever the Harness does next | `Error` is never terminal, so the Session stays usable if the Harness recovers |
| The Model will not load | an `Error` Event, or a failed start if it happens before `SessionReady` | the same two shapes, and which one depends only on when it happened |
| The Daemon restarts under a live Session | `SessionEnded{lost}` from the boot sweep | the Session is ended with its transcript intact. The Client offers to start a new Session here, never to resume |
| A Vendor is unreachable | neither a Host State nor an Event | the `vendors` frame carries reachability beside the resident list, so the Host card's Vendor row empties rather than going stale |

Precedence when more than one applies: Host State, then the HTTP status, then an Event, then the
operational log. A user looking at a `Down` Host is not also told that its Vendor stopped answering.

## Decided here: the build order

Seven milestones. Each one ends at something you can watch happen, because a milestone that ends at
"the package compiles" cannot be wrong in a way anyone notices. The order follows
[ADR 0010](docs/adr/0010-go-package-structure-and-seams.md)'s levels, which means the pure packages
come first and SSH comes last.

**M0. The module and the two CI checks.** `go.mod`, `cmd/dispatch/main.go` with the role switch, and
the two `go list` checks in CI on the first day. They are cheap now and they are the fence the single
binary trades away.
*Watch:* `dispatch hub` and `dispatch daemon` each print their role and exit, and CI fails a commit
that makes `internal/daemon` import `internal/hub`.

**M1. The pure packages.** `event`, `protocol`, `session`, `workspace`, `admission`. Tier-one tests,
no I/O beyond `t.TempDir()` with symlinks in it. `session.Fold` and its JSON fixture land here, and
the fixture is the contract the Client's JS will later be tested against.
*Watch:* a slice of Events built by hand folds to each of the five states, and `workspace.Contain`
refuses a symlink that points outside the Root.

**M2. The Event log.** `eventlog` against a real SQLite file. Append, the gapless `Seq` allocated
inside the write transaction, the 4 KiB flush, `Cursor`, `Replay`, `Subscribe`, the boot sweep,
`log_id`. Kill the process mid-message and check what survived.
*Watch:* a killed writer leaves a partial message in the log, and a reader resuming from a cursor
gets that message whole.

**M3. The vertical slice.** `daemon` with the passthrough Harness and the Ollama adapter, driven by
`curl`. Admission, the Session registry, the ten endpoints, the SSE stream, the Vendor poll. No Hub,
no browser, no process supervision yet, because passthrough spawns nothing.
*Watch:* `curl` starts a Session, submits a Prompt, and reads the assistant's text arriving as Deltas
on `GET /v1/events`. This is the first end-to-end path and it is deliberately early.

**M4. A real Harness process.** `harness/acp.go` for OpenCode, plus `supervise.go`, `ledger.go` and
`transcript.go`. The shutdown ladder, the process group kill, the stderr drain, the two synthesis
triggers. The stub Harness is the test binary re-executing itself, so all of it is testable with no
GPU and no OpenCode installed.
*Watch:* a Session runs a tool call that waits on the Approval Policy, and a stop kills a process
group that is refusing to die.

**M5. The Hub and the Client.** `hub`, `hostset` behind a `net.Pipe` dialer, and `web`. Cursor split
and merge, `id:` rewriting, the merged stream, the fold in JS against M1's fixture, and the whole
Client shape. The tier-three test lands here, and it is the first thing that exercises both roles.
*Watch:* a browser drives a Session through the Hub to an in-process Daemon, and closing the tab
mid-Prompt then reopening it replays the transcript whole.

**M6. SSH, and a second machine.** The real `HostDialer`, the two keepalives, backoff, the Handshake,
and Host State with all four values. This is last because it is the only part that cannot be tested
in one process.
*Watch:* pull the network cable on a Host mid-Session and the Client dims that Host inside ~25
seconds while every other Host keeps working.

**M7. The second Harness and the second Vendor.** `harness/pi.go` and `vendors/llamaswap.go`, both
against fixtures already in this repo, plus the one Pi capture that settles its Gate declaration.
They are last on purpose: they are the proof the two abstractions are abstractions, and that proof is
only worth anything once there is something for them to plug into.
*Watch:* the same Session, the same transcript rendering, one Harness swapped in the wizard.

**If v1 has to shrink**, drop in this order and stop as soon as it fits: M7's Pi adapter, then M7's
llama-swap adapter, then M6's Tailscale-shaped niceties. Do not drop M4, because a Harness the Daemon
cannot kill is the failure this whole design exists to prevent.

## Decided here: what proves v1 is done

Not a test suite. Twelve behaviours you can watch, on real machines. v1 is done when all twelve hold.

1. **Start a Session on a sleeping Host and get a useful error.** The Host reads `Down{unreachable}`,
   the Client names it, and the start button on that Host is disabled rather than failing silently.
2. **Close the laptop mid-Session and reattach with full history.** Every Event replays, including the
   assistant message that was still arriving when the lid closed, and it replays whole.
3. **Approve a tool call from a phone over a tunnel.** The `ApprovalRequested` arrives, the decision
   goes back, and the Tool Call ends with an outcome that says a human decided it.
4. **The refusal is honest.** Set `execute` to `refuse`, ask the Session to run a command, and see
   `ToolCallEnded{refused}` written from the Daemon's own `ApprovalDecided`, never from the Harness.
5. **Kill the Daemon under a live Session and restart it.** The boot sweep ends that Session `lost`,
   its transcript is intact and readable, and the Client offers a new Session rather than a resume.
6. **Kill a Harness that will not die.** Stop runs the ladder and the process group is gone, checked
   from a shell on the Host rather than from the Client.
7. **Start a second Session on a busy Host.** The refusal names the Session holding the slot, and one
   click stops that one and starts this one.
8. **Run the same prompt on two Hosts at once.** Both stream into one merged Client stream, neither
   starves the other, and the commands still answer while both are streaming.
9. **Point a Session at a directory outside the Workspace Root.** It is refused before the Session
   exists. Then have the Harness delegate a write outside it, and see that refused too.
10. **Swap the Harness and change nothing else.** The same Model, the same prompt, the same Host, and
    the transcript renders the same way for both OpenCode and Pi.
11. **Run against a Vendor with no metadata.** llama-swap reports `Unknown` for an unloaded Model, the
    Client draws `Unknown` as an answer rather than as a blank, and the Session runs anyway.
12. **Break the Handshake on purpose.** Run an old Daemon against a new Hub, see `Incompatible`, and
    confirm from the Daemon's log that the Hub stopped retrying.

Two of these are the ones most likely to be quietly skipped, so they are called out. Number 6 needs a
shell on the Host, not a green test. Number 12 needs two builds, which is an inconvenience rather
than a difficulty, and it is the only check that the Handshake is real.

## The map's Notes, re-read

Every Note on the map, confirmed or corrected against the research that landed after it was written.

**Confirmed unchanged.** Learning first and portfolio second. Data structures and algorithms are not
a goal. Go, and a dumb browser Client with no React. One binary and two roles, with Daemons that
never learn about their peers, now [ADR 0011](docs/adr/0011-one-binary-two-roles.md). Control Plane
and Data Plane stay separate, now [ADR 0012](docs/adr/0012-the-same-host-invariant.md). The Vendor
abstraction covers discovery, capability and health but not inference. Direct prompting is a
passthrough Harness rather than a second code path, and ADR 0005 confirmed it costs nothing: it is
nine of the sixteen Kinds, a strict subset, with nothing bent. Events are normalised and typed, and
the log is transport, replay buffer and history at once. Daemons bind loopback, reach is an SSH
tunnel, and there is no self-rolled internet-facing auth.

**Corrected.**

*"HTTP for commands, SSE for Events, with `Last-Event-ID` carrying the Event log offset."* The first
half holds. The offset does not. ADR 0009 found that a cursor is the highest `Seq` below every open
appendable Event, so it lags a message that is still arriving, and that lag is exactly what makes an
unfinished message replay whole. `Last-Event-ID` carries a cursor, and a cursor is built from `Seq`
without being equal to it. This also amends ADR 0005's sentence that `Seq` is both the primary key
and the `Last-Event-ID` offset.

*"One Session at a time behind a policy interface."* Confirmed, with a scope the Note did not state.
It is one Session per **Host**, not one in the system. A user driving three Hosts gets three
concurrent Sessions and no cross-Host limit anywhere, because a Daemon cannot enforce one and a
Hub-side limit would be advisory.

*"One Vendor."* Corrected to two, argued above. One Vendor leaves ADR 0007's three-valued capability
design with nothing to exercise it.

*Passthrough plus one real Harness.* Corrected to two real ones, argued above.

**Superseded, and kept visible.** The Note that Hermes is best driven as a local HTTP and SSE server
was wrong: that surface does not exist in Hermes v0.19.0. The lesson generalises and is worth keeping
where people read it. This vendor's docs describe endpoints it does not ship, so a documentation-only
claim needs an empirical check before anything is designed against it.

## Holes carried into the build

Named, so that nobody discovers them by being surprised.

- **The Pi Gate capture.** Whether Pi's permission-gate extension can announce itself before `Start`
  returns. One capture. Both answers keep Pi in v1 and only one of them is pleasant.
- **The Vendor fixtures do not exist.** Finding R8. Tier-two tests for `vendors` need recorded bodies
  for a caller-supplied `http.RoundTripper`, and nothing has recorded them yet. It blocks M7 and part
  of M3.
- **No OpenCode refusal has ever been tested.** Every captured permission request was answered with
  allow, so what `reject_once` does is an assumption. It is cheap to settle during M4 and expensive to
  discover during M5.
- **The Hermes launcher-chain failure is unexplained.** Eight causes were ruled out by measurement and
  the cause is still open. It does not block anything, and it is the reason the Daemon must try to run
  a Harness rather than ask whether it is installed.
- **`web` importing `event` is one import's worth of decision.** It is the only Hub code that knows an
  Event Kind exists. Serving an empty shell and letting the JS fill it would remove that import, and
  M5 is where to weigh it against the cost of a blank first paint.
- **The fold is written twice**, in `session` and in the Client's JS, sharing one JSON fixture. It is
  the only duplicated logic in the design, and it is duplicated because the Client applies live Events
  itself.
- **A recorded frame proves what a Harness said in August 2026 and nothing about September.** Every
  fixture in this repo is a snapshot, so re-capturing is a recurring task rather than a one-off.
