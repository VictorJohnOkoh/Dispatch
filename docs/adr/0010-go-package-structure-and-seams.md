# Four leaf packages, two roles in one binary, and a Host id the Daemon cannot import

Fourteen packages. One Go module, one binary, and a role chosen by `argv[1]`. Four packages import
nothing else in the project, six import only those four, and the rest stack in three thin layers above
them. No package imports another at its own level, which is the whole proof that the graph is acyclic
and is checkable with one `go list` command.

The invariant the tree exists to hold is `CONTEXT.md`'s: a Daemon knows only its own Host and never
learns about its peers. Two fences make that a type error and one makes it a compile error. The
remaining hole is named at the end rather than hidden.

## What the earlier ADRs already decided

Five packages were named before this ticket opened, and this ADR adopts all five rather than reopening
them.

| package | named by | what it holds |
| --- | --- | --- |
| `vendor` | ADR 0007 | `Adapter`, `Endpoint`, `Model`, `Capabilities`, `Frame`, `ReadStream`. A leaf: "`vendor` is a leaf, `harness` imports `vendor` and `event`, and nothing imports `harness`" |
| `harness` | ADR 0006 | `Adapter`, `Run`, `Sink`, `SessionSpec`, `Pipes`, `Files`. The process supervisor, the ledger of open Tool Calls and the transcript writer sit outside it |
| `session` | ADR 0008 | `State` and the fold that derives it |
| `admission` | ADR 0008 | `Policy`, `Request`, `Refusal`, and `SingleSession` |
| `workspace` | ADR 0008 | `Root` and its one method, `Contain` |

ADR 0009 named two more without naming their packages: the frame types and the endpoint set are shared
between the roles, and "the Host id belongs to the Hub alone and never appears in the Daemon's types".
This ADR turns that sentence into a directory.

One of ADR 0007's claims needs a correction rather than an adoption. "Nothing imports `harness`" is
wrong once the Daemon exists, because the Daemon holds an `Adapter` per Harness and calls `Start`. What
the sentence was reaching for is true and is stated here instead: **nothing at `harness`'s own level
imports `harness`**. The Daemon is a level up, and a level up is the only place that may.

## The tree

```
dispatch/
  go.mod                          module github.com/VictorJohnOkoh/dispatch, go 1.24
  cmd/
    dispatch/
      main.go                     role switch, config load, logger, signals, shutdown
  internal/
    event/                        the envelope, the sixteen Kinds, ToolKind, Outcome,
                                  StopReason, Usage, SessionID. Types and nothing else
    protocol/                     the ten paths, the seven frame types, the wire envelope,
                                  the Handshake, cursor parse and format, the status codes
    vendor/                       Adapter, Endpoint, Model, Capabilities, ReadStream, and
                                  ollama.go, lmstudio.go, llamaswap.go
    harness/                      Adapter, Run, Sink, SessionSpec, Spawner, Pipes, Files,
                                  and pi.go, acp.go, passthrough.go
    workspace/                    Root.Contain
    admission/                    Policy, Request, Refusal, SingleSession
    session/                      State and Fold
    eventlog/                     the SQLite log: Append, AppendText, Cursor, Replay,
                                  Subscribe, Sweep. WAL, the Seq counter, the 4 KiB flush,
                                  open-message tracking, log_id
    daemon/                       the Host role
      daemon.go                   the Session registry and the start path
      http.go                     the ten endpoints
      supervise.go                spawn, stderr drain, the shutdown ladder, process groups
      sink.go                     harness.Sink over eventlog, and the Delta fan-out
      ledger.go                   open Tool Calls and the two synthesis triggers
      transcript.go               raw Harness bytes, beside the log
      vendors.go                  the Vendor poll, the catalogue cache, the vendors frame
    hub/                          the multi-Host role
      internal/
        hostset/                  HostID, the Host table, HostDialer, Host State, backoff
        web/                      render.go, the first paint, the templates, the CSS and
                                  the JS, all embedded
      hub.go                      the composition of hostset and web
      stream.go                   fan-in, id: rewriting, cursor split and merge
      http.go                     one command handler and GET /v1/hosts
    config/                       config.Daemon, config.Hub, and the loader
```

