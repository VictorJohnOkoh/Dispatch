# Five Session states, one process each, and a gate that claims only what the Daemon allowed

A Session could have carried a rich state machine, with a queue in front of it, a supervisor timing
its silences, and a Workspace Root advertised as a sandbox. Instead it has five states that all fold
out of its own Event log, no queue at all, no timer that concludes anything, and a containment story
that names the two things it bounds and the two it does not. We chose this because every richer answer
in that list would have been a claim the captures cannot support, and a lifecycle that overclaims is
worse than a small one that does not.

Three decisions carry the rest. One Harness process holds one Session, because Pi cannot do otherwise
and a second supervision shape would undo ADR 0006. A Session never survives a Daemon restart, and the
Client says so in those words. The Approval Policy guarantees what the Daemon allowed, never what the
Harness ran.

## What the captures decide

Four facts, and only one of them is about a state machine.

**Pi is one Session per process and OpenCode is not.** `pi --mode rpc` emits a `session` header line
carrying `{version, id, timestamp, cwd}` once, at the top of the stream, and has no session verbs at
all. `opencode acp` advertises `sessionCapabilities: {close, fork, list, resume}` and mints a fresh id
on every `session/new`. So multiplexing is available on one of the two Harnesses that ship.

**A silent Harness measures its supervisor.** Hermes' Windows hang lasted 118.83s against a 120s
client timeout. Across eight runs, at six timeout values from 120s to 900s and on three different
tools, the hang always landed within about 6s of whatever the client's timeout had been set to. The
worst gap was 7.28s and the closest was 1.17s. In the other direction, the 2026-08-28 llama-swap
capture took 29.70s to first chunk against 1.26s and 1.27s for the other two runs, because the Vendor
was loading the Model. A threshold that catches the first case kills the second.

**Neither Harness reports a refusal the Daemon can read.** Hermes returns `denied by ACP client` for a
human's refusal and for an internal scheduling failure alike, and denied three edits out of three
without sending `session/request_permission` at all on Windows. Pi reports a refusal as
`isError: true` with `Blocked by user` as free text its extension author chose. There is no structural
refusal signal on either.

**A gate can fail to install and say nothing.** Hermes' shell hook returns "no block" on every failure
path in `agent/shell_hooks.py`: crash, missing, not executable, timeout, and non-zero exit. It also
needs first-use consent recorded in `~/.hermes/shell-hooks-allowlist.json`, and a non-TTY caller that
does not pass `--accept-hooks` never registers the hook at all. Pi has the same shape in a different
place. It gates nothing until an extension is loaded, and the extension is a command-line argument that
can fail.

## The state machine

ADR 0004's test applies unchanged. A state earns its place when the Daemon behaves differently, and a
distinction that only changes the Client's wording is a field.

```go
package session

// State is what a Session is doing now. Derived by folding its Events, never stored,
// and the same fold runs in the Daemon and in the Client.
type State uint8

const (
	Starting State = iota // launching. No Prompt may be submitted
	Idle                  // up, nothing in flight. A Prompt may be submitted
	Working               // a Prompt is in flight. A second Prompt is refused
	Asking                // a Tool Call is held, waiting for the user's decision
	Ended                 // terminal, carrying stopped, failed or lost
)
```

| State | The fold that produces it | What the Daemon does differently |
| --- | --- | --- |
| `Starting` | `SessionStarted` with no `SessionReady` | Refuses a Prompt. Accepts a stop, which abandons the launch |
| `Idle` | `SessionReady`, no Prompt open | Accepts a Prompt |
| `Working` | A `PromptSubmitted` with no `PromptCompleted` | Refuses a second Prompt. Accepts an Interrupt |
| `Asking` | An `ApprovalRequested` with no `ApprovalDecided` | Accepts a decision. A stop must answer the question before it ends the Session |
| `Ended{reason}` | A `SessionEnded` exists | Accepts nothing. The history stays readable |

`Asking` is more specific than `Working` and both folds are true at once, so the fold answers `Asking`
first. Parallel Tool Calls can leave two questions open, and the Session stays `Asking` until the last
one is decided.

Measured against the eight states the ticket offered, four survive and four do not.

**`requested` and `admitted` are not states**, because admission runs before the Session exists. A
refused start writes no Event, has no Session id, and leaves nothing in the Event log. The Session
begins at `SessionStarted`, which the Daemon writes only once admission has passed.

**`stopping` is not a state**, because it is the inside of one operation. ADR 0006's shutdown ladder
ends in a kill of the process group, so stopping is bounded by a short fixed wait and finishes faster
than a Client reconnects. Making it a state would need a new Event kind to carry a condition that lasts
about a second. Instead the Daemon serialises it. A Session stops once, and a second stop request joins
the first rather than starting another.

**`crashed` is not a state**, it is `Ended{failed}`. The Daemon does nothing different for a Session
that crashed and one the user stopped. Both are terminal, both keep their history, and neither
restarts. This is the same collapse ADR 0004 made when it folded `unreachable` and `no-daemon` into one
`Down{cause}`.

