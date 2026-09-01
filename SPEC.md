# Dispatch v1 build spec

This is the destination of the architecture map. Everything below is decided. Building it should be
execution, and where it is not, that is a defect in this document rather than a question to reopen.

## How to read this

`CONTEXT.md` is the vocabulary. Read it first, and use its words. This spec does not repeat it: where
a term is capitalised here it means exactly what `CONTEXT.md` says it means.

`docs/adr/` holds the arguments. Twelve ADRs, and each title is its decision, so the index in
`CLAUDE.md` is usually the whole answer. Open an ADR when you change the area it owns.

What this spec adds is the assembly, the decisions no earlier ticket owned, and four corrections to
ADRs that contradict or under-specify something. Everything original to this document is marked
**Decided here** so a reader can tell it apart from the summaries.

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
- Three Vendors: Ollama, LM Studio and llama-swap. **Decided here**, argued below.
- Per-Session Model choice, made before the Session starts.
- Approval Policy, five slots, one per `toolKind`, settable at start and changeable while running.
- Workspace Root containment on the working directory and on every delegated write.
- Interrupt, which abandons the Prompt and keeps the Session. Stop, which runs the shutdown ladder.

**The Event log and the wire**

- Sixteen Event Kinds, a per-Daemon Sequence Number, and Deltas that are never stored.
- SQLite via `modernc.org/sqlite`, one `events` table, write-ahead logging, committed before sent,
  nothing ever deleted from the log.
- A per-Session transcript of the Harness's raw bytes, capped at 64 MB with a truncation marker.
- Ten endpoints on the Daemon, the same ten under a `/v1/hosts/{host}` prefix on the Hub, plus
  `GET /v1/hosts`.
- One merged SSE stream to the Client, one stream per Host to the Hub, six frame types the last of
  which is a keepalive comment, a seventh `host` frame the Hub originates, and a `host` field the Hub
  adds to all of them.
- Replay from a cursor, and `resync` when the cursor is unservable.

**The Client**

- Server-rendered HTML with vanilla JS. One Session fills the screen, the others are a rail.
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
| A native Client, and React or anything like it | rejected in charting |
| The TUI as a second Client | wanted, and it proves the Hub's API boundary is real. Not v1 |
| Hermes as a shipped Harness | [ADR 0003](docs/adr/0003-opencode-replaces-hermes-as-the-second-harness.md). It stays as a test fixture |
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
| `vendors.Adapter` | three: `ollama.go`, `lmstudio.go`, `llamaswap.go` | a fourth adapter file. The interface does not move, which is the claim three Vendors exist to test |
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

## Decided here: three Vendors

**v1 ships Ollama, LM Studio and llama-swap.**

The map's shorthand was one Vendor. Three is right, and the reason is that the Vendor Adapter's whole
shape is an answer to how differently these three behave. One Vendor makes it a rename.

[ADR 0007](docs/adr/0007-the-vendor-adapter-interface.md) made every reported Capability three-valued,
`Yes`, `No` or `Unknown`, and the three Vendors are what fill the three values. LM Studio reports
`trained_for_tool_use`. Ollama answers `Yes` for what its `/api/tags` lists and `Unknown` for
everything else, and never `No`, because an Ollama below v0.30.2 lists no capabilities at all and a
discovery routine that read absent as `No` would hide every usable Model on that Host. llama-swap
answers `Unknown` for every Model it has not loaded, and it is the only one of the three with a real
load and unload story, which is what `Load` and `Unload` exist for. **Drop any one of them and a
value in that design stops being exercised.** Drop llama-swap in particular and nothing in v1 is
`Unknown` on a current Vendor.

**Corrected 2026-08-31, and this line is the correction.** An earlier version of this paragraph said
Ollama answers `Unknown` for everything because its `/v1/models` carries no capability field. The
endpoint claim is true and the conclusion did not follow, because discovery does not use that
endpoint. ADR 0007's own version matrix already said a single `/api/tags` call answers every field on
Ollama v0.30.2 and later, and `docs/research/captures/ollama-vendor/` is that body from a running
v0.33.2. What survives unchanged is the rule the three values exist for: **absent is `Unknown` and
never `No`.**