Ten of the fourteen packages are named after a `CONTEXT.md` term, and none of them adds one. The four
that are not are `cmd/dispatch`, `protocol`, `web` and `config`, and each of those names a mechanism
rather than a domain idea. That is the check that the tree follows the domain instead of inventing a
second vocabulary beside it.

Go 1.24 is the floor, not a preference. ADR 0007's own test example calls `t.Context()`, which arrived
in 1.24.

### The adapters are files, not sub-packages

`vendor/ollama.go` rather than `vendor/ollama/`. Three Vendor adapters share `Endpoint`, `Model`,
`Capabilities` and `ReadStream`, and three Harness adapters share `Sink`, `SessionSpec` and `Pipes`. If
each adapter were its own package, every one of those types would have to be exported across an import
that buys one thing: a rule stopping the Ollama adapter importing the llama-swap adapter. Nothing wants
to do that, and no reviewer would miss it if something tried.

The rule that decides it: **split a package to stop an import somebody would otherwise make.** Splitting
to make a directory listing look tidy is the pass-through failure mode with a filesystem instead of a
function.

### One `cmd`, because one binary

`cmd/dispatch/main.go`, with `dispatch daemon` and `dispatch hub` as subcommands. Not `cmd/daemon/` and
`cmd/hub/`, because two `cmd` directories are two binaries and the map settled on one. The Client's
templates, CSS and JS are `go:embed`ed from `internal/hub/internal/web/`, so one file is the whole
deployment on a Host and the whole deployment on the machine the browser talks to.

`main.go` does five things and no more: read `argv[1]`, load the one config file that role uses, build a
`*slog.Logger`, construct the role, and wire `SIGINT` to its shutdown. Everything it constructs takes
plain values, which is the subject of the configuration section below.

## What is shared, and the three fences

Shared between the roles: `protocol`, and `event` and `session` for the Hub's first paint. That is all.

| package | Daemon | Hub |
| --- | --- | --- |
| `protocol` | serves it | speaks it, in both directions |
| `event` | writes it | reads it, in `web` only |
| `session` | folds it | folds it, in `web` only |
| `vendor`, `harness`, `workspace`, `admission`, `eventlog` | yes | never |
| `hub/internal/hostset` | cannot | yes |

The Hub imports neither `vendor` nor `harness` nor `eventlog`. It holds nothing durable, per ADR 0009,
and it never speaks to a Vendor or spawns a process. So the two role packages overlap on the wire and
almost nowhere else, which is what makes a second Client cheap: a TUI is a different `web`, against the
same `protocol`.

### Fence one: `config.Daemon` has no peers field

```go
package config

// Daemon is one Host's own configuration. It has no field naming another Host, and
// that absence is the design rather than an omission.
type Daemon struct {
	Listen        string           // loopback only
	WorkspaceRoot string
	LogPath       string
	Vendors       []VendorProfile  // this Host's Vendors, on this Host's loopback
	Harnesses     []HarnessProfile // an absolute executable path each, per ADR 0006
	Policy        [event.NumToolKinds]event.Decision // the Approval Policy defaults
}

// Hub is the only type in the system that holds more than one Host.
type Hub struct {
	Listen string
	Hosts  []HostProfile // name, SSH target, key path, the Daemon's loopback port
}
```

A Daemon reads a `Daemon`. There is nowhere in that struct to put a peer, so a peer cannot arrive by
configuration. This is the strongest of the three fences because it needs no discipline at all: the
failure is `unknown field`, at startup, in the one place a human would put the mistake.

### Fence two: no type the Daemon serves has a Host field

ADR 0009 already fixed this and this ADR only names where it lives. On the Daemon's leg a command path
is `/v1/sessions/{id}/prompts` with no `{host}` segment, and a frame body has no `host` field. Those
shapes are `protocol`'s, and `protocol` has two envelopes for the same reason:

```go
package protocol

// Event is one Event on the wire. Payload is never unmarshalled by anything between
// the Daemon that wrote it and the reader that draws it, which is what lets the Hub
// forward a Kind it has never heard of.
type Event struct {
	Seq     uint64          `json:"seq"`
	Session string          `json:"session"`
	At      int64           `json:"at"` // Unix microseconds
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

// HostEvent is the Client's leg. The Hub makes one from an Event and a Host id, and
// no Daemon ever constructs one or receives one.
type HostEvent struct {
	Event
	Host string `json:"host"`
}
```