## No transition is internal

Every transition is caused by an Event, and there are no others.

| From | Trigger | To |
| --- | --- | --- |
| nothing | `SessionStarted` | `Starting` |
| `Starting` | `SessionReady` | `Idle` |
| `Idle` | `PromptSubmitted` | `Working` |
| `Working` | `ApprovalRequested` | `Asking` |
| `Asking` | `ApprovalDecided`, with no other question open | `Working` |
| `Working` | `PromptCompleted` | `Idle` |
| any | `SessionEnded` | `Ended{reason}` |

The ticket asks which transitions produce Events and which are internal. The distinction cannot exist
here. ADR 0005 made Session state derivable by folding the log, so a state reached without an Event is
a state the Client cannot draw, a reconnecting Hub cannot recover, and a restart cannot see. It would
be a fact held only in the Daemon's memory, which is the one place this design already decided not to
keep Session state.

Two things do happen without an Event, and neither is a transition. Admission runs before the Session
exists. The shutdown ladder runs between the last Event and `SessionEnded`.

`SessionReady` is a new kind. It is here because `Starting` needs a trigger, and because the Model the
Harness actually selected has nowhere else to land. ADR 0006 requires the Adapter to read that back
before `Start` returns, and until now the log kept only the Model the Daemon asked for.

| kind | written by | payload | appendable |
| --- | --- | --- | --- |
| `SessionReady` | Daemon | the Model id, as the Harness reported it | no |
| `DaemonStarted` | Daemon | none | no |

`SessionReady` carries the Harness's own spelling rather than a normalised one. OpenCode reports
`capstone/qwen3.5-9b` on `session/new`, provider included, so the Vendor that served the Session is
visible in the string. The Adapter decides whether that answer names the Model the Daemon asked for,
because only the Adapter knows how its Harness spells things, and the raw string is logged so a human
can check that judgement later. ADR 0006 found OpenCode reporting `opencode/big-pickle`, a hosted model
nobody asked for, in six runs whose own config named a `capstone/` Model. That is what this field is
for.

`DaemonStarted` is the one ADR 0004 asked for. It is written on boot into every Session that had no
`SessionEnded`, which is the same shape as `HubDetached`: the Daemon writing about itself, into each
Session's log. A restart with nothing live writes it nowhere, because no Session has a gap.

### A launch that fails ends a Session, and this sharpens ADR 0006

`SessionReady` never arriving is a state the log has to describe, because `SessionStarted` was written
before the launch began. So a failed launch is not nothing:

1. `Error{harness_failed}`, with whatever the Adapter reported and the tail of the transcript.
2. `SessionEnded{failed}`.

The Session is `Ended{failed}` and it never reached `Idle`. No Prompt was ever submitted, so there is
nothing else to close.

ADR 0006 says twice that a failed `Start` means "no Session exists", and read literally that is now
wrong. It was written from the Adapter's side, where it is exactly right: the Adapter returns an error,
holds no process, and hands back nothing runnable. What this ADR adds is that the Daemon had already
opened a record, so the *record* exists and the *Session* does not. The two readings agree once they
are separated, and the distinction is worth the paragraph, because it is the difference between a
launch failure the user can see and one that vanishes.

The reason for that ordering is worth restating here. A cold Model load took 29.70s in the captures. A
Daemon that wrote `SessionStarted` only after a successful launch would leave the user watching nothing
for half a minute, with no Session id, nothing to cancel, and no record at all when it failed.

Sixteen kinds now. This extends the Event model under ADR 0005's own compatibility rules, which allow a
new kind and require readers to draw an unknown one as a neutral row. It does not reopen it.

## One process per Session

`opencode acp` could have held three Sessions. It will hold one.

The reason is not cost. Pi has no session verbs at all, so multiplexing would be an OpenCode-only path,
and the Daemon would then supervise two shapes: one process to one Session, and one process to many.
ADR 0006 put process supervision in the Daemon precisely because every process rule is identical across
Pi, OpenCode and Hermes. A second shape gives that back.

Three smaller reasons point the same way.

The working directory is a process property. Pi stamps `cwd` once in its `session` header, so two
Sessions in one Pi process share one directory. ADR 0003 needs a per-Session `opencode.json` beside the
working directory for the Model to be chosen per Session, and the capture confirms that file merges
with the user's global config rather than replacing it. Multiplexing removes the per-Session working
directory, and the per-Session Model goes with it.

One fault takes every Session in the process. Hermes' phantom denial only appears once another tool has
already run in that process, and the research correlates it with a thread-local approval callback that
Hermes' own source warns about. The mechanism was never proven, so this is a suggestive case rather
than a proof. What it suggests is state leaking between tool calls inside one process, and multiplexing
would invite the same leak between Sessions.

The expensive thing is not the process. A Node or Python process is a hundred megabytes or so. Loading
the Model took 29.70s and gigabytes of VRAM, and that cost sits in the Vendor, where it is shared
whatever the Daemon does with processes.

