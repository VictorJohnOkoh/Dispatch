# Map: Remote Harness Orchestrator

## Destination

A locked architecture spec for the system described in [CONTEXT.md](../../CONTEXT.md) — domain model, module boundaries and their interfaces, wire protocol, storage design, and a frozen v1 feature list — complete enough that building it is execution rather than design.

This map ends at the spec. Writing the code is a separate effort.

## Notes

**Domain**: A Go program running in two roles — a Daemon on each Host supervising Harness processes against local Vendors, and a Hub aggregating several Hosts and serving a browser Client.

**Skills every session should consult**: `/grilling` and `/domain-modeling` by default. `/codebase-design` for any ticket about interfaces or seams. `/prototype` for the Client ticket. `/research` for research tickets.

**Standing preferences for this effort** (settled while charting — do not relitigate without cause):

- Purpose is learning first, portfolio second. Optimise for architecture practice, not delivery speed. There is no deadline.
- Data structures and algorithms are explicitly **not** a goal; networks, concurrency, process supervision and interface design are.
- Go. Non-negotiable now. Keep the browser Client dumb — server-rendered HTML and a little vanilla JS. No React.
- One binary, two roles (`daemon`, `hub`). Daemons never learn about their peers; all multi-Host knowledge lives in the Hub.
- Control Plane and Data Plane stay separate. A Harness always reaches a Vendor on its own Host over localhost.
- The Vendor abstraction covers discovery, capability and health — not inference.
- Direct prompting with no agent is a passthrough Harness, never a second code path.
- Events are normalised and typed. The Client never sees raw Harness output. The Event log is transport, replay buffer and history at once.
- Transport is HTTP for commands, SSE for Events, with `Last-Event-ID` carrying the Event log offset.
- Daemons bind loopback only. Reach is via SSH tunnel; Tailscale is the later upgrade. Never self-rolled internet-facing auth.
- Design the seam, ship the simple implementation — this applies to admission control (one Session at a time behind a policy interface) and to Daemon install (manual now, Client-driven later).

**Decided while charting, recorded elsewhere**: [ADR 0001 — resident Daemon on each Host](../../docs/adr/0001-resident-daemon-on-host.md).

## Decisions so far

<!-- one line per resolved ticket -->

- [Hermes and Pi control surfaces](./issues/01-hermes-and-pi-control-surfaces.md) — the two Harnesses have **incompatible integration shapes**: Pi is a subprocess emitting JSONL (`--mode json` / `--mode rpc`), Hermes has no structured stdout and is best driven as a local HTTP+SSE server (`hermes serve`). Approval is likewise non-uniform: Hermes offers three interception mechanisms, Pi has none built in.
- [Vendor discovery, capability and health APIs](./issues/02-vendor-discovery-apis.md) — Vendors are **uniform for inference and genuinely divergent underneath**. Capability metadata is rich on Ollama and LM Studio, per-server-only on llama.cpp. Lifecycle, health and VRAM differ on every axis. **Passthrough is not trivial** — reasoning fields have three different names and Ollama breaks SSE framing on mid-stream errors. No Vendor advertises itself; discovery must be ours.

## Not yet specified

- **Testing strategy without a GPU.** How the Daemon's supervision and streaming get tested without real models or real Harnesses — a stub Harness binary, recorded Event fixtures, or something else. Cannot be sharpened until the package seams exist.
- **Configuration format and shape.** How a Host profile, its Workspace Root, and its Vendor endpoints are declared, and where that file lives for each role. Now known to be load-bearing rather than incidental: no Vendor advertises itself, so everything is explicitly configured or it does not exist. Still waits on the package structure.
- **Observability inside the Daemon.** Logging, and whether Session supervision needs metrics or tracing to be debuggable at all.
- **Error taxonomy and how failure reaches the user.** Vendor down, Harness crashed, Model won't load, Host unreachable — these are different to the user and probably different in the Event model, but the shape can't be fixed before the Event model is.
- **The TUI as a second Client.** Intended as proof that the Hub's API boundary is real. Nothing decidable until that API exists.
- **Real admission control.** The concurrent-Sessions policy behind the v1 seam — eviction, queueing, VRAM accounting. Waits on the seam being drawn.
- **Client-driven Daemon install over SSH**, and the Tailscale reach upgrade. Both deliberately deferred; both need the v1 shape settled first.
- **Two further ADR candidates**: the Hub/Daemon one-binary-two-roles split, and the same-Host invariant for Harness and Vendor. Both qualify on all three bars; neither written yet.

## Out of scope

- **Building the app.** This map produces a spec. Implementation is a separate effort against that spec.
- **Cross-Host Sessions** — a Harness on one Host using a Vendor on another. Rejected in charting: it drags inference traffic back onto the network and reintroduces auth, tunnelling and a streaming proxy on the highest-volume path, for a case that does not currently exist.
- **Self-rolled internet-facing auth or TLS.** Rejected in charting: remote process execution behind hand-written auth is a real exposure, not a learning exercise. Loopback binding plus SSH or Tailscale instead.
- **A native desktop Client**, and any heavyweight frontend framework. Rejected in charting: install friction, the worst remote-access story, and a skill investment orthogonal to the point of the project.
- **Multi-user or multi-tenant operation.** Single user throughout.
- **Showcasing data structures and algorithms.** Ruled out in charting: this problem contains none naturally, and forcing them in would degrade the design and read as padding.
