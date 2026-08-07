# The Harness adapter interface

Type: grilling
Status: open
Blocked by: 01, 05

## Question

Define the Go interface a Harness adapter implements — the seam that makes adding a third Harness cheaper than adding the second.

- **What is the interface?** Not a wish list — the actual method set, arguments and return types. What does the Daemon hand an adapter (Model, Vendor endpoint, working directory, Approval Policy, prompt input), and what does it get back (an Event channel, a control handle)?
- **Where does the translation boundary sit?** Does the adapter own the child process and emit Events, or does the Daemon own the process and the adapter own only parsing? The first makes the adapter deep and the Daemon thin; the second makes adapters trivial to test but pushes process quirks into the Daemon. Pick, and justify against the actual differences the research found.
- **The research made this harder than assumed, and this is now the central question of the ticket.** Pi is a subprocess emitting JSONL on stdout. Hermes has no structured stdout at all and is best driven as a **local HTTP+SSE server** (`hermes serve`), where the server is long-lived and Sessions are *runs within it*. So the interface must span "supervise a process and read its pipes" and "manage a local service and consume its event stream" — and passthrough, which has neither. If a single interface cannot cover all three without becoming a lowest-common-denominator shell, say so and propose where the split goes instead. Do not force uniformity that isn't there.
- **Capability negotiation is now confirmed necessary, not hypothetical.** Hermes supports approval interception three ways; Pi has none built in and would need a custom extension. Any design that assumes uniform approval support is already known to be wrong.
- **Is it deep or shallow?** A deep module hides a lot behind a small interface. If the interface leaks Harness-specific configuration, it is not an abstraction. Test it: what would the third adapter cost?
- **Capability negotiation.** Adapters will not be uniform — one may support approval interception, another may not. Does the interface expose declared capabilities, and how does the Daemon behave when a Session requests something its Harness cannot do? Failing loudly at Session start is better than failing silently mid-run.
- **The passthrough adapter.** It implements the same interface with no child process at all. Confirm it fits without special-casing; if it does not, the interface is wrong.
- **Lifecycle and failure.** How does an adapter signal a crash versus a clean exit? What does it do with stderr? What is the contract on shutdown — is termination graceful, forced, or both in sequence?
- **Testing.** Can an adapter be tested against recorded transcripts with no real Harness present? If not, the seam is in the wrong place.

Use `/codebase-design` alongside `/grilling` — this ticket is squarely about depth and seams.