What it costs, stated plainly. With admission set to one Session at a time, this decision costs nothing
today, because one Session means one process either way. The bill only arrives if admission is later
loosened, and then it is one process per concurrent Session. That is the right trade now, because the
alternative buys nothing until a policy that does not exist yet says otherwise.

## What the Daemon concludes from a silent Harness

Nothing. There is no supervision timeout, no health check on the Harness, and no hung state.

ADR 0006 fixed the rule and this is the second place it applies:

> A timer may end a Session. It may never diagnose one.

The evidence is the pair of measurements above. Hermes' hang tracked its client's timeout across eight
runs, so a timeout used as a measurement measures itself. llama-swap's cold start took 23 times longer
than its warm one on the same Harness and the same Model, so any threshold under 30s kills an ordinary
first prompt and any threshold over it is useless against a hang.

So a Session that goes quiet stays `Working`. The Client renders how long it has been since the last
Event and offers a stop, which needs no new field because every Event carries the Daemon's timestamp.
The user has context the Daemon does not. They know whether they asked for something slow.

Two things do act, and neither is a timer reading silence. The process exiting is an observation rather
than an absence, and ADR 0006 makes any exit before `Close` a failure whatever the exit code. Step five
of the shutdown ladder is a fixed short wait before the kill, and it ends a Session that is already
ending. It concludes nothing.

## Crash, restart, and what does not survive

**A Harness crash surfaces and stops. The Daemon never restarts one.**

The Harness holds the message history in its own process memory, so a restarted Harness starts blank.
Restarting would mean silently starting a different Session under the same id, which looks like a
feature until someone reads the transcript. OpenCode has `session/resume` and Pi has nothing, so a
restart that kept history would work on one Harness, which is the multiplexing argument again. And
nothing separates a crash worth retrying from one that repeats. Hermes' launch failure over SSH was
reproducible, and a restart loop would have turned that into a storm.

Does the answer differ by how far the Session progressed? No, and that is deliberate. A crash at the
first frame and a crash at the nine hundredth both leave a process that no longer holds the
message history. What differs is how much the log already holds, and that is preserved either way. So
progress changes what the user sees and not what the Daemon does.

The sequence on a crash:

1. `Error{harness_failed}`, with the exit code and the tail of the transcript in its `message`. ADR
   0005 fixed that payload at a code and a message, and neither needs a new field.
2. `ToolCallEnded{unknown}` for every Tool Call still open.
3. `SessionEnded{failed}`.

**A Daemon restart makes every live Session `lost`, and a lost Session is not resumable.** ADR 0005
fixed that sequence and this ADR adds `DaemonStarted` at the front of it. On boot, for each Session
with no `SessionEnded`:

1. `DaemonStarted`.
2. `ApprovalDecided{refused, by: daemon_restart}` for every open question.
3. `ToolCallEnded{refused}` for those calls, and `ToolCallEnded{unknown}` for any other open call.
4. `SessionEnded{lost}`.

ADR 0005's synthesis rule fires at `PromptCompleted` and at `SessionEnded`, whichever comes first.

**This contradicts ADR 0005 as written, and the contradiction is worth stating rather than smoothing
over.** That ADR says "One trigger, one place, no per tool kind special case", and this is a second
trigger. Two things make it the right correction rather than a loosening.

ADR 0005 does not follow its own sentence. Its truncation shape is "`Error{stream_truncated}`, then a
synthesised `ToolCallEnded` for anything open, then `SessionEnded{failed}`", which closes open calls on
a path that never reaches `PromptCompleted`. So the second trigger was already there, unnamed, in the
same document.

Without it the model breaks its own invariant. ADR 0005 states that every `ToolCallRequested` is
followed by exactly one `ToolCallEnded`, and calls that "an invariant of the model, not a hope about
Harnesses". A Session that ends mid-Prompt, which is every crash, every stop and every Daemon restart,
leaves calls open forever under one trigger alone.

What survives untouched is the half of the sentence that was doing the work. There is still one place
and still no per tool kind special case. What changes is that "the Session is over" joins "the Prompt
is over" as a reason to close what is open, and both are moments the Daemon already writes an Event
for.

What survives is the history, not the Session. The Client shows a Session that ended with reason
`lost`, its whole transcript readable, and an action that starts a **new** Session in the same working
directory on the same Model, with a new id and no message history. That action is worth having and it must
never be called resume. ADR 0005 already said why the Session itself cannot come back: a handle to a
child process cannot be folded out of a log.

OpenCode could in principle do better, since it has `session/resume` and mints a stable id
(`ses_fbaced840ffecF2q3xGDWdrzPa` in the captures). Rejected for v1, because Pi cannot, and a Session
that resumes on one Harness and not the other is a property the Client cannot describe in a sentence.
If it is ever wanted, the Harness's native session id lands on `SessionReady` beside the Model, and
nothing else here changes.

## Stopping

**The user stops a Session, and so does the Daemon. Nothing else does.**

