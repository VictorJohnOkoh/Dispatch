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

Four Hosts, one in each Host State, and five Sessions.

- Desktop is `Ready` and holds the Session in focus. Its message is still open, so
  Deltas arrive when you attach.
- Shed box is `Ready`. Six seconds after you attach, it asks to run `rm -rf build/`.
  This is the interruptive case, and it always comes from the Host you are not
  looking at.
- Laptop is `Down{no-daemon}`. Its Session was `Working` at 14:02 and may still be.
  Everything about it is Stale.
- Old box is `Incompatible`.
- Desktop also holds an `Ended{failed}` Session and a passthrough Session with no
  tools, so no Approval Policy.

The stream at `/stream` replays a fixed script: Deltas, a `PromptCompleted`, the
`ApprovalRequested` from Shed box, then Desktop drops to `Connecting` at 14 seconds
and comes back at 20. Allow or Deny does a real `POST` that answers `202` with an
empty body, and both `ApprovalDecided` and `ToolCallEnded` come back on the stream.
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

**The four choices are really two.** Once the Model list is scoped to one Host, the
Vendor is a prefix on the Model name and nobody picks it separately. The Harness has
a sensible default. So the flow is Host and Model, with the Harness as an override.
C's wizard looked heavy the moment I drew it: four steps for two selects. No wizard.

**`ToolCallEnded{outcome: unknown}` should be grey, not red.** Drawing it, the pull
to make it look like a failure was strong. "no result reported" is not a failure, it
is the Harness going quiet, and the user does nothing about it.

**"no Gate" beside an OpenCode read reads as information, not as a bug.** ADR 0005
refused to invent an approval that never happened. On screen that refusal is legible,
which I was not sure of before.

## The recommendation

The Session is the primary object, so A. The user drives Sessions. A Host is a place
a Session runs, and it only becomes interesting when it breaks.

Two things to take from B. The Host card is where Host facts belong, once, instead of
repeated on every row, so A needs a Hosts view one click away rather than only the
strip at the top. And B's operations table is the best of the three tool call
renderings for scanning what a Prompt actually did, so fold it into A's step list.

Nothing to take from C. Its toast is only needed because it hides the other Sessions,
which is a problem A does not have.

Flip through before this is settled. The useful disagreement is usually "A, but with
that bit of B".