`encoding/json` drops a field the target struct does not have, so a Hub that tried to tell a Daemon
about a peer would be sending bytes into a type with no room for them. The Daemon does not defend
against this. It is simply unable to receive it.

**The wire envelope is not `event.Event`**, and that is deliberate rather than duplication.
`event.Event` carries `Payload any` and a typed `Kind`, and it exists on the write path and in the
fold. `protocol.Event` carries `json.RawMessage` and a string, and it exists on the read path. They
meet in the SQLite row, whose five columns are already the wire shape. So a replay reads rows and
writes them out with no JSON parsing at all, and so does the Hub. The payload is parsed in exactly two
places in the whole system, three if the Hub renders a first paint: where the Daemon writes it, where
the browser draws it, and in `web`.

### Fence three: `hub/internal/hostset` is a compile error from the Daemon's half

`HostID`, the Host table, `HostDialer`, Host State and the backoff all live at
`internal/hub/internal/hostset`. Go's `internal` rule is that a package under `a/internal/b` is
importable only from the tree rooted at `a`, so this one is importable only from `internal/hub/...`.
`internal/daemon` importing it does not fail review. It fails `go build`.

That is one nested directory bought with one idea, and it is the only place in the tree where nesting
`internal` inside `internal` earns anything.

### What stays impolite

`internal/daemon` can still import `internal/hub`, because the two share a parent `internal` and Go
cannot stop that. Doing so would gain a Daemon nothing, since the only peer list in the program is a
`config.Hub` value the Daemon never loads and `hostset` is still shut. But the import compiles, and an
ADR that claimed otherwise would be wrong.

The closing check is one line, and it belongs in CI rather than in a document:

```
go list -deps ./internal/daemon/... | grep -q /internal/hub && exit 1
```

So: two fences are type errors, one is a compile error, and one review-level rule is enforced by a
grep. The honest summary is that a Daemon cannot **learn** about a peer structurally, and cannot
**mention** one without a build break in the sub-tree that holds the word.

## Depth, package by package

The judgement is leverage at the interface: how much behaviour a caller reaches per unit of interface
they have to learn.

| package | interface | what it hides | verdict |
| --- | --- | --- | --- |
| `eventlog` | 6 methods | SQLite, WAL, gapless `Seq` inside the insert, the 4 KiB flush, which Events are open, the cursor's lag, `log_id`, resync detection, subscriber fan-out | deep, the deepest here |
| `workspace` | 1 method | resolve before compare, the walk to the deepest existing ancestor, `EvalSymlinks`, the `..` check on the remainder, case folding on Windows | deep |
| `vendor` | 5 methods | three native APIs, llama-swap's per-Model `/props` walk, LM Studio's Auto-Evict, the five normalisation rules, Ollama's unframed mid-stream error | deep |
| `harness` | 2 methods, plus 3 on `Run` | two wire protocols, blocking gates, correlation repair, tool-name to `ToolKind` | deep |
| `hostset` | 3 methods | SSH dialling, `direct-tcpip`, two keepalives, the Handshake, backoff with a 60s reset, the four Host States | deep |
| `daemon` | `New` and an `http.Handler` | the whole Host role | deep |
| `web` | `New` and an `http.Handler` | the first paint, sixteen Kinds drawn, the pair rule #11 found | deep |
| `session` | 1 type, 1 function | a five-state fold over six Kinds | shallow, kept |
| `admission` | 1 method | 3 lines | shallow, kept on a written bet |
| `event` | 16 payload structs | nothing | shallow, kept |
| `protocol` | 2 envelopes, 7 frames, 10 paths | nothing | shallow, kept |
| `config` | 2 structs and `Load` | file reading and validation | shallow, kept |
| `hub` | `New` and an `http.Handler` | nothing of its own | shallow, and correct |