The user stops it through the Client. This is a single-user system, so there are no roles to model and
inventing them would answer a question nobody asked. The Daemon stops a Session on a Harness exit, on
boot for a Session it can no longer own, and during its own clean shutdown. A clean Daemon shutdown
ends its Sessions `stopped`. An unclean one leaves them to be swept `lost` on the next boot, which is
the same two paths as above.

The Hub may not stop a Session. A Hub that disconnects writes `HubDetached` and changes nothing, which
ADR 0004 settled.

**Stopping is graceful then forced, in that order**, and the order is ADR 0006's ladder with two steps
in front of it:

1. If a question is open, `ApprovalDecided{refused, by: session_stopped}`. The Harness is blocked on
   that answer, so a stop that skips this step leaves it blocked until the kill.
2. `ToolCallEnded` for every open call. Refused for the ones just refused, unknown for the rest.
3. `Run.Close`, so the Adapter sends its protocol's goodbye.
4. The Daemon closes stdin. This is the step both Harnesses answer, and the Hermes hang went from
   118.83s to 1.18s when stdin was closed before the tool ran.
5. A fixed short wait.
6. Kill the process group, not the process.
7. `SessionEnded{stopped}`.

A stop does not interrupt the Prompt first. `Run.Interrupt` exists to abandon a Prompt and keep the
Session, which is a different thing the user asks for with a different button. A stopped Session gets
no `PromptCompleted` and its last `AssistantMessage` stays `complete: false`, which is the torn message
ADR 0005 defined and the Client already draws.

**Stopping a Session that is still `Starting` skips to step 4.** The ladder above assumes a `Run`, and
during a launch there is not one yet: `Adapter.Start` has not returned, so the Daemon holds a process
and no protocol handle. There is nothing to say goodbye to and nothing open to close, so the Daemon
cancels the context it passed to `Start`, closes stdin, waits, and kills the group. `Start` returns
whatever it returns and the Daemon discards it. The Session ends `stopped`, not `failed`, because the
user asked for this. Being able to stop a launch is the reason `Starting` is a state at all, and a
29.70s cold Model load is the reason it is worth the Event.

Because step 6 is a kill, a stop always finishes. That is what lets stopping be an operation rather
than a state.

## Admission

Admission runs in the Daemon, before any Event is written, and it is the whole reason `requested` is
not a state.

```go
package admission

// Policy decides whether one more Session may start on this Host. It is asked once,
// before the Session exists, and never again.
type Policy interface {
	// Admit returns nil to allow. A Refusal reaches the user unchanged.
	Admit(ctx context.Context, req Request) *Refusal
}

// Request is everything a policy may look at. It carries the Vendor Adapter rather
// than a snapshot of the Vendor, because ADR 0004's rule is that reachability is
// never stored, so a snapshot is stale before the policy reads it.
type Request struct {
	Harness string
	Model   string         // the Model id, as the Vendor spells it
	Vendor  vendor.Adapter // this Host's Vendor, on loopback
	Dir     string         // the working directory, already contained
	Live    []Live         // every Session on this Host that has not ended
}

type Live struct {
	Session event.SessionID
	Harness string
	Model   string
	Since   time.Time
}

// Refusal is why a Session may not start. Blocking names the Sessions the user would
// have to stop, and is empty when stopping something would not help.
type Refusal struct {
	Reason   string
	Blocking []event.SessionID
}
```

**Only `SingleSession` is implemented.** It refuses when `len(req.Live) > 0` and names the live Session
as `Blocking`. There is no configurable count, because a knob nobody sets is complexity with a default.

The shape is right if a later policy fits without changing it. Two do:

| policy | what it reads | new field |
| --- | --- | --- |
| A count limit per Host | `len(req.Live)` | none |
| VRAM aware | `req.Vendor.Catalogue(ctx)` for the Model's size, `req.Vendor.Resident(ctx)` for what is loaded now, `req.Live` for who is using it | none |

Passing the `vendor.Adapter` rather than a struct of Vendor facts is what buys the second row. ADR 0007
put `Catalogue` and `Resident` on that interface, so a VRAM policy calls them itself, at the moment it
decides, rather than reading numbers the Daemon gathered at some earlier time.

**A rejected start is an error, never a queue position.** A queue is a second lifecycle. Queued
Sessions must be cancellable, ordered, visible, and they have to survive a restart or they are a lie.
That is a lot of machinery for a limit of one, where the only person in the queue is the person looking
at the Session holding the slot. So the Daemon refuses, names the blocking Session, and the Client
offers "stop that one and start this one" as a single action. One click, and the user chooses which
Session dies, which a queue never lets them do.

A refusal writes no Event, because there is no Session to write it to. It goes to the Daemon's own
operational log.

**Admission is per Host, confirmed.** A Daemon knows only its own Host and never learns about its
peers, so it could not enforce a global limit without becoming a different component. This is not a
concession. VRAM is a per-Host resource, so a global limit would bound the wrong thing.

