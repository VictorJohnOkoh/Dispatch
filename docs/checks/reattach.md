# Close the laptop mid-Session and reattach

`SPEC.md` behaviour 2. Every Event replays, including the assistant message that was still arriving
when the lid closed, and it replays whole.

A person runs this one. The lid is the point: a dropped socket in a test is not a machine that
stopped executing.

## What you need

A Host that is **not** the Client machine, running a real Harness. The Daemon has to keep working
while the Client is away, so a Daemon on the laptop you are closing proves nothing.

Give the Session something long to say, so the lid closes in the middle of an assistant message. A
prompt that asks for a page of prose is enough.

## The run

1. Start a Session on the Host and watch it at `/hosts/{host}/sessions/{session}`.
2. Submit the long prompt.
3. While the text is still arriving, close the laptop lid. Wait a minute.
4. Open the lid. The Hub reconnects on its own; if you stopped it, start it again and reload the
   Session page.

## What has to be true

- The page comes back with the whole transcript, from `SessionStarted` to now.
- The assistant message that was arriving when the lid closed is there **whole**. Not half of it, and
  not twice. Deltas are never kept in the log, so what replays is the message the Daemon assembled,
  and the read path sends an open message whole.
- The Daemon's log on the Host holds a `HubDetached` and then a `HubAttached`, in that order and one
  each. Those two Events are the record that the Host knew it was alone.
- The Session is still the same Session. It was never `lost`, because the Daemon never restarted.
- Submit another prompt. It works, on the Session that was already there.

Two copies of the same text is the failure this check exists to catch. It means the replay started
before the Cursor rather than after it.

## Runs

| date | commit | Harness | what was seen | held |
| --- | --- | --- | --- | --- |