**`hub` is shallow and that is the right answer, not a defect.** It is a composition root for a role,
the same job `cmd/dispatch` does for the process. The Hub has exactly two faces, the Daemons and the
browser, and they are `hostset` and `web`. Neither imports the other. They share `protocol` and nothing
else, and `hub.go` is the few hundred lines that join them. Every deep thing the Hub does lives in one
of those two packages, so the deletion test on `hub` itself moves a wiring function, and the deletion
test on `hostset` puts SSH dialling in a browser.

The Daemon is deliberately not split the same way, and the asymmetry has a reason. The Hub has two
faces. The Daemon has one inward face, HTTP, and four outward ones: processes, Vendors, the log and the
filesystem. Splitting by outward face gives four packages that all need the Session registry, so they
would import each other or import a fifth package holding it, which is the shallow layer this ADR spent
a section deleting.

Four of the six shallow ones are vocabulary rather than modules. Apply the deletion test to `event`:
delete it and the sixteen payload structs reappear in `eventlog`, `daemon`, `session` and `web`,
four copies that must agree. A package whose whole job is that four things agree is doing its job even
with no functions in it.

`session` is the interesting one, because it could just as easily live inside `daemon`. It does not,
and the deletion test says why: `web` folds Events too, to draw a Session row on the first paint.
If the fold lived in `daemon`, the Hub would import the Daemon to render a list, and the roles would
stop being two things.

**`admission` is genuinely shallow today and I am keeping it anyway.** One method, one implementation,
three lines of body. Under the usual rule that one adapter is a hypothetical seam, it should not exist.
ADR 0008 bought it against a named future: a count limit reads `len(req.Live)` and a VRAM policy calls
`req.Vendor.Catalogue` and `req.Vendor.Resident`, and both fit `Request` with no new field. That is a
bet, it is written down with the evidence for it, and if neither policy is ever written then
`admission` was a directory that held an `if`. Naming it as a bet is better than calling it deep.

## The pass-through hunt

Pass-through layers that exist only to forward calls are the failure mode this ticket named. Five were
found in the first draft of the tree and four are gone.

**A `store` or `repository` interface over `eventlog`.** Deleted. One implementation, and the real one
opens a temp file in a millisecond, so a fake would be slower to write, less true, and would still need
the real one tested behind it. There is no storage interface anywhere in this design.

**A `daemon.Service` between the HTTP handlers and the Session registry.** Deleted. ADR 0009 made a
command an intention whose answer arrives as an Event, so a handler decodes a body, calls one method,
and writes a status code. A layer between those two lines forwards and does nothing else.

**A Vendor facade in the Daemon, wrapping `vendor.Adapter` with the cache and the admission rules.**
Deleted. ADR 0007 put the cache and the policy in the Daemon on purpose. `daemon/vendors.go` is the
place that holds them, and wrapping the Adapter as well would make the Daemon reach through itself.

**A `harness.Manager`.** Deleted. The Session registry is the manager, and a second noun for the same
map is a rename with a directory around it.

**The Hub's ten command handlers.** Kept, as one handler. This is the survivor and it is the strongest
evidence ADR 0009's one-protocol decision was right:

```go
// The Hub's whole command path. Ten Daemon endpoints, one handler.
func (h *Hub) command(w http.ResponseWriter, r *http.Request) {
	host, rest, ok := splitHostPrefix(r.URL.Path) // /v1/hosts/{host}/… → {host}, /v1/…
	...
}
```

Adding an eleventh endpoint to the Daemon costs zero lines in the Hub. If the Client's leg and the
Daemon's leg had been two protocols, that same eleventh endpoint would have cost a handler, a request
type, a response type and a test on the Hub as well. The one-protocol decision is what turns a
pass-through into a router, and a router that never parses a payload is not a shallow layer. It is the
component doing its whole job.

`hub` is thin on purpose and it is still deep, because the deletion test on it is brutal: delete the
Hub and SSH dialling, reconnection, backoff, the Handshake, cursor splitting, `id:` rewriting and Host
State reappear in the browser, where none of them can run.

## Dependency direction, and where the interfaces live

Go's usual answer is that an interface belongs with its consumer, so the implementer's package does not
import the consumer's. ADR 0006 and ADR 0007 both put `Adapter` with the implementer. That looks like a
contradiction and it is not, and the reason is worth stating as a rule because it decides three cases
in this tree.