For a user driving three Hosts that means three concurrent Sessions, one per Host, and no cross-Host
limit anywhere. The Hub does not add one. A Hub-side limit would be advisory, since a Daemon reached
any other way would ignore it, and the user bought three machines to use three machines. The Client
shows the limit per Host row, beside the Session holding it.

## Workspace Root bounds two things, and it is not a sandbox

The honest sentence first, because everything else in this section depends on it. **The Harness runs as
an ordinary process with the user's own permissions.** Nothing the Daemon does stops it opening any
file that user can open. Workspace Root bounds what the Daemon hands the Harness and what the Harness
hands back. It does not bound the Harness.

One enforcement point, used twice:

```go
package workspace

// Root is a Host's Workspace Root, resolved once at Daemon start.
type Root struct{ resolved string }

// Contain resolves name against base with symlinks followed and returns the resolved
// absolute path when it is the Root or under it. Every path the Daemon gives a Harness
// and every path a Harness gives back passes through here, and there is no second way
// in. base is named by the caller rather than defaulted, because the two call sites
// have different bases and the Daemon's own working directory is neither of them.
func (r Root) Contain(base, name string) (string, error)
```

`Contain` resolves before it compares, which is the only rule that matters. It joins `name` onto
`base`, walks up to the deepest ancestor that exists, calls `filepath.EvalSymlinks` on that, rejoins
the part that does not exist yet, and then takes `filepath.Rel` against the Root, refusing a result
that is `..` or begins with `..` and a separator. The Root itself is resolved the same way once at
Daemon start.

Walking up to the deepest existing ancestor is what makes the check usable rather than merely correct.
`EvalSymlinks` fails on a path that is not there, and a write that creates a file names a path that is
not there by definition, so resolving the candidate directly would refuse every new file and leave the
one tool kind this bounds unable to create anything. Resolving the ancestor and checking the remainder
gives the same guarantee: a symlink can only be traversed through a component that exists, and the
components that do not exist yet cannot point anywhere. The remainder is still checked for `..`, since
a caller can name one whether the directory exists or not.

Comparing path elements rather than string prefixes is what stops `/home/v/work-other` passing for
`/home/v/work`. The comparison is case-folded on Windows and exact elsewhere, because a case-sensitive
check on a case-insensitive filesystem refuses a directory the OS considers inside the Root.

The two call sites, and each names its own base:

**At Session start, on the working directory**, with the Workspace Root as the base. So a user who
names a relative directory names it relative to the Root, which is the only base that means anything
to them, and never relative to whatever directory the Daemon happens to have been started in. The
directory must already exist, since a Session cannot run in a directory that is not there, and a
failure means no Session. This is the point ADR 0006's `SessionSpec.Dir` comment already assumed, now
with a function behind it.

**At every `fs/write_text_file`, on the path the Harness sent.** The OpenCode capture found the Daemon
a second lever nobody expected: the client advertised `fs.readTextFile: true` and `fs/read_text_file`
was never called, while the single write went through `fs/write_text_file`. So every write OpenCode
makes crosses the seam, ADR 0006 routes it through `Files.WriteTextFile`, and that calls `Contain`
again with the Session's working directory as the base.

Re-resolving at each write rather than trusting the start-of-Session check is not paranoia. The tree is
mutable and the Harness is the thing mutating it, so a symlink that pointed inside the Root at Session
start can point outside it a minute later. There is still a window between the resolve and the write,
and it is not worth closing. The adversary here is a confused Model, not an attacker racing the Daemon
for microseconds.

**What is not bounded, named rather than implied.** Two of the five tool kinds escape entirely:

| kind | bounded by Workspace Root | why not |
| --- | --- | --- |
| `edit` | yes, on OpenCode | writes cross the seam as `fs/write_text_file` |
| `read` | no | the capture shows reads never cross the seam at all |
| `execute` | no | OpenCode runs `bash` in-process, `terminal: false` on the client |
| `fetch` | no | it leaves the machine, so no path check applies |
| `other` | no | by definition |

This is stronger than ADR 0003 assumed. That ADR said the Approval Policy cannot honour wait or refuse
for reads because OpenCode has no permission key for them. The capture shows there is nothing to hook
either, so Workspace Root really is the only bound on reads, and it bounds only where the Session
started.

So the containment is split, and each half should be described as what it is. **Workspace Root is the
containment for the working directory and for delegated writes. The Approval Policy is the containment
for `execute`.** A user who sets the `execute` slot to auto has given the Session the machine, and the
Client should say that in those words rather than showing a slot that reads like a setting.

The only thing that would make the guarantee real is OS-level isolation: a container, `bubblewrap`, or
a Windows Job Object. Rejected for v1, and named so it is a decision rather than an oversight. It is a
per-platform project of its own, it changes how the Harness is launched on three operating systems,
and this repo has already lost two captures to executable resolution alone.

## The Approval Policy

