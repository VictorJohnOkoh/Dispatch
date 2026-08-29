# One protocol that differs by a Host id, a merged stream to the Client, and an Event that is written before it is sent

The Client's protocol and the Daemon's could have been two designs. The Client sees several Hosts and
a Daemon sees one, so there is a real difference to express, and expressing it twice would have been
defensible. Instead there is one protocol, and the Hub adds a Host id to what it forwards and strips
one from what it routes. That is the whole difference, and it shows up in three places rather than in
a second document.

The second decision is about the stream. ADR 0004 already fixed the Daemon's leg at one stream per
Host, because a Host's state *is* that stream's liveness. The Client's leg is one merged stream for
every Host at once, so a browser holds one connection however many Hosts the user owns.

The third is the write path. An Event is committed to SQLite before its frame is sent. Everything in
this project rests on the log being the truth, and a Client that saw an Event the log does not hold
would watch it disappear on the next replay. Deltas are what make that affordable. They are sent and
never written, so the per-token path touches no disk at all.

## What was already decided

Four earlier ADRs and the map hand this one its constraints. None of them is reopened here.

| from | what it fixes |
| --- | --- |
| ADR 0004 | One Event stream per Host, multiplexing every Session. An SSE keepalive frame. The Handshake makes a Host `Ready` or `Incompatible`. |
| ADR 0005 | The five-field envelope. `Seq` is per Daemon, gapless, allocated inside the write transaction. Deltas are never stored. |
| ADR 0007 | A Model catalogue is a request and a response, not an Event. A `Catalogue` may be shown Stale; a `Resident` list may not. |
| ADR 0008 | Five Session states, all folded from the log. Admission runs before any Event exists. The boot sweep writes four kinds per abandoned Session. |
| The map | HTTP for commands, SSE for Events, `Last-Event-ID` resuming the Event stream. Daemons bind loopback only. The Client is server-rendered HTML and a little vanilla JS. |