> An interface goes with its consumer when the consumer is the only side that names it. It goes in a
> shared vocabulary package when both sides name its parameter types.

`harness.Adapter` takes a `SessionSpec` and a `Sink`, and returns a `Run` with a `Capabilities`. Every
one of those is named by the adapters and by the Daemon. So a package holding them exists whatever
happens to `Adapter`, and once it exists, moving `Adapter` to the Daemon buys nothing and costs a
second definition that has to be kept in step. Same argument for `vendor.Adapter` and for
`admission.Policy`.

`harness.Sink` is the consumer-side case in the same file. The Adapter calls it and the Daemon
implements it, so the caller's package holds it, and `harness` imports nothing of the Daemon's. That is
the rule working normally.

Where the rule changes the tree is in the two cases it deletes:

- **No `eventlog` interface**, as above. The consumer is the Daemon, the Daemon has one implementation,
  and the real one is testable.
- **`HostDialer` lives in `hostset`, with the Hub as its only consumer.** It has one production adapter
  and one test adapter, which under the usual rule is a hypothetical seam. ADR 0004 bought it for
  testing alone and this ADR upholds that, because the alternative is a Hub that can only be tested
  with a real SSH server, which means it is not tested. This is the one seam in the design justified by
  testing rather than by production variance, and it is named rather than dressed up as two adapters.

## The graph

Every import in the project, written out. A picture would hide the thing worth checking, which is that
no line ever names a package on its own level.

```
L0   event      →  (nothing)
     vendor     →  (nothing)
     workspace  →  (nothing)
     protocol   →  (nothing)

L1   harness    →  event, vendor
     admission  →  event, vendor
     session    →  event
     eventlog   →  event, protocol
     hostset    →  protocol
     config     →  event, vendor

L2   daemon     →  event, protocol, vendor, harness, workspace, admission, session, eventlog
     web        →  event, protocol, session

L3   hub        →  protocol, hostset, web

L4   cmd        →  config, daemon, hub
```

| level | packages | may import |
| --- | --- | --- |
| L0 | `event`, `vendor`, `workspace`, `protocol` | nothing in this module |
| L1 | `harness`, `admission`, `session`, `eventlog`, `hostset`, `config` | L0 |
| L2 | `daemon`, `web` | L0, L1 |
| L3 | `hub` | L0, L1, L2 |
| L4 | `cmd/dispatch` | anything |

**No package imports another at its own level.** A cycle needs at least one sideways or upward edge, and
there are none, so the graph is acyclic by construction rather than by inspection. It is also checkable
in one command, which matters more than the proof:

```
go list -deps ./internal/event/... ./internal/vendor/... \
              ./internal/workspace/... ./internal/protocol/... \
  | grep VictorJohnOkoh   # must print only the four themselves
```

Four edges are worth reading off the list because each says something.

`daemon` and `web` are both L2 and neither imports the other, which is the same fact as the roles being
two things. `hostset` is at L1 and imports `protocol`, because the Handshake and the `hello` frame are
protocol rather than Hub policy. `eventlog` imports both `event` and `protocol`, which is the
two-envelope split of the write path and the read path landing inside one package. And `config` sits at
L1 rather than L0 because it imports `event` and `vendor` for the types its fields hold, which is the
one direction config traffic is allowed to go.

The absences are louder than the edges. `hub` does not import `event`, `session`, `vendor`, `harness`,
`workspace`, `admission` or `eventlog`. `daemon` does not import `hostset`, and cannot. And nothing at
all imports `config` except `cmd`.

## Concurrency

Eight of the fourteen packages contain no goroutine, no channel and no mutex.