**Who sets it.** The user, at Session start and while the Session runs. There is no other actor. ADR
0005 left open whether a Host config could pin a slot the user cannot loosen, and the answer is no. A
pinned slot is the same person, at a different time, forbidding themselves, and a lock whose key holder
is the person locked out is decoration. What the Host config does carry is a **default** per slot,
which is the useful half without the lock.

**The default**, in one rule: `read` is auto, the other four are wait, and any slot with no Gate is
clipped to auto.

The clip is what keeps the default legal. ADR 0006 made a slot with no Gate refuse anything but auto,
at Session start and at every change, so a fixed default array would be a default that fails to apply
on the Harness that ships. Computing the default through the clip means it can never produce a policy
the slot rule rejects, and it earns its place immediately: OpenCode's own permission block has keys for
`edit`, `bash` and `webfetch` and none for `other`, so `other` clips on the first Session anyone starts.

`read` is auto rather than wait for a reason worth writing down. On OpenCode it would clip to auto
anyway, so the choice only changes Pi, where a coding agent reads dozens of files per prompt and a wait
default would ask dozens of times. Making it auto keeps the default the same on both Harnesses, and the
Client shows a user who wanted otherwise exactly which slot is ungated. The cost is real and it is the
one named above: reads are bounded by the working directory and nothing else.

**Changing it mid-Session** is settled by ADR 0005, and this adds one rule about timing:

> A policy change applies to Tool Calls requested after it, never to a question already open.

A question in flight is answered by its own `ApprovalDecided`. Without this rule, flipping the `edit`
slot to auto while an edit is waiting would silently answer it, and the log would carry a decision
nobody made.

**Where the record lives, and whether it is an Event.** It is `ApprovalDecided`, and yes. ADR 0005
already made it an Event with a `by` field. What this ADR adds is the reading rule:

> The Daemon's `ApprovalDecided` is the record. What the Harness says happened is corroboration, and
> the two are stored separately rather than reconciled.

That produces three cases and no parsing:

| the Daemon decided | the Harness said | `ToolCallEnded` |
| --- | --- | --- |
| refuse | anything | `refused`, from the Daemon's own decision |
| allow | error | `error`, with the Harness's text kept in `content` |
| it was never asked | whatever it said | that, or `unknown` when it said nothing |

The middle row is why Pi's `Blocked by user` is not read. The Daemon knows it allowed the call, so it
does not look for the word blocked in a string an extension author chose.

## Gating on the protocol, never on a hook

**The Daemon gates only on the in-protocol surface.** On OpenCode that is `session/request_permission`
plus `fs/write_text_file`. On Pi it is the permission-gate extension's `extension_ui_request`. No hook
mechanism is used on any Harness, and Hermes' `pre_tool_call` shell hook is not used at all.

Four reasons, and the first is decisive on its own.

A hook is a second process with its own failure modes, and Hermes' fails open on all five of them. A
gate that permits when it breaks is not a gate, it is a log line.

A hook needs consent the Daemon cannot give. Registration is recorded in an allowlist, and a non-TTY
caller that does not pass `--accept-hooks` gets no hook and no error.

The in-protocol surface fails closed by construction. The Harness blocks until its client answers, so a
Daemon that cannot answer holds the Tool Call instead of releasing it. Nothing has to be configured for
that to be true.

ADR 0006 already built the Adapter around answering a blocking request, so adding a hook would mean two
gates with opposite defaults on one Harness. That is precisely the trap Hermes demonstrates by having
one of each.

**What the Daemon does when a Harness answers a question it was never asked.** Hermes' phantom denial
is that case, and the answer is uncomfortable enough to be worth stating rather than solving: **the
Daemon cannot detect it and does not try.** Detecting it means recognising `denied by ACP client` in
free text, which is the parsing this design forbids everywhere else, and Hermes emits that same string
for an internal failure anyway.

So the Daemon writes what it decided and what the Harness said, and a human reading the log sees a
`ToolCallEnded` reporting a refusal with no `ApprovalRequested` above it. That is also why the
guarantee is worded the way it is:

> The Approval Policy guarantees what the Daemon allowed. It never guarantees what the Harness ran.

Every capture in this repo is consistent with that sentence, and none of them is consistent with the
stronger one. It is also why Hermes is a fixture rather than a v1 Harness under ADR 0003, and the
weaker guarantee is what makes shipping the other two honest.

## Verifying the gate is live before the Session starts

A gate that silently fails to install is worse than no gate, so the Daemon checks its own path rather
than assuming it. Three checks do that, and they do not all sit where the ticket asked. The ticket says
"before admitting a Session", and only the first of the three is that early. Saying so is better than
moving the other two somewhere they cannot work.

| check | when | what it catches |
| --- | --- | --- |
| The declared Gate against the requested policy | before `SessionStarted` | a policy this Harness cannot honour |
| The declared Gate against the captures | in the Adapter's tests | an Adapter that declares a Gate it does not have |
| The gating component announcing itself | inside `Start` | a gate that failed to load at launch |

None of the three is a runtime probe, and the third cannot be moved earlier: whether an extension
loaded is not knowable until the process it loads into is running. What it can do, and does, is fail
the launch rather than the first tool call.

