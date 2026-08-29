# Prototype: the multi-Host Client

Throwaway. Answers issue #11. Server-rendered HTML and a little vanilla JS, faking
all data. No Daemon, no Hub, no persistence, no tests. Delete it when the answer is
folded into real code.

```
python prototype/multi-host-client/serve.py
```

Then open <http://127.0.0.1:8765/?variant=A>. Left and right arrow keys switch
variant, and so does the bar at the bottom.

## The question

What is the user looking at? Three answers, one route, `?variant=`.

| variant | primary object | how it starts a Session |
| --- | --- | --- |
| A, queue | the Session. One list, every Host | defaults, with an override |
| B, machine room | the Host. A card each, Sessions inside | a dense form per card |
| C, desk | the conversation. One transcript, a rail | a four step wizard |

## The faked world

Five Hosts, covering every Host State and both causes of `Down`, and six Sessions.

- Desktop is `Ready` and holds the Session in focus. Its message is still open, so
  Deltas arrive when you attach.
- Shed box is `Ready`. Six seconds after you attach, it asks to run `rm -rf build/`.
  This is the interruptive case, and it always comes from the Host you are not
  looking at.
- Laptop is `Down{no-daemon}`. Its Session was `Working` at 14:02 and may still be.
  Everything about it is Stale.
- Garage rig is `Connecting`. Its Session keeps its content at full strength with a
  mark on the edge, because ADR 0004 lets `Connecting` absorb a blink. At 26 seconds
  it gives up and becomes `Down{unreachable}`, and only then does it dim.
- Old box is `Incompatible`.
- Desktop also holds an `Ended{failed}` Session and a passthrough Session with no
  tools, so no Approval Policy.

The stream at `/stream` replays a fixed script: Deltas, a `PromptCompleted`, the
`ApprovalRequested` from Shed box, Desktop dropping to `Connecting` at 14 seconds and
returning at 20, and Garage rig giving up at 26. Allow or Deny does a real `POST`
that answers `202` with an empty body, and both `ApprovalDecided` and `ToolCallEnded`
come back on the stream.
That is ADR 0009's rule, that a command is only an intention.

The one piece of real logic is `world.state_of`, which folds a Session's Events into
its Session State. Everything else is a fixture.

## What the prototype changed my mind about

**Session State alone cannot label a row.** ADR 0008's five states are right, and
they are not enough for the Client. `Working` on a `Ready` Host and `Working` on a
Host that stopped answering ten minutes ago look identical and mean different
things. No Event says which one you have, because the difference is Host State,
which lives only in the Hub. So a row renders the pair, never the Session State on
its own. Neither ADR says this and the Client cannot work without it.

**The interruptive Event decides the layout. The transcript does not.** I expected
the tool call rendering to be the hard part. It was not. C reads best while nothing
is happening, and it is the only variant that needs a whole extra mechanism, a
floating toast, to show a question from a Session that is off screen. A and B get it
free because every Session is already on the page. Where the pending Tool Call can
appear is a layout constraint, not a component you add later.

**A Host that is not `Ready` costs a Session row three extra pieces.** The cause, the
stamp, and a disabled composer. In A each row carries all three itself and the row
gets crowded. In B the Host card header carries them once and every Session under it
inherits them. That is the strongest argument for B, and I did not expect the
argument to come from the failure case.

**Only two of the four choices carry information.** Once the Model list is scoped to
one Host, the Vendor is a prefix on the Model name and nobody picks it separately,
and the Harness has a sensible default. Drawing it, I read that as an argument
against the wizard. The decision below went the other way, and the reason is worth
recording: four deliberate steps beat two selects when the thing being started runs
for an hour on a machine in another room.

**Tool Calls have no parent and no child, so no variant can draw a tree.** The
issue says tool calls and their results are structurally nested. The only nesting
the Event model has is a call and its end, and a Prompt and the calls inside it.
`ToolCallRequested` carries no parent id, so a Client that drew a tool call inside
another tool call would be inventing a relation no Event states. All three variants
therefore show two levels, and that is the ceiling rather than a shortcut.

**`ToolCallEnded{outcome: unknown}` should be grey, not red.** Drawing it, the pull
to make it look like a failure was strong. "no result reported" is not a failure, it
is the Harness going quiet, and the user does nothing about it.

**"no Gate" beside an OpenCode read reads as information, not as a bug.** ADR 0005
refused to invent an approval that never happened. On screen that refusal is legible,
which I was not sure of before.

## The decision

**The primary object is the conversation. C.** Chosen after seeing all three side by
side. I had argued for A, on the grounds that the user drives Sessions and a Host
only becomes interesting when it breaks. Seeing them beside each other did not
support that: a list of every Session on every machine is a thing to administer, and
the work happens in one conversation at a time.

**B's Host card comes with it, read only.** A Hosts view holds the cause, the stamp,
the Vendors and the resident Models once per machine, instead of repeating them on
every Session row. It shows machines. It does not start Sessions.

**The wizard stays.** Four steps, Host then Model then Harness then Approval Policy,
opened from the rail. Starting does not live on the Host card.

What C costs, taken on purpose. It hides every Session but one, so the toast is
load bearing rather than decoration: it is the only path a `ToolCallRequested` on
another Host has to reach the user. Whatever replaces it in real code has to keep
that job, and the rail's Asking pill is not enough on its own.
