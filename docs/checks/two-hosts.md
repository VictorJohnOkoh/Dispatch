# Run the same prompt on two Hosts at once

`SPEC.md` behaviour 8. Both stream into one merged Client stream, neither starves the other, and the
commands still answer while both are streaming.

## What you need

Two Hosts, both `Ready`, each with a Harness and a Model. The two Models do not have to match; the
prompt does.

## The run

1. Start a Session on each Host.
2. Open one Session page. The rail beside it lists the other.
3. Submit the same long prompt to both, as close together as you can. A prompt that asks for a page
   of prose is what you want: the check is about two streams at once, not about the answer.
4. While both are still writing, use the Client. Change the Approval Policy, or interrupt one, or
   open `/hosts`.

## What has to be true

- Both Sessions advance at the same time. Watch the rail: the Session you are not looking at moves on
  its own.
- Neither one stops while the other writes. A long run on one Host that pauses the other is the
  starvation this check exists to catch.
- The command you sent in step 4 answers while both are streaming. Commands and the stream are
  separate, and a busy stream must not block a command.
- Each Event lands under the right Host. Open the other Session page and find its own text there,
  with nothing from the first.
- Stop one Session. The other keeps going, and the stream stays open.
- Reload the page mid-stream. Both Sessions replay from their own Cursors, and the Cursor has an entry
  per Host.

## Runs

| date | commit | Hosts | what was seen | held |
| --- | --- | --- | --- | --- |