**The declared Gate is checked against the requested policy, at Session start.** ADR 0006's slot rule.
The Daemon reads `Capabilities.Gates`, refuses any slot set to wait or refuse without a Gate, and the
Session never exists. This runs before `SessionStarted` and it is the check that turns a missing gate
into a refusal rather than a surprise.

**The declared Gate is checked against the captures, in the Adapter's tests.** `Gates` is declared and
not discovered, so an Adapter could claim one it does not have. The test that catches that is ADR
0006's replay: for each kind declared gated, `Approve` must be called. OpenCode's own frame counts are
what such a test reads like, and they are unambiguous: `edit` 1 started, 1 ended, 1 asked; `execute`
the same; `read` 2 started, 2 ended, 0 asked. That last row is not in anything OpenCode says about
itself. ADR 0006 found it by counting, which is the point: a Gate is proven by the frames or it is not
proven.

**A Gate that depends on something loadable must announce itself before `Start` returns.** This is the
`--accept-hooks` finding, generalised to the Harness that ships. Pi gates nothing by default. Its
permission-gate extension is a command-line argument, and if it fails to load then Pi runs every tool
ungated while the Daemon believes it is gating. So:

> An Adapter whose Gate depends on a component that can fail to load must have that component announce
> itself before `Start` returns. If the announcement does not arrive, `Start` returns an error and the
> Session ends `failed` without ever reaching `Idle`.

Pi's RPC docs list `notify` and `setStatus` as fire-and-forget extension messages, so the announcement
is a one-line change in the bundled extension. That it works has not been captured, and it should be,
because the fallback is ugly. If Pi cannot announce, its Adapter declares no Gates at all, the clip
sets every slot to auto, and the Client shows a Session with no gates anywhere. Ugly is the correct
outcome, since it is what is true, and it is unpleasant enough to motivate the capture.

There is deliberately no probe that runs a tool call to see whether the Harness asks. Testing a gate by
tripping it means running the thing the gate exists to hold.

## What a start proves

A `ToolCallRequested` means the Harness announced an intention, and nothing more. Both Harnesses
announce before the gate resolves: OpenCode moves a tool call to `in_progress` before it sends
`session/request_permission`, and Pi fires `tool_execution_start` before its `extension_ui_request`.
ADR 0005 deleted `ToolCallStarted` for exactly this reason.

So the state machine takes one thing from a start. **There is now an open Tool Call that must be
closed.** It joins the Daemon's ledger, and it closes by the Harness's report, by the Daemon's own
refusal, or by synthesis at `PromptCompleted` or `SessionEnded`.

Nothing else follows. Not that a file was read, not that a command ran, and not that the Session became
busy, since `PromptSubmitted` already said that. The Client rule from ADR 0005 is the same fact from
the other side: never draw a tool call as running before its `ApprovalDecided`.

## Harness-generated artefacts

Hermes documents writing `trajectory_samples.jsonl` and `failed_trajectories.jsonl` into the working
directory, although none appeared in the 2026-08-14 capture. The Daemon writes `opencode.json` there
itself, under ADR 0003. So files land in the user's project from both directions.

**Workspace Root has nothing to say about this, and calling it containment inflates it.** A file
written inside the Root is the Root working. The concern is tidiness and attribution, not escape.

Two rules cover it, and they cost nothing:

**Files the Daemon puts in the user's directory are the Daemon's to remove.** The per-Session
`opencode.json` is written at Session start and deleted at `SessionEnded`. Leaving it would confuse the
next Session and the user's own `opencode` runs. This rule is safe only because admission allows one
Session at a time, since the file's name is fixed by OpenCode's discovery and cannot be made
per-Session. If admission is ever loosened, two Sessions in one directory need a different answer, and
that is the coupling to remember.

**Files the Harness puts there are the user's, and the log says which directory a Session ran in.**
`SessionStarted` already carries `cwd`, so a stray `trajectory_samples.jsonl` traces back to a Session
without any new machinery. Attribution is the whole answer.

Prevention would mean a copy-on-write working directory or an overlay mount, which is the same
OS-level project as sandboxing `execute` and is rejected for the same reasons.

## Considered options

- **Eight states, as the ticket proposed.** Rejected: `requested` and `admitted` describe a Session
  that does not exist, `stopping` is one bounded operation, and `crashed` is a reason on `Ended` that
  the Daemon treats identically.
- **A `Stopping` state with its own Event.** Rejected: a seventeenth kind for a condition that lasts
  about a second and ends in a guaranteed kill.
- **`SessionStarted` written after the Harness is up, with no `Starting` state.** One fewer kind.
  Rejected: a launch can take 30s on a cold Model, and a Session with no id yet is one the user cannot
  cancel and a failed launch has nowhere to be recorded.
- **One `opencode acp` process holding several Sessions.** Fewer processes, and the protocol supports
  it. Rejected: Pi cannot, so it is a per-Harness supervision shape, and it takes the per-Session
  working directory and the per-Session Model with it.