They also disagree on things a careless Daemon would average. `usage.reasoning` was 51 under LM
Studio and 0 under the other two for the same thinking output, so zero means not reported rather than
none. llama-swap charged 24 input tokens against a 2986-token cached prefix where the others charged
about 3010. Totals agree, the split does not. Those traps are only visible with more than one Vendor
in front of you, and they are the reason the Session records the Vendor and Model that served it
rather than the ones it was configured with.

The cost is two adapter files beside the first. The research in
[#3](https://github.com/VictorJohnOkoh/Dispatch/issues/3) covers all three, the captures in
`docs/research/captures/pi-vendors/` exercise all three, and
[ADR 0010](docs/adr/0010-go-package-structure-and-seams.md)'s tree already names `ollama.go`,
`lmstudio.go` and `llamaswap.go` as files in one package rather than three packages.

All three are proven on a real Host over SSH, which is worth stating because an earlier version of
the research claimed otherwise about LM Studio. The `pi-vendors` pass drove all three at loopback on
the Host, and the OpenCode run records `gate1_tool_call_on_host: pass` against LM Studio over SSH with
no TTY. That finding is corrected in `docs/research/remote-host-findings.md` rather than left to be
rediscovered. The one real difference is that LM Studio's server is started once from its own
application instead of from a CLI, which is Host setup and not something the Daemon ever does: a
Vendor is reachable exactly when a call to it succeeds.

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
10 seconds. The Hub adds a `host` field to each and sends its own keepalive on the same beat.

**The Client's leg has a seventh frame the Daemon never sends.** `host` carries Host State, and it
has to be a frame rather than an Event because every Event carries a Session id and a Host that is
down has no Session to carry one. It is the only thing the Hub originates instead of forwarding, and
it is the whole reason the Client can draw the pair on a Session row.

`id:` appears only where a frame advances the cursor, and the Hub rewrites it to the whole compound
cursor, because an `EventSource` keeps only one `Last-Event-ID`.

**A reader names the log its cursor came from in a `Dispatch-Log` header**, beside `Last-Event-ID` on
`GET /v1/events`. ADR 0009 fixed a Daemon's cursor at one number and gave the identity to `hello`,
which arrives after the Daemon has already begun replaying, so the identity travels with the request
instead. The header is optional: a reader that has never connected holds no identity, sends none, and
is served. This is additive, so the protocol version does not move.

The log is SQLite with write-ahead logging, one `events` table whose five columns are the wire shape,
one index. An Event is committed before it is sent. An open message flushes every 4 KiB, so a crash
keeps what it had. Nothing is ever deleted, so a cursor is never too old, and a `resync` means the
log's identity changed or the cursor is above anything ever allocated.

## The package tree

[ADR 0010](docs/adr/0010-go-package-structure-and-seams.md) owns it. Fourteen packages in five
levels, one module, one binary, and the rule that proves the graph is acyclic: **no package imports
another at its own level.** Ten of the fourteen are named after a `CONTEXT.md` term. Go 1.25 is the
floor. ADR 0010 set it at 1.24 because ADR 0007's own test example calls `t.Context()`, and
`modernc.org/sqlite` raised it: the current driver needs 1.25, and M2 takes the current driver rather
than pinning an old one to hold a floor nothing else needs.

One addition to that tree, argued in the corrections below: `daemon/supervise.go` splits into
`supervise_windows.go` and `supervise_unix.go`, because killing a process tree is the one thing in
this design with no portable primitive. Nothing else in fourteen packages needs a build tag.

## The Client shape

Settled by the prototype on `prototype/multi-host-client`. The artefact is the argument and it stays
off `main`.

- **The primary object is one Session, drawn in full.** Its Events fill the screen and every other
  Session is a rail. The competing answer, one list of every Session on every Host, reads as a thing
  to administer.
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

**The one other dependency decision, which no ADR ever made: the SQLite driver is
`modernc.org/sqlite`.** ADR 0009 chose SQLite, WAL and the schema and stopped short of what opens the
database, and M2 cannot start without an answer. Pure Go matters more here than speed: it needs no C
toolchain, so `go build` works on any machine and the binary stays one self-contained file, which is
exactly ADR 0011's argument that one file is the whole deployment. The cgo binding is faster and would
put a working gcc between you and every Host you deploy to, in a repo that has already lost work to
environment differences four times. It uses `database/sql`, which is the interface worth learning. The
cost is a large machine-translated codebase you will never read.

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

Nine milestones. Each one ends at something you can watch, because a milestone that ends at "the
package compiles" cannot be wrong in a way anyone notices.

The order follows [ADR 0010](docs/adr/0010-go-package-structure-and-seams.md)'s levels with three
deliberate departures, each argued where it happens: `eventlog` is split, the Hub comes before the
second Harness, and SSH comes early rather than last. All three answer the same question, which is
how soon something runs that you would actually use.

**M0. The module and the two CI checks.** `go.mod`, `cmd/dispatch/main.go` with the role switch, and
the two `go list` checks in CI on the first day. They are cheap now and they are the fence the single
binary trades away. The module path is `github.com/VictorJohnOkoh/Dispatch`, matching the
repository's own capitalisation, because a Go module path is case sensitive to the proxy.
*Watch:* `dispatch hub` and `dispatch daemon` each print their role and exit, and CI fails a commit
that makes `internal/daemon` import `internal/hub`.

**M1. The pure packages.** `event`, `protocol`, `session`, `workspace`, `admission`. Tier-one tests,
no I/O beyond `t.TempDir()` with symlinks in it. These are the test-first packages, and
`session.Fold` ships here rather than with the Client: ADR 0008's state table has a column for what
the Daemon does differently per state, so the Daemon folds in order to refuse a Prompt on a
`Starting` Session. What defers to M6 is the JS twin and the shared JSON fixture, not the fold.
*Watch:* a slice of Events built by hand folds to each of the five states, and `workspace.Contain`
refuses a symlink that points outside the Root.

**M2. The Event log, write and stream only.** `eventlog` against a real SQLite file, with `Append`,
`AppendText` and `Subscribe`. WAL, the gapless `Seq` allocated inside the write transaction, the
4 KiB flush, open-message tracking, and the subscriber fan-out that drops rather than blocks.

**`Cursor`, `Replay`, `Sweep` and `log_id` defer to M4**, and this split is what pulls the first
running Daemon forward by weeks. Nothing is faked to do it. It is the real SQLite log with three of
its six methods, and the three that are missing serve reattach and restart, which nothing tests until
M4. ADR 0010 rejected an in-memory `eventlog` implementation, and this is not one.
*Watch:* a killed writer leaves a partial message in the log, and every byte that was flushed is
still there.

**M3. The vertical slice, and the first thing that runs.** `daemon` with the passthrough Harness and
the Ollama adapter, driven by `curl` on localhost. Admission, the Session registry, the ten
endpoints, the SSE stream, the Vendor poll. No Hub, no browser and no process supervision, because
passthrough spawns nothing.

The Daemon calls `vendors.Load` during `Starting`, before `SessionReady`. Record the `vendors`
fixtures here as well: Ollama is already running for this milestone, so finding R8 becomes a `curl`
and a save rather than the capture-script problem it has been.
*Watch:* `curl` starts a Session, submits a Prompt, and reads the assistant's text arriving as Deltas
on `GET /v1/events`.

**M4. The Hub, a browser, and a real machine.** `hub`, `hostset`, `web` reduced to one page, and the
rest of `eventlog`: `Cursor`, `Replay`, `Sweep`, `log_id` and resync detection. Cursor split and
merge, and `id:` rewriting.

**`HostDialer` gets its real `x/crypto/ssh` implementation here, not at the end.** The `net.Pipe`
dialer stays for the in-process test, so nothing is lost. What is gained is that every milestone
after this one runs against a real Host, which is the actual target, and the class of failure this
repo has lost work to four separate times is found now instead of last. Manual install happens here:
copy the binary and `daemon.json` to the Host and start it by hand.
*Watch:* a browser shows a Session's transcript streaming from a machine in another room. Close the
tab mid-Prompt, reopen it, and the whole transcript comes back including the message that was still
arriving when the tab closed.

**M5. A real Harness process.** `harness/acp.go` for OpenCode, plus `supervise.go`, `ledger.go` and
`transcript.go`, running on the Host. The shutdown ladder, the stderr drain, the two synthesis
triggers, and the Approval Policy becoming live, because passthrough never had one.

**Killing the process tree is per platform and it is not one line**, which is the first correction
below. The stub Harness is the test binary re-executing itself, so all of this is testable with no
GPU and no OpenCode installed.
*Watch:* a Session runs a tool call that waits on the Approval Policy, and a stop leaves nothing
behind on the Host, checked from a shell there rather than from the Client.

**M6. The full Client shape.** The rail, the read-only Hosts view, the four-step wizard, the toast,
the pair on a Session row, and the fold in JS against M1's fixture. `web` renders the first paint on
the server and JS applies live frames after it.
*Watch:* an `ApprovalRequested` on a Session that is not on screen reaches you anyway.

**M7. Host State.** The two keepalives, backoff that resets only after 60s continuously `Ready`, the
Handshake, and all four states with `Down` carrying its cause. M4 gave the Hub a connection; this
milestone makes its failures correct.
*Watch:* pull the network cable on a Host mid-Session and the Client dims that Host inside ~25
seconds while every other Host keeps working.

**M8. The second Harness and the other two Vendors.** `harness/pi.go`, `vendors/lmstudio.go` and
`vendors/llamaswap.go`, all against fixtures already in this repo, plus the Pi gate work described
under holes. They are last on purpose: they are the proof the two abstractions are abstractions, and
that proof is only worth anything once there is something for them to plug into.
*Watch:* the same Session and the same transcript rendering, with one Harness swapped in the wizard.
And one Model list showing `Yes` from LM Studio, `Unknown` from Ollama, and `Unknown` from llama-swap
until the Model is resident.

**If v1 has to shrink**, drop M8, in this order and stopping as soon as it fits: the Pi adapter, then
the llama-swap adapter, then the LM Studio adapter. That is the whole list, and the honesty is the
point. Note what dropping either Vendor costs, since it is not nothing: llama-swap takes `Load` and
`Unload` with it, and LM Studio takes the only `Yes` any Capability ever returns. M0 to M7 is one
Host, one Harness and one Vendor running end to end, which is the smallest thing that is the system
rather than a demonstration of it, so nothing inside it can go without breaking one of the thirteen
behaviours below. In particular do not drop M5, because a Harness the Daemon cannot kill is the
failure this whole design exists to prevent.

### Where test-first applies, and where it does not

Agreed before any code is written, because "write tests" is not a plan and the answer differs by
package.

| | packages | why |
| --- | --- | --- |
| **Test-first** | `session.Fold` with its JSON fixture, `workspace.Contain`, `admission`, `protocol`'s cursor parse and format | pure, and a test is cheaper than a REPL. For these the test genuinely is the specification |
| **Rig first, assert after** | `eventlog`, `supervise` | build the real SQLite temp-file rig and the re-exec stub Harness first, then write assertions against what actually happened. Nobody can predict what a killed WAL writer leaves behind, and guessing produces a test that encodes the guess |
| **Tests after, from fixtures** | `vendors`, `harness` | a recorded `http.RoundTripper` and this repo's own `*-frames.jsonl`. The fixtures are the specification, and for `harness` they already exist |
| **No unit tests** | `web`, `hub`, `cmd` | covered by the one in-process Hub-to-Daemon test over `net.Pipe`. Unit-testing a composition root tests the wiring diagram |

## Decided here: what proves v1 is done

Not a test suite. Thirteen behaviours you can watch, on real machines. v1 is done when all thirteen
hold.

1. **Start a Session on a sleeping Host and get a useful error.** The Host reads `Down{unreachable}`,
   the Client names it, and the start button on that Host is disabled rather than failing silently.
2. **Close the laptop mid-Session and reattach with full history.** Every Event replays, including
   the assistant message that was still arriving when the lid closed, and it replays whole.
3. **Approve a tool call from a phone over a tunnel.** The `ApprovalRequested` arrives, the decision
   goes back, and the Tool Call ends with an outcome that says a human decided it.
4. **The refusal is honest.** Set `execute` to `refuse`, ask the Session to run a command, and see
   `ToolCallEnded{refused}` written from the Daemon's own `ApprovalDecided`, never from the Harness.
5. **Kill the Daemon under a live Session and restart it.** The boot sweep ends that Session `lost`,
   its transcript is intact and readable, and the Client offers a new Session rather than a resume.
6. **Kill a Harness that will not die.** Stop runs the ladder and the whole process tree is gone,
   checked from a shell on the Host rather than from the Client. On Windows this means checking that
   the Harness's own children went with it, which is the part a naive kill gets wrong.
7. **Start a second Session on a busy Host.** The refusal names the Session holding the slot, and one
   click stops that one and starts this one.
8. **Run the same prompt on two Hosts at once.** Both stream into one merged Client stream, neither
   starves the other, and the commands still answer while both are streaming.
9. **Point a Session at a directory outside the Workspace Root.** It is refused before the Session
   exists. Then have the Harness delegate a write outside it, and see that refused too.
10. **Swap the Harness and change nothing else.** The same Model, the same prompt, the same Host, and
    the transcript renders the same way for both OpenCode and Pi.
11. **See all three Capability values in one Model list.** LM Studio answers `Yes` from
    `trained_for_tool_use`, Ollama answers `Yes` for what `/api/tags` lists and `Unknown` for what it
    does not, and llama-swap answers `Unknown` until the Model is resident. The Client draws `Unknown` as an answer
    rather than as a blank, and every Session runs anyway.
12. **Break the Handshake on purpose.** Run an old Daemon against a new Hub, see `Incompatible`, and
    confirm from the Daemon's log that the Hub stopped retrying.
13. **Put a Daemon on a Host that has never had one.** Copy the binary and `daemon.json`, start it,
    and have the Hub reach it, following only the written instructions and typing nothing from
    memory. Manual install is a frozen v1 feature with nothing else checking it, and a silent `scp`
    that landed nowhere has already cost this repo two runs.

Three of these get skipped unless they are called out, so they are called out. Number 6 needs a shell
on the Host, not a green test. Number 12 needs two builds, which is an inconvenience rather than a
difficulty, and it is the only check that the Handshake is real. Number 13 fails if the install
instructions do not exist, which is the point of writing it as a behaviour rather than as a task.

## Decided here: four corrections to earlier ADRs

Each of these contradicts or completes something an ADR already says. They live here, named, which is
how this repo has handled every prior correction: ADR 0009 amended ADR 0005's cursor sentence, and
ADR 0010 corrected ADR 0007's package name. The index in `CLAUDE.md` carries a pointer on each
affected row.

**ADR 0008's shutdown ladder, step 6, is not portable as written.** It says "kill the process group,
not the process", and Windows has no POSIX process groups. `SysProcAttr.Setpgid` is Unix only, and
Windows' `CREATE_NEW_PROCESS_GROUP` changes only where a Ctrl+Break is delivered: it does not make
children die. `os.Process.Kill` calls `TerminateProcess` on one handle, so descendants survive as
orphans. This matters here rather than in theory, because OpenCode resolves to a package binary that
spawns its own child, so a naive kill leaves the real work running with the Model still resident
while admission believes the slot is free.

**The fix is a Job Object on Windows**, created at spawn with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`
and the Harness assigned to it, so closing the handle takes the whole tree including processes
spawned later. `golang.org/x/sys/windows` covers it in about thirty lines. ADR 0010's tree gains two
files, `supervise_windows.go` and `supervise_unix.go`, and the ladder's step 6 stays word for word,
because "the process group" was always the right idea and only the primitive differs. This does not
reopen ADR 0008's rejection of Job Objects for isolation: process-tree lifetime is not sandboxing,
and nothing here limits what the Harness may do.

**ADR 0007's `Load` and `Unload` had no caller anywhere in the design.** Nothing in ADRs 0008, 0009
or 0010 calls either, and the only candidate was a VRAM-aware admission policy that is out of v1. Left
alone that is two interface methods no v1 code path reaches.

**The Daemon calls `Load` during `Starting`, before `SessionReady`.** ADR 0008 already assumed this
without saying so: it justifies `Starting` as a state on the grounds that "a 29.70s cold Model load is
the reason it is worth the Event", and a load only lands in `Starting` if the Daemon triggers it,
since all three Vendors otherwise load lazily on the first inference call and would put the stall in
`Working` instead. Calling `Load` also disables the Vendor's own evictor for the Session's life, which
is what makes `Idle` mean idle rather than "your next Prompt costs twenty seconds". **`Unload` stays
on the interface and v1 never calls it**, reserved for the VRAM policy, and that is a decision here
rather than something for a reviewer to find.

**ADR 0005 asked for transcript retention and no ticket picked it up.** Its Consequences say the raw
transcript "needs its own rotation and retention. It is not covered by #10's log retention." ADR 0009
then removed retention for the Event log and never took the other half, so the requirement fell
between two documents. It lands here.

**A transcript stops at 64 MB per Session and appends one line saying where it stopped.** A byte
counter and one threshold in `transcript.go`. No rotation, no config knob, no policy. The number is
reasoned rather than measured: one Pi tool call in `captures/pi-gate/gate-deny-raw.log` is 76 KB of
raw bytes, so 64 MB is roughly 840 tool calls and a normal Session never reaches it. The Event log's
promise that nothing is ever deleted is untouched, because a transcript is not the log: it is bytes in
a file that no program reads, and its only reader is a human debugging an Adapter.

**ADR 0010 left the first paint to this ticket and it is decided.** `web` keeps its `event` import and
renders the transcript, the rail and the Host cards on the server; JS applies live frames after that.
Serving an empty shell would remove the only import that makes the Hub aware Event Kinds exist, but it
costs a blank first paint and moves all rendering into JS rather than just the fold, which is the
shape the map rejected when it ruled out React. The cost of keeping the import is bounded: the Hub
still forwards payloads byte for byte, so an Event Kind it has never heard of still reaches the
browser and renders as a neutral row.

## The map's Notes, re-read

Every Note on the map, confirmed or corrected against the research that landed after it was written.
The Notes are grouped below rather than listed in the map's order, and nothing is dropped: a Note that
still holds is named here even when it needed no thought.

**Confirmed unchanged.** The **Domain** note, a Go program in two roles, is what this whole spec
describes. Learning first and portfolio second. Data structures and algorithms are not a goal. Go, and
a dumb browser Client with no React. One binary and two roles, with Daemons that never learn about
their peers, now [ADR 0011](docs/adr/0011-one-binary-two-roles.md). Control Plane and Data Plane stay
separate, now [ADR 0012](docs/adr/0012-the-same-host-invariant.md). The Vendor abstraction covers
discovery, capability and health but not inference. Direct prompting is a passthrough Harness rather
than a second code path, and ADR 0005 confirmed it costs nothing: it is a strict subset of the Kinds
with nothing bent, and the ones it never writes are the five about tools, which it does not have.
Events are normalised and typed, the Client never sees raw Harness output, and the log is transport,
replay buffer and history at once. Daemons bind loopback, reach is an SSH tunnel with Tailscale as the
later upgrade, and there is no self-rolled internet-facing auth. Manual Daemon install now and
Client-driven later, which is the other half of the design-the-seam note and is unchanged.

**Confirmed, and it was the one most at risk.** [ADR 0001](docs/adr/0001-resident-daemon-on-host.md),
the resident Daemon, was decided while charting and before any Host existed. Everything since has
leaned on it and none of it broke. The Event log is only a replay buffer because something long-lived
owns it, and behaviours 2 and 5 above are the ones that would have failed under the SSH options.

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

*"One Vendor."* Corrected to three, argued above. Each of the three fills a different one of ADR
0007's three Capability values, so one Vendor leaves that design with nothing to exercise it and two
still leave one value unreached.

*Passthrough plus one real Harness.* Corrected to two real ones, argued above.

*One arithmetic correction, since it is the kind that gets carried silently.* ADR 0005 records
passthrough as nine of fourteen Kinds. ADR 0008 added `SessionReady` and `DaemonStarted`, and
`SessionReady` is the only trigger out of `Starting`, so a passthrough Session writes it too. Ten of
sixteen, and the subset argument is untouched.

**Still true, and not an architecture claim.** The **skills** note names `/grilling` and
`/domain-modeling` as the default reading for a session on this map, with `/codebase-design` for
interface tickets. That was a working practice for charting rather than a decision about the system,
and it does not carry into the build, where the reading is `CONTEXT.md`, this spec, and the one ADR
that owns whatever is being changed.

**Prior art, re-read and still the closest analogue.** T3 Code solves the Harness half the same way
this design does: an adapter per agent CLI, an event-sourced core, a headless server, loopback plus
tunnel reach. Four independent arrivals at the same shape is the strongest outside evidence any of
these decisions have. It deliberately does not solve the Vendor half, which is the half this project
exists for, so it confirms the Harness side and says nothing about ADR 0007. Worth re-reading before
M5, where the first real Harness process lands, and worth ignoring during M8.

**Superseded, and kept visible.** The Note that Hermes is best driven as a local HTTP and SSE server
was wrong: that surface does not exist in Hermes v0.19.0. The lesson generalises and is worth keeping
where people read it. This vendor's docs describe endpoints it does not ship, so a documentation-only
claim needs an empirical check before anything is designed against it.

## Holes carried into the build

Named, so that nobody discovers them by being surprised.

- **Pi's Gate needs an extension we write, and it needs one capture.** The mechanism itself is proven:
  `captures/pi-gate/` holds an allow run and a deny run, and the file state settles it rather than the
  wording, since the target was deleted on `Yes` and survived on `No`. What those captures do not show
  is a Gate in ADR 0008's sense. Pi's bundled `permission-gate.ts` fires only on `bash`, and only for
  three regexes (`rm -rf`, `sudo`, `chmod 777`), so most `execute` calls sail past it, and it never
  announces itself. The Daemon therefore ships its own extension, and what that extension must add is
  coverage of every `toolKind` plus an announcement before `Start` returns. The one capture still owed
  is that the announcement arrives. If it cannot, the Adapter declares no Gates, every slot is forced
  to `auto`, and the Client draws a Session with no gates anywhere, which ADR 0008 already called the
  correct outcome because it is what is true.
- **The Vendor fixtures do not exist.** Finding R8. Tier-two tests for `vendors` need recorded bodies
  for a caller-supplied `http.RoundTripper`, and the capture that should have produced them wrote
  nothing while reporting `HTTP 200`. It is no longer the problem it was: M3 has Ollama running
  anyway, so recording them is a `curl` and a save, and M3 is where to do it.
- **No OpenCode refusal has ever been tested.** Every captured permission request was answered with
  allow, so what `reject_once` does is an assumption. Cheap to settle during M5 and expensive to
  discover during M6.
- **The Hermes launcher-chain failure is unexplained.** Eight causes were ruled out by measurement and
  the cause is still open. It blocks nothing, and it is the reason the Daemon must try to run a
  Harness rather than ask whether one is installed.
- **The fold is written twice**, in `session` and in the Client's JS, sharing one JSON fixture. It is
  the only duplicated logic in the design, and it is duplicated because the Client applies live Events
  itself.
- **A recorded frame proves what a Harness said in August 2026 and nothing about September.** Every
  fixture in this repo is a snapshot, so re-capturing is a recurring task rather than a one-off. This
  is the risk that grows with the calendar: OpenCode lands at M5 and Pi at M8, and neither is a
  program this project controls.
- **The Host has to be standing from M4 onward**, which is earlier than the original order needed it.
  If it is down, M4 still runs on the `net.Pipe` dialer and the real dial waits, but every milestone
  after it assumes a reachable machine.