| package | goroutines | channels | shared state |
| --- | --- | --- | --- |
| `event` | none | none | none |
| `protocol` | none | none | none |
| `session` | none | none | none. `Fold` is a pure function over a slice |
| `workspace` | none | none | none. `Contain` touches the filesystem and returns a string |
| `admission` | none | none | none |
| `vendor` | none | none | none. Every call blocks. `ReadStream` is a loop on the caller's `io.Reader` |
| `config` | none | none | none |
| `harness` | one reader per `Run`, started by `Start` and joined by `Close` | none across the interface | none. One `Run` is one goroutine |
| `eventlog` | none | one buffered channel per subscriber | one mutex |
| `hostset` | one per Host: dial, handshake, read, backoff | none across the interface | one mutex on the Host table |
| `web` | none. Rendering is synchronous | none | none. Templates are parsed once at start and read-only after |
| `daemon` | one stderr drain and one process wait per Session, one Vendor poll, one per SSE subscriber | reads `eventlog`'s | two mutexes |
| `hub` | one per Client connection, one keepalive ticker | none | one mutex |
| `cmd/dispatch` | the signal handler | none | none |

The rule behind the table: **a package that hands out a channel does not own the goroutine at the other
end.** `eventlog` gives a subscriber a channel and never reads it. The Daemon's SSE handler owns the
goroutine that does. So the log has no goroutine to leak and the connection has no lock to hold.

### Five pieces of shared mutable state, and that is all of them

1. `eventlog`: the SQLite writer, the `Seq` counter, the open-message table and the subscriber list, all
   under one mutex, because `Seq` is allocated inside the insert and the open-message table decides the
   cursor.
2. `daemon`: the Session registry, `map[event.SessionID]*liveSession`.
3. `daemon`: the Vendor catalogue cache.
4. `hostset`: the Host table, each row carrying a state, a cursor and a `logId`.
5. `hub`: the Client subscriber list.

Everything else is per Session or per connection, reached by exactly one goroutine, and needs no lock.
A Session's ledger of open Tool Calls is the case worth stating, because it looks shared and is not:
`harness.Sink` is called only from that Session's one reader goroutine, so `daemon/sink.go` and
`daemon/ledger.go` hold no lock at all. The lock they would have needed is `eventlog`'s, one level down,
where the writes actually collide.

### A slow subscriber is dropped, not waited for

`eventlog.Append` sends to each subscriber's buffered channel without blocking, and drops a subscriber
whose buffer is full. That is safe only because of something the design already has: the dropped reader
reconnects with its cursor and replays. A slow browser recovers by the same mechanism as a broken
tunnel, so the alternative, letting one slow reader stall every write on the Host, buys nothing.

### Containers

Following the project's own rule, a map appears only where keys are made at runtime.

| container | shape | why |
| --- | --- | --- |
| Vendor slots | slice, fixed at Daemon start | config names them once and the set never changes |
| Harness adapters | slice, fixed at Daemon start | three of them, looked up by name, and a linear scan beats a hash |
| Host table | slice, fixed at Hub start | `config.Hub.Hosts`, and a user has a handful of machines |
| Approval Policy | `[event.NumToolKinds]Decision` | five slots, always all set |
| `Capabilities.Gates` | `[event.NumToolKinds]bool` | ADR 0006's, unchanged |
| subscribers | slice | appended and removed, and never large |
| Session registry | map | ids are minted while the Daemon runs |
| open Tool Calls | map | tool call ids come from the Harness |
| open messages | map keyed by `Seq` | opened and closed while the Daemon runs |

Three maps, and each is keyed by something that did not exist at startup.

## What is testable without a Host, a Vendor, a Harness or a GPU

Almost everything, and the map's **Testing strategy without a GPU** entry resolves into three tiers.

**Tier one, pure.** `event`, `protocol`, `session`, `workspace`, `admission`. No I/O beyond
`workspace`'s `t.TempDir()` with symlinks in it. `session.Fold` takes a slice of Events built by hand
and returns a state. `admission.SingleSession` takes a `Request` built by hand.

**Tier two, fixtures.** `vendor` through a caller-supplied `http.RoundTripper` answering from recorded
bodies, which ADR 0007 already specified and whose fixtures do not exist yet, that is finding R8.
`harness` through the scripted transport ADR 0006 specified, driven by this repo's own `*-frames.jsonl`.
`eventlog` against a real SQLite file in `t.TempDir()`, including the crash cases, because killing a
process is cheaper than faking WAL.

**Tier three, one process, no network.** The Hub, the Daemon and a stub Harness in a single `go test`
run. `HostDialer` returns a `net.Pipe` to an in-process Daemon rather than an SSH connection, so a test
drives a command from the Client's leg through the Hub's cursor arithmetic into the Daemon's log and
back out as a frame, with no SSH, no browser and no GPU.