- **Restart a crashed Harness.** The Session appears to survive. Rejected: the restarted Harness has no
  message history, so the Session only appears to survive.
- **Resume a Session across a Daemon restart on OpenCode.** Rejected: Pi cannot, and a property that
  holds on one Harness cannot be described to the user in a sentence.
- **A supervision timeout that marks a Session hung.** Rejected: Hermes' hang tracked its own
  supervisor's timeout across eight runs at six timeout values, and llama-swap's cold start is 23 times
  its warm one.
- **A queue in front of admission.** No start is ever refused. Rejected: it is a second lifecycle, with
  cancellation, ordering and restart survival, in front of a limit of one.
- **A Hub-wide admission limit.** Rejected: a Daemon never learns about its peers, so the limit would
  be advisory, and VRAM is per Host anyway.
- **Gating on Hermes' `pre_tool_call` shell hook.** It sits closer to the tool than the protocol does.
  Rejected: it fails open on all five of its failure paths and needs consent a non-TTY caller cannot
  give.
- **Detecting a phantom denial by matching `denied by ACP client`.** Rejected: it is free-text parsing,
  and the same string means an internal failure.
- **A Host config that pins an Approval Policy slot the user cannot loosen.** Rejected: one user, so
  the lock and the key are the same person.
- **A fixed default policy array.** Rejected: it fails ADR 0006's slot rule on the Harness that ships,
  which is why the default is computed through the clip instead.
- **OS-level isolation for `execute`.** The only thing that makes the containment claim real. Rejected
  for v1: a per-platform project that changes the launch path on three operating systems.

## Consequences

- Two Event kinds are added, `SessionReady` and `DaemonStarted`, taking the closed set to sixteen. Both
  are written by the Daemon.
- **ADR 0005's "one trigger" sentence is corrected to two.** Synthesis fires at `PromptCompleted` and
  at `SessionEnded`. Anyone reading ADR 0005 alone gets this wrong, and every crash, stop and restart
  path depends on it, so the correction belongs in the reader's hands rather than only in this ADR's
  argument.
- [#10](https://github.com/VictorJohnOkoh/Capstone/issues/10) inherits a `Seq` counter that must be
  reloaded from the log on boot before `DaemonStarted` is written, since it is gapless and per Daemon.
  It also inherits the boot sweep as a write path: for every Session with no `SessionEnded`, four kinds
  in a fixed order.
- [#11](https://github.com/VictorJohnOkoh/Capstone/issues/11) renders five Session states, shows the
  time since the last Event on a `Working` Session rather than a spinner, and offers a stop from every
  state. It draws a `lost` Session as ended with its transcript intact, and its restart action says
  "start a new Session here" rather than resume. On a refused start it names the blocking Session and
  offers to stop it. It shows the `execute` slot set to auto as giving the Session the machine, and it
  shows `read` as ungated on OpenCode whatever the slot says.
- The Pi Adapter gains a requirement that is not translation: its permission-gate extension must
  announce itself, and `Start` fails without that. Whether Pi can announce needs one capture. Until it
  returns, the Pi Adapter's honest declaration is no Gates at all.
- The Daemon gains a `workspace.Root` with one method, and every path in the system goes through it.
  There is no second path check anywhere, and adding one would be the bug this design is trying to
  avoid.
- The Daemon deletes the per-Session `opencode.json` at `SessionEnded`, which is safe only while
  admission allows one Session. Loosening admission reopens it.
- Testing needs no Host, Vendor, Harness or GPU for most of this. The state machine is a fold over a
  slice of Events. `admission.Policy` takes a `Request` a test builds by hand. `workspace.Root.Contain`
  takes a `t.TempDir()` with symlinks in it. The parts that need a real process are the shutdown ladder
  and the crash path, which is the same boundary ADR 0006 drew.
- `CONTEXT.md` gains **Session State** and **Admission**, the Event Kind entry becomes sixteen kinds
  with `SessionReady` and `DaemonStarted` added to the Daemon's list, **Workspace Root** names its
  enforcement point and what it does not bound, and **Approval Policy** gains the default rule and who
  may set it.

**ADR 0005 is corrected in one place and extended in another.** The correction is the synthesis
trigger, argued above: its "one trigger" sentence is replaced by two, and its own truncation shape
already needed the second. The extension is the two new kinds, which are additions under its own
compatibility rules, and one of them it handed to this ticket by name. Nothing else about the Event
model moves.

**ADR 0006 is sharpened, not reopened.** Its shutdown ladder, slot rule and timer rule are used here as
written. What is sharpened is its phrase "no Session exists" on a failed `Start`, which is true of the
Adapter's world and not of the Daemon's log, for the reasons in the launch-failure section above.

It also sharpens ADR 0003.
That ADR priced the missing read gate as a policy the Approval Policy cannot honour, and the OpenCode
capture shows reads never crossing the seam at all, so Workspace Root is the only bound on them and it
bounds only the directory the Session started in.
