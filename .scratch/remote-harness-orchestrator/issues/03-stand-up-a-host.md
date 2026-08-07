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

**Targets for the capture, now that the research has landed.** [Hermes and Pi control surfaces](./01-hermes-and-pi-control-surfaces.md) lists eleven things that documentation could not settle. The highest-value ones to resolve while you have a live install:

- Hermes' event payload **field names** — documented by name only, with the HTTP handler bodies truncated in the published source. Capture real `GET /v1/runs/{id}/events` output.
- Whether Hermes' `pre_tool_call` shell hook **fails open** when the hook itself errors. Test it deliberately by making the hook exit non-zero. This decides whether Approval Policy is trustworthy.
- Whether Pi's `ctx.ui.confirm` actually routes over `--mode rpc`. The entire Pi approval story rests on this composition, and it is inferred rather than documented.
- Exit codes for both — undocumented on either side.

Capture Hermes from `hermes serve` (its HTTP+SSE surface), not from `hermes chat` — the latter has no structured output at all.

Record in the Answer: the Host's specs and VRAM, exact Vendor and Harness versions, the invocation used for each Harness, where the captured transcripts live, and anything that failed or surprised you.

The agent can walk you through this, but the machine is yours — this one is worked with a human in the loop. Consider `/wizard` if a scripted walkthrough would help.
