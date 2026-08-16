# T3 Code — prior art

Not tied to an issue. T3 Code is the closest shipped analogue to the system in
[CONTEXT.md](../../CONTEXT.md): a control surface that supervises coding-agent processes on a
host and serves remote clients. It solves the **Harness** half of this project and deliberately
does not attempt the **Vendor** half. This note records what converges with decisions already
made, what is worth stealing, and where the two designs genuinely part.

## What was researched, and how far to trust it

Checked on **2026-08-13** against `pingdotgg/t3code` — the published `docs/` tree and repo
metadata, read from `main`. **The source tree itself was not read.** Every claim below is
sourced from project documentation, which is a weaker primacy standard than the Ollama and
llama.cpp work in [vendor-discovery-apis.md](./vendor-discovery-apis.md), where the Go and C++
sources were read directly.

Two consequences, both flagged inline:

- Absence of a feature in the docs is recorded as **[not found]**, never as "does not exist".
- No version pin was established. The repo publishes no version-stamped API surface, so
  everything here is "as documented on `main`, 2026-08-13".

| | |
| --- | --- |
| Repo | [github.com/pingdotgg/t3code](https://github.com/pingdotgg/t3code) |
| Licence | MIT (Copyright T3 Tools Inc.) |
| Runtime | Node.js `^22.16 \|\| ^23.11 \|\| >=24.10` |
| Shape | One monorepo → `apps/server`, `apps/desktop` (Electron), `apps/web`, `apps/mobile` (iOS/Android) |
| Docs read | `internals/overview.md`, `internals/providers.md`, `internals/connection-runtime.md`, `internals/remote.md`, `internals/t3-connect.md`, `user/permission-modes.md`, `user/providers-claude.md`, `user/remote-access.md`, `user/background-service.md` |

Distinct from **T3 Chat**, which is a subscription chat product. T3 Code is BYO-agent and
BYO-credentials.

---

## What it is

**Not an agent, and not a fork of one.** T3 Code wraps five existing agent CLIs as subprocess
drivers: `codex`, `claudeAgent`, `cursor`, `grok`, `opencode`. It runs no inference and defines
no tools of its own.

That places it at the same layer as our **Harness** abstraction, not our Vendor one — and it
reached the same structural answer: an adapter per agent CLI, translating that CLI's native
protocol (Codex's JSON-RPC-over-stdio app-server, and so on) into a normalised internal event
stream.

The registry split is worth naming, because it is a distinction our issue
[06 — harness adapter interface](../../.scratch/remote-harness-orchestrator/issues/06-harness-adapter-interface.md)
has not yet drawn:

- **`ProviderInstanceRegistry`** — *configured* instances by id. Looks up a driver by
  `driverKind`, calls `driver.create`.
- **`ProviderAdapterRegistry`** — *live* adapters, resolved from an instance id.
- **`ProviderService`** — sits on both, combining adapter resolution with session management
  "to enable callers to reference threads rather than specific agents."

A driver declares three things: a `driverKind`, a `configSchema`, and a `create` function.

> "Adding a driver means writing the driver plus adapter and adding it to `BUILT_IN_DRIVERS`.
> No orchestration, contract, or client change is required for the common case."
> — `docs/internals/providers.md`

That last sentence is the test our own Harness interface should be held to.

---

## Convergence — four decisions independently confirmed

These are not new information, but they are evidence that charting decisions recorded in
[map.md](../../.scratch/remote-harness-orchestrator/map.md) are load-bearing rather than
arbitrary.

**1. The server is the sole execution boundary.**

> "The server is the execution boundary: every provider process, terminal, git operation, and
> filesystem read happens there, never in the client." — `internals/overview.md`

Matches "the Client never sees raw Harness output", and pushes it further: not just output
normalisation, but *all* privileged operations.

**2. Loopback plus tunnel; never self-rolled internet-facing auth.** Four documented access
methods: direct WS/WSS, a Cloudflare-tunnel-backed managed relay ("T3 Connect"), Tailscale
Serve, and desktop-managed SSH launch with a local port-forward. The relay brokers pairing
credentials and never proxies application traffic. This is our reach story, Tailscale included,
arrived at independently.

**3. Daemons never learn about their peers.** One running server is one `ExecutionEnvironment`
with a stable `environmentId`. There is explicitly no cross-server control plane and no session
replication between environments — clients hold a catalog of known environments and talk to one
at a time.

**4. Headless operation is first-class, not an afterthought.** `t3 serve` runs the server with
no browser and prints a pairing token/URL/QR. A separate background-service mode keeps it alive
after logout on Linux. Corroborates
[ADR 0001 — resident Daemon on each Host](../adr/0001-resident-daemon-on-host.md).

---

## Worth stealing

### The event-sourced core — bears directly on [issue 05](../../.scratch/remote-harness-orchestrator/issues/05-the-event-model.md)

Commands and events are separated by a pure function:

> "Clients dispatch typed commands; the engine turns them into persisted events; projections
> derive the read model." … For each envelope the engine checks durable receipts for
> idempotency, runs the decider, then "inside one SQL transaction, appends events to the event
> store, applies them to the in-memory read model via projector, projects them into persisted
> tables, and writes the accepted receipt."
> — `internals/overview.md`

The invariant that buys: *"Because persistence and projection share a transaction, the read
model cannot durably disagree with the event log."*

Our map already says "the Event log is transport, replay buffer and history at once". This is
the sharper version of the same idea — the decider being a **pure function from state and
command to events** is what makes the whole thing testable without a live Harness. Note also
the single worker fiber processing envelopes sequentially: ordering is a property of the
architecture, not of careful locking.

Idempotency receipts are a detail we would otherwise have discovered late, when a client
reconnects and replays a command.

### `DrainableWorker` — a candidate answer to an open question

> "`DrainableWorker` pairs a transactional queue with a transactional count of outstanding
> items." Services expose `drain` methods so tests "await 'queue empty and current item
> finished' instead of sleeping." — `internals/overview.md`

The map lists **"testing strategy without a GPU"** as unspecified. This is a real, shipped
answer to half of it: if every queue in the Daemon exposes `drain`, supervision and event
ordering become deterministically testable against a stub Harness, with no sleeps and no model.
It does not solve the fixture problem — what a recorded Event stream looks like — but it
removes the synchronisation problem underneath it.

### Launch vs. access as separate concepts

T3 Code deliberately separates *how a remote server comes to exist* (SSH-launched by the
desktop app, already running, published through the relay) from *how a client reaches it*
(direct, relay, Tailscale, port-forward). Our issues
[03](../../.scratch/remote-harness-orchestrator/issues/03-stand-up-a-host.md) and
[04](../../.scratch/remote-harness-orchestrator/issues/04-host-reachability-and-presence.md)
carry this split implicitly. The vocabulary is better than ours and costs nothing to adopt.

### Turn-bracketed checkpoints as hidden Git refs

> "Each turn is bracketed by workspace checkpoints so diffs and reverts are exact.
> `CheckpointStore` captures state as hidden Git refs through the VCS driver's checkpoint
> operations." — `internals/overview.md`

Cheap per-turn diff and revert, with no bespoke snapshot format, in a Workspace Root that is
already a Git repo. Out of scope for v1, but the cost of *not foreclosing it* is low: it only
requires that turn boundaries be identifiable in the Event model, which they must be anyway.

---

## Where the designs part

### Their wire protocol is bespoke and welds client to server

One authenticated **Effect RPC WebSocket**, with contracts as shared TypeScript schemas in
`packages/contracts` (`WS_METHODS` assembles a `WsRpcGroup`; each member is either unary or a
server stream). Streaming subscriptions replaced what used to be a broadcast push bus — clients
subscribe to the threads and terminals they care about rather than receiving everything.

The subscribe-to-what-you-need correction is worth noting. The transport choice is not: it is
fast to build inside a TypeScript monorepo and unusable from anything else. Our HTTP-commands +
SSE-events with `Last-Event-ID` stays the better fit for a dumb server-rendered Client and a
second TUI Client, which is precisely the constraint that makes the Hub's API boundary real.

Their client-side connection supervisor — reconnect with exponential backoff capped at 16s,
explicit version-mismatch handling — is worth reading regardless of transport.

### No vendor abstraction whatsoever — the biggest divergence

T3 Code does not abstract model providers. It passes environment variables through to the
wrapped CLI (`ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CONFIG_DIR`, `CODEX_HOME`,
and so on) and lets that CLI do its own routing; the docs demonstrate pointing Claude Code at
OpenRouter this way. BYOK works, and an arbitrary OpenAI-compatible base URL works, but only to
the extent the wrapped CLI already supports one.

There is **[not found]** any model catalog, capability metadata, health checking, VRAM
accounting, or discovery. Everything in
[issue 02](../../.scratch/remote-harness-orchestrator/issues/02-vendor-discovery-apis.md) —
which established that no Vendor advertises itself and that capability metadata diverges
sharply between Ollama, LM Studio and llama.cpp — has no counterpart here.

This is the clearest case where our design goes somewhere T3 Code does not, and the reason is
structural rather than an oversight: wrapping hosted-agent CLIs means the model is someone
else's problem. Driving local Vendors means it is ours.

### No fleet

Each `ExecutionEnvironment` is siloed. A client selects one and talks to it. There is no
aggregating Hub, no multi-host view, no cross-environment session sharing or merge. Our
[issue 10 — the multi-host Client view](../../.scratch/remote-harness-orchestrator/issues/10-the-multi-host-client-view.md)
is unexplored ground by comparison, and cannot be answered by copying anything here.

### Approvals are a mapping layer, not a policy

Four per-thread permission modes — Supervised, Auto-accept edits, Auto, Full access — mapped
onto each provider's native mechanism (Codex's approval policy plus sandbox level; Claude's own
auto-permission mode). Providers with no equivalent, such as OpenCode, **fall back to
always-ask**. Approvals surface inline in the conversation through the event stream
(`thread.approval.respond`). Sandboxing is delegated entirely downstream; T3 Code implements
none.

The shape of this problem is identical to ours.
[Issue 01](../../.scratch/remote-harness-orchestrator/issues/01-hermes-and-pi-control-surfaces.md)
found Hermes offering three interception mechanisms and Pi offering none. T3 Code's answer to
the same asymmetry was to **expose the lowest common denominator and degrade loudly** — the
weakest provider forces always-ask rather than the abstraction pretending a uniform guarantee
it cannot deliver. That is a defensible position for our Approval Policy seam, and it is
notably *not* the same as "implement the missing gate ourselves".

---

## Open questions this raises for us

- If the decider is pure and events are the only durable truth, does the Daemon need a SQL
  store, or is an append-only file enough at our scale? T3 Code's transaction invariant is
  bought with SQLite; the invariant matters more than the mechanism.
- Does our Harness adapter meet the "no orchestration, contract, or client change" bar when a
  third Harness is added? Worth stating as an explicit acceptance test on issue 06.
- Should Approval Policy adopt lowest-common-denominator degradation, given Pi has no gate at
  all — or is that a case for refusing to run Pi unsupervised rather than silently always-asking?