The stub Harness is the part with a name worth writing down. It is not a checked-in binary and not a
fake `Run`: it is the test binary re-executing itself,
`exec.Command(os.Args[0], "-test.run=TestHarnessHelper")`, which is the standard library's own technique
for this. That gives a real OS process with a real process group, so the shutdown ladder, the kill after
the fixed wait, the stderr drain and the unprompted exit are all exercised against a process that
behaves exactly as badly as the test tells it to.

**What genuinely needs hardware**, stated rather than implied. Vendor `Load` timings, which nothing in
the design concludes anything from, per ADR 0007's rule that the Daemon never reads a Vendor's latency.
VRAM admission, which is not in v1. And the thing no fixture can cover: a Harness changing its output
format. A recorded frame proves the adapter reads what OpenCode said in August 2026 and proves nothing
about September. That is what keeps the captures in the repo useful and what makes re-capturing a task
rather than a one-off.

### The fold is written twice, and there is no way around it

`session.Fold` exists in Go, and the Client's JS applies live Events itself and so must fold too. Two
implementations of one rule is exactly the kind of duplication that drifts.

The mitigation is small and it is the only one available: the fold's cases are a JSON file, and both
test suites read it. One fixture, two implementations, and a new Event Kind that changes the fold breaks
both suites or neither. Anything more clever, generating the JS from the Go or running the fold on the
server for every frame, costs more than the five states are worth.

## Configuration enters at `cmd` and goes no deeper

One rule, no exceptions: **no package under `internal/` imports `internal/config`.**

`main.go` reads the file, validates it, and constructs plain values. `workspace.NewRoot` takes a path.
`vendor.NewOllama` takes an `Endpoint` and an `http.Client`. `daemon.New` takes a `workspace.Root`, a
slice of `vendor.Adapter`, a slice of `harness.Adapter`, an `admission.Policy`, an `*eventlog.Log` and a
`*slog.Logger`. Nothing below `cmd` can read a setting that was not handed to it, which means nothing
below `cmd` needs a config file to be tested.

The one direction that is allowed is `config` importing `vendor`, so `config.VendorProfile` can decode
straight into a `vendor.Endpoint` and a `vendor.Kind`. That is config depending on the vocabulary, never
the vocabulary depending on config, and it is why `config` sits at L1 rather than L0.

