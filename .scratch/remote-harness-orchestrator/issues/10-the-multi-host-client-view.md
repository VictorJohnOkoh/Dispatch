# The multi-Host Client view

Type: prototype
Status: open
Blocked by: 05, 09

## Question

What does the Client actually look like when several Hosts are live at once and Sessions are running on more than one of them? Build something rough to react to rather than arguing in the abstract.

The interesting question is not styling. It is **what the user is looking at**:

- Is the primary object a Session, a Host, or a conversation? Three defensible answers with different information architectures.
- How does a Host's presence state — including the transitional and stale cases from [Host reachability and presence](./04-host-reachability-and-presence.md) — appear without dominating the screen?
- How does a running Session on an unreachable Host read? It still has history and may still be running; the user simply cannot see it right now.
- How does a `ToolCallPending` demand attention when the user is looking at a different Session on a different Host? This is the one genuinely interruptive Event and it can arrive from anywhere.
- How does an agent Session render — a transcript, a step list, something else? Tool calls and their results are structurally nested, and a flat chat log may be the wrong metaphor entirely.
- Picking a Host, then a Vendor, then a Model, then a Harness is four choices before anything happens. Is that a wizard, one dense form, or mostly defaults with an override?

Constraints, from the map's Notes: server-rendered HTML and a little vanilla JS. No React. The prototype is throwaway and may fake all data — no Daemon required.

Use `/prototype`. Link the artefact from the Answer; record what the prototype **changed your mind about**, since that is the only reason it exists.
