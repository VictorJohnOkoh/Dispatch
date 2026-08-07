# The Event model

Type: grilling
Status: open
Blocked by: 01, 03

## Question

This is the crux of the project. Every Harness's native output is translated into normalised, typed Events; the Client renders only Events. Define that taxonomy.

Design it against the **captured transcripts** from [Stand up a Host](./03-stand-up-a-host.md) and the findings from [Hermes and Pi control surfaces](./01-hermes-and-pi-control-surfaces.md) — not against imagination.

- **What are the Event kinds?** Starting point, to be attacked rather than accepted: `SessionStarted`, `AssistantMessage`, `ToolCallPending`, `ToolCallDecided`, `ToolCallStarted`, `ToolResult`, `Error`, `SessionEnded`. Which of these are real, which are missing, which collapse into one?
- **What is the common envelope?** Session id, monotonic sequence number, timestamp, kind, payload. The sequence number is load-bearing — it is the `Last-Event-ID` offset and the Event log primary key at once.
- **Streaming granularity.** Does an assistant message arrive as one Event or as a stream of deltas? Deltas make the log enormous and replay chatty; whole messages make the UI feel dead. Is there a third answer — an Event that is appended to, with the log storing the final form?
- **What is genuinely common across Harnesses versus Harness-specific?** Anything that cannot be normalised needs a deliberate home — an opaque `raw` field, a `HarnessSpecific` Event kind, or exclusion. Decide the rule, not just the cases.
- **How does the passthrough Harness fit?** The charting assumption was a strict subset — `AssistantMessage` and terminal Events only. [Vendor discovery, capability and health APIs](./02-vendor-discovery-apis.md) complicated that: reasoning content is a real thing all three Vendors emit under three different field names, so passthrough produces at least one Event kind beyond plain assistant text. Is reasoning its own Event kind, a field on `AssistantMessage`, or discarded? Whatever you choose applies to agent Sessions too, since Harnesses surface reasoning as well. **If passthrough feels forced, the abstraction is wrong**, and finding that out here is the cheapest it will ever be.
- **Errors arrive malformed and must still become Events.** Ollama breaks SSE framing on mid-stream errors (raw JSON, no `data:` prefix, no `[DONE]`); LM Studio can return a bare-string error with HTTP 200. A stream that ends badly is a normal case, not an exceptional one. Does the Event model distinguish "the Harness reported an error", "the stream died mid-message", and "the Session ended cleanly"? The Client renders these very differently.
- **Is a Session's full state derivable by folding its Events?** If not, what extra state exists, and can that be eliminated? A "yes" here is what makes replay, restart-survival and history the same mechanism.
- **Approval as a request/response pair.** `ToolCallPending` goes out over SSE; the decision comes back as a POST; `ToolCallDecided` is then itself an Event. Confirm this holds, and decide what happens to a pending call when the Client disconnects, when the Daemon restarts, and when the user never answers.
- **Versioning.** Events are persisted, so old Events must remain readable after the taxonomy changes. What is the compatibility rule?

Use `/grilling` and `/domain-modeling`. Every Event kind that survives belongs in `CONTEXT.md`.
