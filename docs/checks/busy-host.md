# Start a second Session on a busy Host

`SPEC.md` behaviour 7. The refusal names the Session holding the slot, and one click stops that one
and starts this one.

## What you need

One Host with a Session already running on it.

## The run

1. Start a Session and leave it running.
2. Open `/new` and fill the wizard in for the same Host: a Model, a Harness, a working directory you
   typed yourself, and a policy.
3. Start it.

## What has to be true

- The start is refused, and the wizard says why: `one Session at a time on this Host`.
- The refusal **names** the Session holding the slot. A refusal you cannot act on is a dead end.
- It is not a queue position. Nothing says "you are next", because there is no queue and nothing is
  waiting on your behalf.
- The wizard offers to stop that one and start this one, as a single action.
- Take it. The old Session ends `stopped`, the new one starts, and the working directory is the one
  **you typed**, not a default and not the old Session's.
- The old Session's transcript is still readable afterwards.
- No Event was written for the refused start. Admission is asked before the Session exists, so a
  refusal produces no Session to have Events.

## The other Host is not busy

If you have a second Host, go back to `/new` and start a Session there while the first Host is busy.
It starts. Admission is per Host, and a Daemon never learns about its peers.

## Runs

| date | commit | what was seen | held |
| --- | --- | --- | --- |
