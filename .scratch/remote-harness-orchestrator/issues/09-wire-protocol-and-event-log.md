# Wire protocol and Event log — one design

Type: grilling
Status: open
Blocked by: 05

## Question

These are deliberately one ticket. The Event log's sequence number and the SSE `Last-Event-ID` are the same number, so designing them apart would invent two things where one exists.

**Wire protocol (Client↔Hub and Hub↔Daemon)**

- Enumerate the endpoints. Commands are `POST`; Events are an SSE stream. What is the full command set — start Session, send prompt, decide a pending tool call, stop Session, list Hosts, list Models?
- **Are Client↔Hub and Hub↔Daemon the same protocol or two?** If the same, the Hub is a fan-in proxy and gets much simpler; but the Client's view is multi-Host and a Daemon's is not, so something must differ. Find the smallest honest difference.
- SSE stream granularity: one stream per Session, one per Host, or one merged stream per Client? This decides whether the Hub is merging streams or just routing them.
- Protocol version handshake on connect, and what a mismatch does.
- What does the Hub do to Events in transit — pass through untouched, or annotate with Host identity? If it annotates, Event identity is no longer purely a Daemon concern.

**Event log**

- SQLite schema. Which columns are real and which live in a JSON payload?
- Is the sequence number per Session or per Daemon? This determines whether replay is per-Session or global, and it interacts with the stream granularity question above.
- Replay semantics on reconnect: the Client sends `Last-Event-ID`, and gets what? Define the guarantee — at-least-once, exactly-once, ordered — and whether the Client must be idempotent.
- Replay after a **Daemon restart**, where the process is gone but the log remains. What does a Client reconnecting to a Session whose Harness died actually receive?
- Retention. Does the log grow forever? If not, what is deleted, and what does that do to the guarantee above?
- Write path: does an Event reach the Client before, after, or concurrently with being durably written? Writing first is slower and correct; sending first is faster and can lose Events on crash.
- Concurrent readers — a replaying Client and a live one on the same Session.

Use `/grilling`. Nothing here needs a running system, but everything here needs the Event envelope settled first.