The file format is now a small decision rather than a load-bearing one, and it belongs to
[#13](https://github.com/VictorJohnOkoh/Capstone/issues/13). My recommendation is JSON, because the
stdlib parses it, an unknown field can be made an error with one call, and two small files do not
justify a dependency. The cost is that a human edits it without comments, and the answer to that is a
`daemon.example.json` beside it rather than a parser.

## Observability has a home because the Event log took the other one

The map lists observability as unspecified. The tree settles where it goes, and the rule that decides it
is ADR 0005's, already written:

> Any Daemon decision that changes how a Session behaves is itself an Event.

Everything else is a `log/slog` line, and the two never compete. The Event log is durable, ordered,
replayed to the Client and never deleted. The operational log is a text file for a human, and losing it
loses nothing the Client would have shown. ADR 0008 already sent the first entry there: an admission
refusal writes no Event because no Session exists, so it goes to the operational log.

The logger enters where config enters, and the same fact falls out of the concurrency table: **the
packages with no goroutines also take no logger.** A pure function has nothing to report. A
`*slog.Logger` appears in `daemon`, `hub`, `hostset` and `cmd`, which is the same list as the
goroutines, and that agreement is a good sign rather than a coincidence.

Metrics and tracing are not in v1, and the reason is specific rather than a deferral. A Session's Event
log already is its trace: ordered, timestamped on one clock, complete, and durable. Adding a second
timeline over the same events would produce two answers to when something happened. What is missing is
Host-level and process-level, memory, goroutine counts, SSH reconnect rates, and `net/http/pprof` on the
Daemon's loopback listener covers all of it for one import.

## Considered options

- **Two binaries, `cmd/daemon` and `cmd/hub`.** The strongest fence available: a Daemon binary that does
  not link the Hub's code cannot hold a peer list at any level. Rejected because the map fixed one
  binary while charting, and because the cost lands on the thing this project actually does most, which
  is copying a build to a Host. Two binaries also means two version numbers and a Handshake that has to
  police a skew the single build makes impossible.
- **A flat `internal/` with no nested `internal`.** Simpler to read and one directory shallower.
  Rejected: it turns fence three from a compile error into a review comment, and the whole ticket asked
  whether the violation is impossible or merely impolite. One directory is a cheap price for an answer.
- **Sub-packages per adapter, `vendor/ollama/`, `harness/acp/`.** Rejected above. It exports four types
  across an import to prevent a mistake nobody would make.
- **The Hub imports `daemon` and runs one in-process for the local Host.** Tempting, because the common
  case is one machine, and it deletes a hop. Rejected: it makes the Hub-to-Daemon leg optional, which
  means the leg that must work over SSH is the one least exercised in development. The in-process test
  from tier three gives the same convenience without letting it into production.
- **`event.Event` on the wire, with no second envelope.** One type instead of two. Rejected: the Daemon
  would unmarshal payloads to replay them and the Hub would need `event` to forward them, which breaks
  ADR 0009's promise that an unknown Kind still reaches the Client.
- **An `eventlog` interface with an in-memory implementation.** Rejected: two implementations of a log
  whose correctness is entirely about durability, ordering and crash behaviour, where the fake has none
  of those properties and would pass every test the real one fails.
- **A `daemon.Service` layer.** Rejected above. It is the pass-through the ticket named.

## Consequences

- The tree is fourteen packages, one module, one binary. Nothing in it is a layer that forwards, and
  the one thing that looks like a layer, the Hub's command handler, is one function serving ten
  endpoints and would serve an eleventh unchanged.
- **Three of the map's Not yet specified entries are now specifiable, which is what this ticket existed
  to do.** Testing strategy resolves into the three tiers above, with the stub Harness named as a
  re-exec of the test binary. Configuration resolves into two structs, one rule, and a format decision
  small enough for #13. Observability resolves into `slog` beside the Event log, `pprof` on loopback,
  and no metrics in v1.
- [#13](https://github.com/VictorJohnOkoh/Capstone/issues/13) inherits a build order that follows the
  levels. L0 and L1 first, because they are pure and their tests need nothing. Then `eventlog` and
  `daemon` with a passthrough Harness, which is the vertical slice the map asked for. Then `hub`, whose
  tier-three test is the first thing that exercises both roles. `hostset` is last, because SSH is the
  only part that cannot be tested in one process.
- The two ADRs the map still wants are partly written here. **The Hub and Daemon role split** is the
  three-fence section plus the shared-package table, and what remains for its own ADR is why one binary
  rather than two, which the Considered options section argues but does not own. The **same-Host
  invariant** is enforced by `config.Daemon` holding this Host's Vendors only and `SessionSpec.Vendor`
  carrying a loopback `Endpoint`, so it is already structural and its ADR is a paragraph rather than a
  document.
- **ADR 0007's "nothing imports `harness`" is corrected** to "nothing at `harness`'s own level imports
  `harness`". The original is false as soon as the Daemon exists, and the corrected version is the rule
  the whole graph runs on.
- The Session State fold exists twice, in `session` and in the Client's JS, sharing one JSON fixture. It
  is the only duplicated logic in the design and it is duplicated because the Client applies live Events
  itself, which #11 settled.
- `web` is the only Hub code that imports `event`. If a later version served an empty shell and let
  the JS fill it, the Hub would know nothing about Event Kinds at all. That is one import's worth of
  decision and #13 can weigh it against the first paint.
- Two lint-level checks belong in CI on day one: `go list -deps ./internal/daemon/...` must not reach
  `internal/hub`, and the four L0 packages must have no project dependencies. Both are one line, and
  both catch the failure this ADR is built to prevent.
- The Vendor fixtures still do not exist. R8 is now blocking tier two rather than blocking a document,
  which is a better place for it to be but is not progress on it.
- `CONTEXT.md` is unchanged. Ten of the fourteen packages are named after a term already in it, and
  the tree adds no domain idea, which is the outcome to want from a structural ticket.
