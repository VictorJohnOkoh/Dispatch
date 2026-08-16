# Stand up a Host

Type: task
Status: open
Blocked by: —

## Question

Nothing to decide here. This is manual work that unblocks decisions — specifically [The Event model](./05-the-event-model.md), which cannot honestly be designed against documentation alone.

Get one real Host into a state where Harness output can be observed directly:

1. A machine that will act as the Host, reachable over SSH with key auth from the machine you sit at.
2. A Vendor installed and serving — Ollama or LM Studio — with at least one tool-calling-capable model pulled and confirmed working.
3. Both Harnesses installed, or as many as prove installable. If one cannot be installed, record why; that is a real finding.
4. Each Harness driven manually at least once against the local Vendor, end to end, until it makes a tool call.
5. **Raw output captured to files** — stdout and stderr, for both a plain exchange and one involving a tool call, in whatever modes the Harness offers. These transcripts are the primary evidence the Event model gets designed against.

**Part of this is already done.** The capture work behind [ticket 01](./01-hermes-and-pi-control-surfaces.md) covered items 2 to 5 on the local machine and under WSL2: LM Studio serving `qwen/qwen3.5-9b`, both Harnesses installed and driven to real tool calls, and raw transcripts landed in `docs/research/captures/`. What remains is item 1 — a **separate** machine acting as the Host, reachable over SSH with key auth. Until that exists, every finding is single-machine and the same-Host invariant has never been exercised across a network.

**Targets for the capture, now that the research has landed.** [Hermes and Pi control surfaces](./01-hermes-and-pi-control-surfaces.md) lists eleven things that documentation could not settle. The highest-value ones to resolve while you have a live install:

All of the original targets are now answered — event vocabulary and payloads, the shell hook failing open, Pi's gate routing over `--mode rpc`, and exit codes for both. What a **remote** Host adds is the class of question a single machine cannot answer:

- Does the Hermes stdin deadlock stay absent on bare-metal Linux, or was WSL2's pipe implementation doing the work? The disproof rests on three runs under one kernel.
- Does a Harness behave differently when its Vendor is on the same machine but reached over a real interface rather than loopback?
- What does SSH key auth plus a tunnel actually cost in setup steps? That number is the honest input to the "manual Daemon install" decision in [ticket 12](./12-freeze-v1-and-write-the-spec.md).

Capture Hermes from `hermes acp`, **not** `hermes serve` — the HTTP+SSE run API is documented but absent from v0.19.0. Capture Pi from `pi --mode rpc`, which is a superset of `--mode json`. Reuse `scripts/capture-hermes.sh` and `scripts/capture-pi.sh`.

Record in the Answer: the Host's specs and VRAM, exact Vendor and Harness versions, the invocation used for each Harness, where the captured transcripts live, and anything that failed or surprised you.

The agent can walk you through this, but the machine is yours — this one is worked with a human in the loop. Consider `/wizard` if a scripted walkthrough would help.
