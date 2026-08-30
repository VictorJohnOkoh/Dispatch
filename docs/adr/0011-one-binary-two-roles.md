# One binary runs both roles, and the Hub is the only place a second Host can be named

`dispatch daemon` and `dispatch hub` are the same build, chosen by `argv[1]`. The Daemon owns one
Host and never learns that another exists. The Hub holds a connection to every configured Host,
merges what they report, and serves the Client. Neither role is a subset of the other, and nothing
durable lives in the Hub.

The map listed this as an outstanding ADR. [ADR 0010](0010-go-package-structure-and-seams.md) wrote
most of it while deciding the package tree: the three fences and the shared-package table are there
and are not repeated here. What was left, and what this ADR owns, is why the split is two roles in
one binary rather than two binaries, and what each role is answerable for.

## What each role owns

| | Daemon | Hub |
| --- | --- | --- |
| how many Hosts it knows | one, its own | every Host in its config |
| Session registry | owns it | has none |
| Harness processes | spawns, supervises, kills | never spawns anything |
| Vendors | polls its own on loopback | never speaks to one |
| the Event log | writes it, and it is the only writer | never writes, never stores |
| Sequence Numbers | allocates them, gapless from 1 | tracks one per Host, allocates none |
| Host State | cannot compute it | owns it, and it exists nowhere else |
| the protocol | serves it | speaks it in both directions |
| what survives a restart | the log, so almost everything | nothing, because it holds nothing |

Read down the Hub column and the pattern is that the Hub adds one fact to the system and holds no
other: which Host a frame came from, and whether that Host is answering. Everything else it shows,
it is forwarding.

That asymmetry is why the two roles are not symmetric packages. The Hub has two faces, the browser
and the Daemons, so [ADR 0010](0010-go-package-structure-and-seams.md) split it into `hostset` and
`web`. The Daemon has one inward face and four outward ones, Vendors, Harness processes, the log and
the filesystem, and all four need the Session registry, so splitting it by face gives four packages
that all reach for the same state.

## Why one binary

Two binaries is the stronger fence. A Daemon build that does not link the Hub's code cannot hold a
peer list at any level, and no `go list` check in CI is needed to prove it. That is a real thing to
give up, so the reasons had better be real too.

**Deployment is the operation this project performs most.** Every capture in `docs/research/captures/`
got onto the Host by copying a build over SSH, and [#16](https://github.com/VictorJohnOkoh/Dispatch/issues/16)
recorded two runs lost to a copy that landed in the wrong directory and said nothing. One artefact
per Host, with the Client's templates and CSS embedded in it, means the copy is one file and a wrong
copy is a missing file rather than a stale half of a pair.

**Two binaries is two version numbers, and the Handshake would then police a skew that need not
exist.** The Handshake in [ADR 0004](0004-host-state-is-connection-liveness.md) exists for skew
across time: a Host that has not been updated in three months. With two binaries it also has to
catch skew inside one release, where the Hub is new and the Daemon beside it on the same disk is
old. That failure is entirely self-inflicted and one build removes it.

**The fences the second binary would have given are available for one directory and two CI lines.**
`hub/internal/hostset` is a compile error from `internal/daemon`, because a nested `internal` is shut
to everything outside its parent. `config.Daemon` has no field that can name a peer, so a config that
tries is `unknown field` at startup. No type the Daemon serves has a `host` field, so a Host id sent
to a Daemon lands nowhere. Three mechanisms, three different failure times, and the one hole left
open, `internal/daemon` importing `internal/hub` and gaining nothing, is closed by
`go list -deps ./internal/daemon/... | grep hub` in CI.

Where the argument would change: if the Daemon ever had to be small, on a machine where the Client's
embedded assets were a cost worth counting, two binaries would win on size alone. That is not this
system. The Hosts are machines chosen for their GPUs.

## Considered options

- **Two binaries, `cmd/daemon` and `cmd/hub`.** Rejected above. The fence is real and the price is
  paid on every deploy, which is the thing done most.
- **A library plus two thin `main`s in one module.** All the deployment cost of two binaries and none
  of the fence, since both `main`s link the same library. Strictly worse than either honest option.
- **One role that is both, with the Hub running an in-process Daemon for the local Host.** Tempting,
  because the common case is one machine, and it deletes a network hop. Rejected: it makes the
  Hub-to-Daemon leg optional, so the leg that has to work over SSH becomes the one least exercised in
  development. [ADR 0010](0010-go-package-structure-and-seams.md)'s tier-three test gives the same
  convenience in a test, where an untested production path cannot hide.
- **No Hub at all, with the browser opening one `EventSource` per Host.**
  [ADR 0009](0009-wire-protocol-and-event-log.md) killed this: six connections per origin over
  HTTP/1.1, no HTTP/2 without TLS, and self-rolled TLS is out of scope, so a six-Host user starves
  their own commands and gets no error saying why. The Hub also has to exist for Host State, which is
  connection liveness and so belongs to whoever holds the connections.

## Consequences

- One file is the whole deployment, on a Host and on the machine the browser talks to. The Client's
  templates, CSS and JS are `go:embed`ed into it.
- The Handshake keeps one job, version skew across time, and never has to reason about a Hub and a
  Daemon from different builds of the same release.
- Two CI checks are load-bearing rather than tidy, and belong in the first commit that has a
  `go.mod`: `go list -deps ./internal/daemon/...` must not reach `internal/hub`, and the L0 packages
  must have no project dependencies.
- The Hub holds nothing durable, so a Hub restart costs the Client its cursor and nothing else. Every
  Daemon replays from the log and the Client repairs itself, which is the same path a network drop
  already takes.
- A second Client is a different `web` against the same `protocol`. The role split is what makes the
  TUI in the map's **Not yet specified** list a package rather than a project.
