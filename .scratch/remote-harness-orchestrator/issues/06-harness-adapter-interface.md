# The Harness adapter interface

Type: grilling
Status: open
Blocked by: 01, 05

## Question

Define the Go interface a Harness adapter implements — the seam that makes adding a third Harness cheaper than adding the second.

- **What is the interface?** Not a wish list — the actual method set, arguments and return types. What does the Daemon hand an adapter (Model, Vendor endpoint, working directory, Approval Policy, prompt input), and what does it get back (an Event channel, a control handle)?
- **Where does the translation boundary sit?** Does the adapter own the child process and emit Events, or does the Daemon own the process and the adapter own only parsing? The first makes the adapter deep and the Daemon thin; the second makes adapters trivial to test but pushes process quirks into the Daemon. Pick, and justify against the actual differences the research found.
- **The research made this easier than assumed in one way and harder in another.** Both Harnesses are the *same* integration shape — subprocess with structured stdio (`hermes acp` is JSON-RPC 2.0 over ndjson, `pi --mode rpc` is LF-delimited JSONL). The HTTP+SSE server this ticket originally assumed for Hermes does not exist. So the interface spans two process shapes, not three: "supervise a process and read its pipes", and passthrough, which has no process at all. The hard part moved: the two Harnesses agree on shape and **disagree on completeness**. Pi gives a full tool lifecycle on one stable id; Hermes gives a start event, no completion for `read` or `edit`, and an approval id that does not match its own tool-call id. Decide whether the adapter is allowed to **synthesise** the missing Events, or whether the gap is the Daemon's to close.
- **Stdin is a supervision concern, not a detail.** Both Harnesses trap on it in opposite directions — Pi's `-p` reads to EOF even with the prompt as an argument, and `--mode rpc` truncates the run mid-turn if stdin closes; Hermes deadlocked on Windows because a spawned *child* inherited the pipe. Whichever side of the seam owns the process must own stdin explicitly rather than inherit it.
- **Capability negotiation is still necessary, but not for the reason first recorded.** Both Harnesses can gate a tool call — Hermes in-protocol via `session/request_permission`, Pi over RPC via `extension_ui_request` with its bundled `permission-gate` extension. What neither does is report a **refusal** the orchestrator can trust: Hermes reports a human refusal and an internal failure identically, and Pi reports refusal only as a tool error with free text. So the capability to negotiate is "can this adapter gate", and the interface must let the Approval Policy record its own decision rather than read it back out of the Event stream.
- **Is it deep or shallow?** A deep module hides a lot behind a small interface. If the interface leaks Harness-specific configuration, it is not an abstraction. Test it: what would the third adapter cost?
- **Does the interface declare its capabilities, and what happens when one is missing?** Failing loudly at Session start is better than failing silently mid-run — and silent degradation of an approval gate is the worst option available.
- **The passthrough adapter.** It implements the same interface with no child process at all. Confirm it fits without special-casing; if it does not, the interface is wrong.
- **Lifecycle and failure.** How does an adapter signal a crash versus a clean exit? What does it do with stderr? What is the contract on shutdown — is termination graceful, forced, or both in sequence?
- **Testing.** Can an adapter be tested against recorded transcripts with no real Harness present? If not, the seam is in the wrong place.

Use `/codebase-design` alongside `/grilling` — this ticket is squarely about depth and seams.