ADR 0004 also left one question open by name. What happens when a `Last-Event-ID` is unknown to the
Daemon because its log rotated. It is answered in [Resync](#resync-is-a-frame-not-an-error).

## One protocol, and the Host id is the difference

Client to Hub and Hub to Daemon are the same protocol. Same paths, same bodies, same frames, same
status codes. The Client's version names a Host and the Daemon's does not, and that one idea appears
in three places:

| place | Daemon leg | Client leg |
| --- | --- | --- |
| a command's path | `POST /v1/sessions/{id}/prompts` | `POST /v1/hosts/{host}/sessions/{id}/prompts` |
| a frame's body | no `host` field | `"host": "desktop"` on every frame |
| the stream cursor | `412` | `desktop=412,laptop=98` |

The Daemon never sees the compound cursor. The Hub splits it and passes each Host a plain integer, so
a Daemon's `Last-Event-ID` is one number and stays one number. That matters more than it looks. The
Daemon's half of the protocol is exercisable by hand with `curl`, and nothing in it knows a Hub
exists.

There is one thing the Hub originates rather than forwards, and it is the same difference wearing a
different hat. Host State is `Connecting`, `Ready`, `Down{cause}` or `Incompatible`, and ADR 0004 put
it in the Hub because the Hub is the only component that knows more than one Host exists. It cannot
be an Event. ADR 0005's envelope carries a Session id on every Event, and a Host that is down has no
Session to carry one. So the Client's stream has a `host` frame that no Daemon ever sends. Calling
that a second protocol would be generous. It is the Hub reporting the only fact it owns.

Two things follow that are worth having on purpose.

**The Hub is a router, not a merger of meaning.** It reads an SSE frame's `event:` name and its `id:`
field, adds a Host, and writes it out. It never parses a payload. So an Event kind the Hub has never
heard of still reaches the Client, which is what ADR 0005's append-only compatibility rule needs from
a component sitting in the middle of it.

**A second Client costs nothing.** The map wants a TUI eventually, as proof that the Hub's boundary
is real. Under this shape a TUI speaks the same protocol as the browser, which is the same protocol
the Hub speaks to a Daemon.

## The command set

Ten endpoints on the Daemon. The Client's are the same under a `/v1/hosts/{host}` prefix, plus
`GET /v1/hosts` for the Host list, which is the one thing only the Hub can answer.

| method | path | what it does |
| --- | --- | --- |
| `GET` | `/v1/events` | the Host's Event stream. `Last-Event-ID` resumes it |
| `POST` | `/v1/sessions` | start a Session. Admission runs here |
| `POST` | `/v1/sessions/{id}/prompts` | submit a Prompt |
| `POST` | `/v1/sessions/{id}/approvals` | decide one held Tool Call |
| `POST` | `/v1/sessions/{id}/policy` | set the Approval Policy |
| `POST` | `/v1/sessions/{id}/interrupt` | abandon the Prompt, keep the Session |
| `POST` | `/v1/sessions/{id}/stop` | stop the Session |
| `GET` | `/v1/sessions` | this Host's Sessions, with a cursor |
| `GET` | `/v1/sessions/{id}/events` | one Session's Events, with a cursor |
| `GET` | `/v1/models` | the Vendor catalogue |

Stopping is `POST .../stop` rather than `DELETE /v1/sessions/{id}`, because a stopped Session is not
deleted. Its history stays readable, which is ADR 0008's `Ended` state, and a `DELETE` that leaves
the thing behind is a lie in the method name.

`interrupt` and `stop` are separate endpoints because ADR 0008 made them separate buttons. Interrupt
abandons the Prompt and keeps the Session usable. Stop runs the shutdown ladder and kills the process
group.

### A command is an intention, and the answer arrives as an Event

The rule the whole command set rests on:

> A command's only successful answer is that it was accepted. Everything that happens because of it
> arrives on the stream.

So `POST /prompts` returns `202` and an empty body. The `PromptSubmitted` Event, the Deltas, the tool
calls and the `PromptCompleted` all arrive on the stream, in the same order and by the same mechanism
whether the Client asked a second ago or is replaying from last Tuesday. One path, not two.

The rule also decides how many Events a command writes, which is the Daemon's business and not the
caller's. Answering an approval with **always allow** is one `POST /approvals` carrying
`{toolCallId, decision, applyToKind: true}`, and the Daemon writes `ApprovalDecided` and
`ApprovalPolicySet` in one transaction. ADR 0005 required both Events and said why. Sending them as
two commands would let the Client die between them and leave a Session running under a policy nobody
chose.

**`POST /v1/sessions` is the one command with a body in its answer.** It returns `201` with the
Session id and the `Seq` of its `SessionStarted`:

```json
{"session": "s-7f3a2c", "seq": 9412}
```

ADR 0008 writes `SessionStarted` before the launch begins, so that a 30 second cold Model load is
something the user can watch and cancel. The id has to reach the caller for that to be true.
Returning the `Seq` beside it costs four bytes and tells the Client exactly where its Session starts
in the log.

### Refusals, and the two kinds of no

A command is refused before any Event is written in exactly the cases ADR 0008 names: admission, a
policy slot with no Gate, and a working directory outside the Workspace Root. Two status codes split
those by what the user should do about it.

| status | meaning | cases |
| --- | --- | --- |
| `409 Conflict` | the current state says no. Change the state and ask again | admission refused; a second Prompt while `Working`; a decision on a question that is not open |
| `422 Unprocessable Content` | the request itself is wrong. Change the request | a `wait` or `refuse` slot with no Gate; a directory outside the Workspace Root; an unknown Model |
| `404 Not Found` | no such Session | |
| `426 Upgrade Required` | protocol version | see the Handshake |
| `503 Service Unavailable` | the Hub only: this Host is not `Ready` | |

The body is the same shape every time, and admission fills the one extra field ADR 0008 defined:

```json
{"reason": "admission", "detail": "one Session at a time on this Host", "blocking": ["s-7f3a2c"]}
```

`blocking` is what lets the Client offer "stop that one and start this one" as a single action, which
is the whole argument ADR 0008 made against a queue.

### Retries need no request ids

Five of the six commands are idempotent for free, three because the Session state machine refuses the
duplicate and two because repeating them changes nothing. A retried Prompt lands in `Working` and
gets `409`. A retried approval finds no open question and gets `409`. A retried interrupt finds no
Prompt in flight and gets `409`. A second stop joins the first, which ADR 0008 decided, so both get
`202`. A retried policy set writes a second `ApprovalPolicySet` with the same values, which folds to
the same answer.

`POST /v1/sessions` is the exception, and it is not idempotent. A retry after a lost response starts a
second Session. Admission refuses it today, so the exception costs nothing while one Session at a time
is the policy. If admission is ever loosened the cost is one Session the user stops, which is cheaper
than an idempotency key on all six commands to defend against one of them.

## The stream

### Granularity

One stream per Host on the Daemon's leg. ADR 0004 decided that, and its reason is not about Events at
all. The stream's liveness *is* the Host's presence, and its 10 second keepalive is what proves the
Daemon's own loop is running. A stream per Session would leave a Host with no Sessions unmeasured,
and would need a second connection to measure it.

One merged stream on the Client's leg, carrying every Host. The browser holds one `EventSource`.

Per-Host streams to the browser were the tempting alternative. They would have made the difference
between the two legs exactly one path prefix, with no compound cursor to encode and a native
`Last-Event-ID` per Host. They lose on a hard limit. A browser allows six connections per origin over
HTTP/1.1, the Hub cannot serve HTTP/2 without TLS, and the map rules out self-rolled TLS. A six-Host
user would hold every connection open on streams and then starve the `POST` that stops one of them.
That failure is silent, and it arrives first for the user who owns the most machines, in a system
whose whole premise is owning several.

A stream per Session was never in it. A user with three live Sessions would hold three connections
and still need a fourth for a Host running nothing.

**Nothing orders Events across Hosts, and nothing claims to.** Two Hosts' frames interleave on the
merged stream in whatever order they arrive. `Seq` is unique inside one Daemon and nowhere else,
which ADR 0005 already said, so a Client comparing a `Seq` from one Host against another's would be
comparing two unrelated counters. It never does, because every fold is per Session and a Session
lives on one Host.

### Five frame types, and a sixth the Hub adds

```
: keepalive

event: event
id: 9412
data: {"seq":9412,"session":"s-7f3a2c","at":1756412093118000,"kind":"PromptSubmitted","payload":{"text":"rename the handler"}}

event: event
data: {"seq":9413,"session":"s-7f3a2c","at":1756412093402000,"kind":"AssistantMessage","payload":{"text":"","complete":false}}

event: delta
data: {"seq":9413,"n":0,"text":"I'll "}

event: delta
id: 9413
data: {"seq":9413,"n":41,"text":"I'll rename it and update the two callers.","final":true}
```

| frame | carries | `id:` |
| --- | --- | --- |
| `event` | one Event: the envelope's other four fields, plus its payload | only when it advances the cursor |
| `delta` | text for an open appendable Event | on the final Delta only |
| `vendors` | this Host's Vendor reachability and its resident Models | never |
| `resync` | your cursor is outside the log | never |
| keepalive | nothing. An SSE comment line, every 10 seconds | never |

The Client's leg adds `host` for Host State, and every frame above gains a `host` field.

The keepalive is a **comment** rather than a named frame, and that is not a detail. The Hub reads raw
SSE and sees comments, so it gets the liveness signal ADR 0004 asked for. A browser's `EventSource`
discards comments before any handler runs, so the Client is never handed a measurement it has no
business making. One frame, correctly invisible to one of its two readers. The Hub sends its own to
the browser on the same 10 second beat, because an idle connection through anything in the middle
should not be allowed to look alive.

`vendors` is how ADR 0004's push survives ADR 0005's envelope. ADR 0004 said the Daemon polls its own
Vendors and pushes changes onto the Host stream. ADR 0005 said every Event carries a Session id, and
a Vendor's reachability belongs to no Session. Both hold once the push is a frame that is not an
Event: no `Seq`, never stored, never replayed. That is the same shape as a Delta, for the same
reason.

**The first `vendors` frame on a newly opened stream carries the whole current state rather than a
change.** Without that rule a Client attaching between two changes has nothing to draw and no
endpoint to ask, which is a Host row that stays blank until a Model happens to load. It is one branch
in the Daemon and it is what keeps `vendors` a push rather than a push plus a fetch.

This splits ADR 0007's two freshness contracts across two mechanisms that match them exactly. **A
`Resident` list is pushed, because it is worthless when old. A `Catalogue` is fetched from
`GET /v1/models`, because it is large, it changes when a human pulls a Model, and it may be shown
Stale.**

Pushing a `Resident` list is not the caching ADR 0007 forbids, and the reason is the reachability
field beside it. What that ADR refused was a Client showing a remembered list while nobody is
answering. Here the same frame carries whether the Vendor answered, so a Vendor that stops answering
produces a push that empties the row rather than a memory that outlives it.

### The cursor lags an open message on purpose

`Seq` and the SSE `id:` are the same number for a finished Event. For an appendable one they are not,
and the gap is deliberate.

An `AssistantMessage` is written to the log the moment the Harness starts producing it, with empty
text and its `Seq` allocated. If that frame carried `id: 9413`, a Client that dropped its connection
mid-generation would resume *after* the message, and the text that arrived while it was away would
never reach it. Deltas are not replayed, and the Event it already holds says `"text": ""`. The Client
would keep a permanently empty message with no way to ask for the rest.

So:

> The cursor is the highest `Seq` below every open appendable Event. An appendable Event is open from
> the frame that announces it until its final Delta arrives or its Session ends, whichever comes
> first.

The second half of that is not a footnote. A Session that dies mid-message never produces a final
Delta, and without the clause the cursor would be pinned behind that Event forever and every
reconnect would replay the whole Session from there. `SessionEnded` closes an open message the same
way it closes an open Tool Call, which is the second synthesis trigger ADR 0008 added to ADR 0005,
applied to the other appendable thing.

An open `AssistantMessage` or `Reasoning` frame carries no `id:`. Its Deltas carry none. The **final**
Delta carries the `id:`, because that is the moment the log holds the Event's whole text. A Client
that reconnects mid-message resumes from before it and gets the row replayed with whatever text it
holds now, which is the repair ADR 0005 designed the final Delta to perform, reached by a different
route.

With admission at one Session per Host, "every open appendable Event" is at most one, so the rule
degenerates to "the last completed Event". It is written as a minimum because a looser admission
policy puts two Sessions on one stream, and the cursor then has to lag behind the older of two open
messages. The cost is a handful of replayed Events on reconnect.

A Client checks for lost Events on `data.seq`, never on the cursor. ADR 0005's gapless counter is what
makes loss detectable by subtraction, and the cursor is deliberately not that number.

### The Hub rewrites `id:`, and that is what lets the browser hold the cursor

A browser's `EventSource` keeps exactly one `Last-Event-ID`, which is the last `id:` it saw. On a
merged stream one Host's `id:` would overwrite every other Host's, and a reconnect would resume one
Host correctly and silently restart the rest.

So the compound cursor is not something the Client assembles. **The Hub replaces the `id:` field on
every frame it forwards with the whole cursor across every Host**, and re-emits it whenever any one
Host's number moves:

```
event: event
id: desktop=9412,laptop=98
data: {"host":"desktop","seq":9412,"session":"s-7f3a2c","at":1756412093118000,"kind":"PromptSubmitted","payload":{"text":"rename the handler"}}
```

The browser then does the whole job unaided. It stores that string, sends it back on reconnect, and
the Client's JavaScript never tracks a cursor at all. The Hub splits the string, hands each Daemon a
plain integer, and the Daemon's half of the protocol stays the single number it has been throughout.

The cost is the `id:` line growing with the Host count, about ten bytes per Host on every frame that
advances anything. Three Hosts is thirty bytes against a payload measured in hundreds. A frame that
advances no cursor still carries no `id:` at all, so Deltas and keepalives, which are the frames
there are most of, pay nothing.

A Host id is therefore part of a wire format, so it is constrained rather than free text. **A Host id
matches `[A-Za-z0-9_-]+`.** The Hub rejects a profile at config load that does not, which is better
than an escaping rule nobody would test.

### The Handshake

The Hub sends `Dispatch-Protocol: 1` on every request. A Daemon that cannot serve that version
answers `426 Upgrade Required` and names what it does speak:

```json
{"reason": "protocol", "speaks": [2]}
```

The Hub marks the Host `Incompatible` and stops. ADR 0004 decided that `Incompatible` never retries,
and this is the failure that earns it. A version mismatch cannot fix itself, so retrying would hammer
a Host that can never come `Ready`. The Client shows both numbers, and the user updates one machine.

The version is one integer, and the Daemon holds the set it can serve. In v1 that set is `{1}`, so
today the check is an exact match. The set exists rather than a single number because widening it
later costs one line, and a Daemon that refuses a Hub it could have served is a Host the user has to
walk over to. This is the map's standing preference applied again: design the seam, ship the simple
implementation.

**What bumps the version, and what does not.** ADR 0005's compatibility rules already cover the Event
model, so a new kind or a new optional field changes nothing. ADR 0008 added two kinds without
anything here noticing. The version bumps for a change to the transport itself: a new frame type the
reader must understand, a changed cursor format, a removed endpoint, or a newly required field on a
command.

The handshake runs **on the stream**, not on an endpoint of its own. The stream is the connection ADR
0004 measures, so a separate `/handshake` would be a second thing to open, keep alive and get wrong.
Commands carry the same header and can fail the same way, though the Hub never sends one to a Host
that is not `Ready`.

Client to Hub has no version skew, because the Client is served by the Hub out of the same binary. It
has one stale case, which is a browser tab left open across a Hub update. The rendered page carries
the Hub's version, the JS sends it, and a mismatch is a `426` the JS answers by reloading the page.

## What the Hub does to Events in transit

> The Hub may add to a frame. It may never add to an Event.

A frame lives for the length of one TCP connection. An Event lives in a log. The Host id is a name in
the Hub's config that no Daemon ever reads, so it goes in the frame and is written down nowhere.
Event identity stays what ADR 0005 made it: a `Seq` unique inside one Daemon and nowhere else.

The ticket's worry was that annotating stops Event identity being purely a Daemon concern. It does
not, because the annotation is routing information rather than content. A Client that keeps the Host
id beside an Event it has rendered is storing its own knowledge of where the Event came from. It is
not storing a sixth envelope field, and no Daemon could ever have supplied one.

The payload is forwarded byte for byte. The Hub does not deserialise it, does not validate it, and
does not know which kinds exist. That is what keeps ADR 0005's append-only rule true across a
component in the middle: a Daemon that starts writing a seventeenth kind reaches a Client through an
unchanged Hub.

## The Event log

### The schema

```sql
PRAGMA journal_mode = WAL;
PRAGMA synchronous  = NORMAL;

CREATE TABLE events (
  seq     INTEGER PRIMARY KEY,  -- per Daemon, from 1, gapless. This is SQLite's rowid
  session TEXT    NOT NULL,
  at      INTEGER NOT NULL,     -- Unix microseconds, on the Daemon's clock
  kind    TEXT    NOT NULL,
  payload TEXT    NOT NULL      -- JSON. One shape per Kind, and nothing above repeated
) STRICT;

CREATE INDEX events_by_session ON events (session, seq);

CREATE TABLE meta (
  id   INTEGER PRIMARY KEY CHECK (id = 1),
  high INTEGER NOT NULL         -- the highest Seq ever allocated on this Host
) STRICT;
```

### Which columns are real

The split was already written down. **The envelope is columns and the payload is JSON**, which is ADR
0005's five fields with the fifth left as text. There is no judgement call to make here, and that is
the payoff for having fixed the envelope first.

The test for promoting anything out of the payload is whether the Daemon ever puts it in a `WHERE`
clause. Four of the five pass. `seq` orders and resumes. `session` scopes every read. `at` is what
retention would compare if it worked by age. `kind` answers the one structural question the Daemon
asks the log, which is which Sessions have no `SessionEnded`. `payload` is the fifth, and nothing
ever filters on it.

**`kind` is a column and not an index**, which is the one place the two are worth separating. Only
one query reads it, the boot sweep below, and it runs once per boot over a log with a few hundred
thousand rows in it. That is a full scan measured in milliseconds, against an index that would be
maintained on every insert for the rest of the Daemon's life. The write path is the thing this design
protects, so the scan wins.

`toolCallId` is the field that looks like it should be a column and is not. The ledger of open Tool
Calls belongs to a live Session, and ADR 0008 keeps it in the Daemon's memory. After a restart every
live Session is `lost`, and the boot sweep closes open calls by folding each abandoned Session rather
than by querying for an id. The column would be indexed for a query nobody writes.

`seq` is `INTEGER PRIMARY KEY`, which in SQLite is the rowid itself rather than a second B-tree. Rows
are stored in `Seq` order, which is the order every read in this design wants, so a replay from a
cursor is a range scan on the table's own key.

`kind` is text rather than an integer enum. It costs about a dozen bytes a row, and it buys ADR 0005's
compatibility rule something real. A log written by a newer Daemon and read by an older one shows
`"SessionReady"` rather than `15`, so an unknown kind is a neutral row a human can also read. An
integer needs a mapping table to stay right forever, and the first time it is not, the log is
undecodable rather than merely unfamiliar.

`at` is Unix microseconds as an integer. It sorts and compares without parsing, and no reader has to
agree with a writer about a text format.

**One table, not two.** A `sessions` table was the obvious second one, and every column it would hold
is either derived state that ADR 0005 and ADR 0008 both forbid storing, or a copy of what
`SessionStarted` already carries. The boot sweep is one query, and it is the scan just argued for:

```sql
SELECT DISTINCT session FROM events
WHERE session NOT IN (SELECT session FROM events WHERE kind = 'SessionEnded');
```

### Two writes per message, not one per token

ADR 0005 made `AssistantMessage` and `Reasoning` appendable and left this ADR the write path. The
answer:

> The Daemon accumulates an open message's text in memory and writes it once, when the message
> completes.

So an assistant message is two writes. An `INSERT` with empty text when the Harness starts producing
it, which allocates the `Seq` and lets the frame go out, then one `UPDATE` when the final Delta
arrives. Pi sent 143 frames for one short turn, and rewriting the row's JSON on each of them is the
write amplification ADR 0005 kept Deltas out of the log to avoid. Doing it in SQLite instead would
have given all of it back.

**A crash mid-message loses that message's text, and the row keeps `complete: false`.** That is the
cost, stated rather than buried. Three things make it the right one. The crash ends the Session
anyway, so the loss is always the last message of a Session that is over. The raw bytes are in the
per-Session transcript file ADR 0005 already requires, so nothing is gone for a human reading back.
And the Client already draws `complete: false` as a torn message, so the shape needs no new handling.

Flushing on a timer was the alternative, and it loses on the argument this project keeps making. It
is a knob, its correct value is unknowable, and it pays out only in the case where the Session is
already dead.

### The counter across a boot and a prune

`Seq` is gapless from 1 and per Daemon, so it has to survive both a restart and a retention pass.

```
next = max(coalesce(max(seq), 0), meta.high) + 1
```

`meta.high` is written at boot and after every retention pass, never per Event. A crash between the
last Event and the meta row can only leave the stored number too low, and taking the larger of the
two covers that. Zero cost on the write path, one row of durable state.

The case it exists for is narrow and nasty. Retention deletes whole Sessions, so a log whose Sessions
have all aged out is an empty table. Without `meta.high` the counter restarts at 1, and a Hub
reconnecting with a cursor of 40 receives Events 41 to 50 that are **different Events** from the ones
it saw at 41 to 50 before. Not a gap, not an error, just quietly wrong history. The resync rule below
catches a cursor above the log's maximum but not one landing inside a reused range, so it is not
enough on its own.

The counter advances only on a committed insert. There is exactly one writer, so this is a serialised
increment rather than a concurrency problem. A failed write consumes no number. It cancels the
Session's context, which is the path ADR 0006's `Sink` already defined for a Daemon that cannot write
an Event.

Reloading the counter is ADR 0008's inherited requirement, and it runs before `DaemonStarted` is
written.

## The write path

**Committed, then sent.** In that order, for every Event, with no concurrent option.

The reason is not durability for its own sake. It is that a Client's fold and the log must never
disagree, because every replay repairs the Client *toward the log*. Send-first means a Client can
render an Event, lose its connection, replay, and watch that Event vanish. An Event arriving a
fraction of a millisecond later is not a cost anyone perceives. An Event that appears and then
disappears is a bug report.

What "committed" means is worth being exact about, because `synchronous = NORMAL` is a deliberate
choice and not a default nobody looked at. In WAL mode with `NORMAL`, a commit appends to the
write-ahead log without an `fsync`, and the `fsync` happens at a checkpoint. A **process** crash loses
nothing, because the WAL is in the operating system's cache and survives it. A **machine** crash can
lose the last few Events.

That is the right trade because of what a machine crash also does. It kills every Harness on that
Host, so every Session it interrupts is `lost` on the next boot whatever the log says, and the Events
at the tail describe a Session that no longer exists. `synchronous = FULL` would buy an `fsync` per
Event for a guarantee the same crash already voided.

Deltas are the exception that makes the rule affordable. **A Delta is sent and never written.** So the
disk sees two writes per assistant message and nothing at all per token, and the ordering rule costs
nothing on the path carrying the most bytes.

The raw transcript is a separate file with its own rotation, which ADR 0005 explicitly kept out of
this ADR's retention. Nothing here writes it and nothing here prunes it.

## Replay

### The guarantee

> Ordered and gapless within one Host. At least once. The Client's fold makes it exactly once,
> because `Seq` is idempotent.

Ordering is free: one writer per Host, one TCP connection per Host. Gapless is ADR 0005's counter.
At-least-once is the honest ceiling, because a Hub can receive frames and die before its cursor is
anywhere, then reconnect from an older one and get them again.

The Client's idempotency requirement is one sentence and one line of code. **Apply an Event keyed by
`Seq`, and ignore a `Seq` already applied.** A fold over a set ordered by `Seq` gives the same answer
however many times a member is offered, so nothing else in the Client has to be careful.

Deltas have their own guarantee, and stating one guarantee for the whole stream would be wrong. **A
Delta is at most once and is never replayed.** ADR 0005's rule is what makes that safe: a Delta never
carries information its Event will not eventually hold, so losing every Delta costs liveliness and
never content. The final Delta repairs a Client that missed some, and the log repairs a Client that
missed all of them.

### Joining the stream without a race

The obvious implementation has a hole in it. Read the log up to its maximum, then subscribe to live
Events, and lose everything written in between.

So the order is reversed. **Subscribe first, then read the log, then drop from the buffer anything at
or below the last `Seq` read.** The subscription buffers into a bounded channel while the reader
drains its range scan.

If that buffer overflows, the Daemon **closes that connection**. It never blocks the writer and never
grows a buffer to accommodate a reader that cannot keep up. The Hub reconnects with its cursor and
catches up from the log, which is a path it already has, so a slow reader costs one reconnect and no
special handling anywhere.

**A reader that is catching up receives no Deltas.** They start when it goes live. A Delta for an
Event the reader has not reached is text with nowhere to go, and dropping it is free for the reason
above. This also settles the one awkward moment. A reader that catches up to an open message and goes
live sees Deltas with an `N` far ahead of its own count, ignores them exactly as ADR 0005 instructs,
and is repaired by the final Delta.

### Concurrent readers

A replaying Client and a live one on the same Session never meet. SQLite in WAL mode gives every
reader a consistent snapshot without blocking the writer, so the range scan one connection is doing
does not slow the `INSERT` another connection's Events are waiting on.

Each connection is the same two things in the same order: one subscription and one range scan. There
is no shared cursor, no per-Session reader registry, and nothing that has to know how many readers
exist. The Daemon fans one write out to every live subscriber and answers every catch-up from the
same table.

Retention is the only thing that could have made a reader race a deletion, and it does not, because
it runs at boot.

### Resync is a frame, not an error

ADR 0004 left this open. When a `Last-Event-ID` names a `Seq` the log cannot serve, either below the
oldest surviving row or above the highest allocated, the Daemon opens the stream anyway and makes its
first frame:

```
event: resync
data: {"oldest":9001,"latest":9420}
```

The Client throws away what it holds for that Host, refetches with `GET /v1/sessions`, and continues
from the cursor that call returns.

**It is not an HTTP error, and that is the whole decision.** The stream is the connection ADR 0004
measures for presence. Failing the request would drop the Host to `Connecting` and then to `Down`,
telling the user their machine is unreachable when what actually happened is that a log rotated while
they had a tab open. A Host serving a resync is working perfectly. It stays `Ready`.

This is also why every `GET` in the command set returns a cursor beside its data. A snapshot without
one has a race in front of it: read the Sessions at `Seq` 9420, open the stream at the live edge which
is now 9423, and silently lose three Events. So the snapshot names where it read to, and the Client
opens the stream there.

> The stream resumes. It does not browse.

A cursor from last week is a resync, not a replay, and history older than the log is not what the
stream is for. `GET /v1/sessions/{id}/events` is, and it is a paged read of one Session with no live
part at all.

That rule also settles what the Hub does when a Client attaches with a cursor older than the Hub's own
on a Host. The Hub does not buffer and has no store, so it reopens that Host's stream at the older
cursor. Reopening is the reconnect it already knows how to do, and it is bounded by one Client
disconnect, because anything further back is a resync.

### After a Daemon restart

Nothing here is a special case, and that is the result worth having.

The Daemon boots, reloads its counter, runs ADR 0008's sweep, and starts listening. A Hub reconnects
with the cursor it held and receives the gap: whatever the Session was doing before the process died,
then `DaemonStarted`, then `ApprovalDecided{refused, by: daemon_restart}` for open questions, then
`ToolCallEnded`, then `SessionEnded{lost}`. The Client folds that to `Ended{lost}` with the whole
transcript intact, and offers to start a new Session in the same directory on the same Model. ADR
0008 was firm that this is never called resume.

**The sweep finishes before the Daemon accepts a connection.** Otherwise a reconnecting Hub reads a
Session that is `Working` in the log and dead in reality, and the Client draws a spinner for a
process that no longer exists. Two lines of ordering at boot, and the alternative is a state the
Client cannot tell from a working one.

The Hub sees a gap with no `HubDetached` in it, because the Daemon was dead and could not write one.
That asymmetry is ADR 0004's and it is deliberate. `HubDetached` says the Hub stopped listening, and
a Daemon that crashed observed nothing at all. `DaemonStarted` is the marker on that side, and the
two together cover both shapes of gap.

## Retention

**Whole ended Sessions, the most recent 200 kept, pruned at boot.**

By Session, because a Session is the unit a fold makes sense over. Deleting a Session's first three
hundred Events would leave a transcript that folds to nonsense: tool calls with no requests, a policy
nobody set, a Prompt with no words in it. A Session is either wholly readable or wholly gone, and
that is a sentence the Client can act on.

By count rather than by age. A count bounds the disk without a clock, and a user who runs nothing for
a month comes back to their history rather than to an empty log. Age deletes the work someone was in
the middle of thinking about, which is the opposite of what a history is for.

Never a Session without a `SessionEnded`, whatever its age. A live Session is not old, it is slow, and
ADR 0008 was clear that the Daemon concludes nothing from how long something has taken.

200, with no configuration knob, following ADR 0008's admission argument. A knob nobody sets is
complexity with a default attached. The number lives in one constant.

**At boot, not on a timer.** The Daemon already holds an exclusive write to the log at boot and no
reader is attached, so retention cannot race a replay. A background pruner would have to know which
ranges a live reader is scanning, or accept a reader hitting a deleted range mid-scan, and the resync
path would then fire during ordinary operation rather than only after a real rotation. One scheduling
decision removes a class of bug instead of managing it.

The cost is real and small. A Daemon that runs for six weeks never prunes, and the log grows for six
weeks. One user's Sessions are text, a few megabytes a week at most, on a machine with a GPU in it.

**What retention does to the guarantee**, plainly: replay is at-least-once and ordered *within the
retention window*. A cursor pointing before it is a resync. That is the boundary, it has one name,
and it is the same name the log-rotation case already had.

One coupling is worth writing down, because it is invisible until it breaks. **Deleting a Session
leaves the surviving log gapless only because admission runs one Session at a time**, which makes a
Session's Events a contiguous run of `Seq` and a deletion a truncation at the front. Two concurrent
Sessions interleave their `Seq`, and deleting the older one then punches holes through the newer
one's range, so a reader subtracting `Seq` would report loss that never happened. Loosening admission
means retention has to delete by `Seq` boundary rather than by Session, or ADR 0005's gapless
promise stops holding. This is the same shape as ADR 0008's note about the per-Session
`opencode.json`: something that is safe only while one Session at a time is the policy.

## Considered options

- **Two protocols, one for the Client and one for the Daemon.** Each fits its own view exactly.
  Rejected: the only real difference is which Host, and writing it twice means every future endpoint
  is added twice and drifts once.
- **The same protocol with no Host id at all**, with the Hub holding a connection per Host and the
  Client addressing Sessions by id alone. Rejected: Session ids would then have to be globally
  unique, which means a Daemon minting an id that is unique across Hosts it is forbidden to know
  about.
- **One SSE stream per Session.** The cleanest cursor, since `Last-Event-ID` would be per Session and
  a per-Session `Seq` would have worked. Rejected by ADR 0004 before this ticket existed: presence is
  the Host stream's liveness, so a Host with no Sessions would need a second connection to be measured
  at all.
- **One stream per Host to the browser.** Native `Last-Event-ID` per Host, no compound cursor, and the
  Hub becomes a pure path router with one difference instead of three. Rejected: six connections per
  origin over HTTP/1.1, no HTTP/2 without TLS, and the map rules out self-rolled TLS. A six-Host user
  starves their own commands, silently.
- **The Hub buffering Events so a Client can rewind further than the Hub's own cursor.** Rejected: a
  second store with a second retention policy, holding a copy of what the Daemon already holds
  durably.
- **A `host` field inside the Event envelope.** It would make a merged stream trivial. Rejected: ADR
  0005 fixed five fields, and a Daemon cannot fill this one, because a Host's identity is a name in
  the Hub's config.
- **Sending an Event before writing it, or concurrently.** Faster by a fraction of a millisecond.
  Rejected: a Client would render Events a later replay removes, and every replay in this design
  repairs toward the log.
- **`synchronous = FULL`.** A real durability guarantee per Event. Rejected: the crash that would lose
  a WAL page also kills every Harness on the Host, so the Events it loses describe Sessions that are
  `lost` on the next boot anyway.
- **Writing an open message's text on every Delta.** The log then holds a partial message across a
  crash. Rejected: 143 row rewrites for one short Pi turn, which is the amplification Deltas exist to
  avoid.
- **Flushing an open message on a timer.** Bounds the loss. Rejected: a knob with no knowable correct
  value, paying out only when the Session is already over.
- **A `sessions` table beside `events`.** Rejected: every column is either state ADR 0005 and ADR 0008
  forbid storing, or a copy of `SessionStarted`.
- **`toolCallId` as a real column.** Rejected: the open-call ledger lives in memory for a live Session
  and is folded from Events after a restart, so nothing ever queries it.
- **Retention by age.** Rejected: it deletes what a user left a month ago and came back to, and it
  needs a clock to bound a disk.
- **Retention deleting the oldest Events rather than the oldest Sessions.** Rejected: a half-deleted
  Session folds to nonsense, and the guarantee stops being statable in a sentence.
- **A background retention pass.** Rejected: it races live readers, and moving it to boot deletes the
  race rather than managing it.
- **Failing the stream request when a cursor is unknown.** Rejected: it flaps the Host to `Down` for a
  log rotation, telling the user to go and check a machine that is working.
- **Idempotency keys on commands.** Rejected: the Session state machine already refuses five of the
  six duplicates, and admission refuses the sixth.

## Consequences

- The Daemon's half of the protocol is exercisable with `curl` against loopback, with no Hub, no SSH
  and no browser. That is the seam ADR 0004's `HostDialer` was aiming at, reached from the other
  side.
- The Hub has no database and holds nothing durable. Its cursors are in memory, Host State is derived
  as ADR 0004 requires, and a Hub restart recovers everything from the Clients' own `Last-Event-ID`
  values and the Daemons' logs.
- [#11](https://github.com/VictorJohnOkoh/Capstone/issues/11) inherits one `EventSource` for every
  Host, six frame types, and a cursor it lets the browser manage rather than tracking itself. It
  applies Events by `Seq` and ignores repeats, checks for loss on `data.seq` and never on the cursor,
  and answers a `resync` frame by refetching rather than by showing an error. Its first paint is
  server-rendered by the Hub, and the `GET` endpoints exist so it can resync without a page reload.
- #11 also gets the answer to how an approval on another Host interrupts. Every Host's Events are on
  the one stream the Client already holds, so nothing has to be subscribed to per Session.
- [#12](https://github.com/VictorJohnOkoh/Capstone/issues/12) gets another package boundary. The frame
  types and the endpoint set are shared between the roles; the Host id belongs to the Hub alone and
  never appears in the Daemon's types, which is one structural way a Daemon is stopped from learning
  about peers.
- The log is one SQLite file per Host: two tables, one index, and five columns on `events` of which
  four are ever filtered on. Everything else in it is JSON that only the Daemon and the Client parse.
- Retention deleting whole Sessions keeps the log gapless only while admission allows one Session at
  a time. Loosening admission reopens it, alongside the `opencode.json` coupling ADR 0008 named.
- The write path is two disk writes per assistant message and zero per token. That number is what
  makes "committed before sent" affordable, and it is worth rechecking if Deltas ever gain a durable
  cousin.
- A machine crash can lose the last few Events, and a crash mid-message loses that message's text.
  Both are named costs, both land on a Session that is `lost` on the next boot, and both leave the raw
  transcript file untouched.
- Sessions older than the most recent 200 are gone, including from the Client. There is no archive and
  no export.
- `CONTEXT.md` gains **Cursor**, **Frame** and **Resync**. **Handshake** gains the status code and the
  version set, and **Event** gains the write order and the retention rule. **Sequence Number** loses
  `cursor` from its list of words to avoid, because a Cursor is now a different number with a
  different job, and conflating the two is the mistake the entry has to warn against instead.
- ADR 0004's open question is closed. A `Last-Event-ID` the Daemon cannot serve is a `resync` frame on
  a `Ready` Host, not a failure.
- ADR 0005's open write-path questions are closed. `Seq` is allocated inside the insert, an open
  message's text does not survive a crash, and the Hub annotates the frame rather than the Event.
- ADR 0007's request-and-response path is `GET /v1/models` for a `Catalogue` that may be Stale, and
  the `vendors` frame for a `Resident` list that may not.

**ADR 0005 is amended in one sentence, and the amendment is worth naming rather than smoothing over.**
That ADR says `Seq` "is both the Event log's primary key and the `Last-Event-ID` offset", which reads
as an identity. It is not one any more. `Seq` is still the primary key, and the cursor is still built
from `Seq` and from nothing else, but the two are different numbers while a message is arriving.

The lag is what buys ADR 0005's own repair rule its last case. That ADR made the final Delta carry
the whole text so a Client that dropped a Delta fixes itself, and it works perfectly while the
connection holds. It cannot cover a Client that was away when the final Delta went out, because
Deltas are not replayed. Making the cursor lag hands that case back to the log, which is where every
other recovery in this design already ends up. So the amendment is that document's argument
continued, not reversed.

`CONTEXT.md` follows, and **Sequence Number** loses `cursor` from its list of words to avoid. That
entry was right to forbid the confusion when the two were one number. Now that they are two, the
useful warning is that they are not interchangeable, which is what the entry says instead.

The rest of ADR 0005 stands. The envelope is unchanged and gains no sixth field. `Seq` stays per
Daemon, gapless, allocated inside the write transaction. Deltas stay out of the log. What this ADR
adds is where the boundary falls between a stored Event and a sent frame, and it falls in the one
place that keeps both documents true: the Hub writes to frames and never to Events.
