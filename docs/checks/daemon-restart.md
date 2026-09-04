# Kill the Daemon under a live Session and restart it

`SPEC.md` behaviour 5. The boot sweep ends that Session `lost`, its transcript is intact and readable,
and the Client offers a new Session rather than a resume.

A person runs this one. It needs a Daemon killed without warning, mid-turn, with a real Harness
process under it.

## What you need

A Host with a Harness that takes time over a prompt, and a shell on that Host.

## The run

1. Start a Session and submit a prompt long enough to still be running in ten seconds.
2. While it is working, kill the Daemon on the Host. Not Ctrl+C, which is a clean shutdown:

   ```powershell
   Stop-Process -Name dispatch -Force
   ```

   ```bash
   pkill -9 -x dispatch
   ```

3. Check the Harness went too, with the shell check in [kill-the-tree.md](kill-the-tree.md).
4. Start the Daemon again with the same `daemon.json`, and read its first lines.
5. Reload the Session page in the Client.

## What has to be true

- The Daemon's log holds `the boot sweep ended the Sessions the last run left open`, with a count of
  one.
- The Session's last Event is a `SessionEnded` with `reason: lost`. Not `stopped`, which is a person,
  and not `failed`, which is the Session's own doing.
- Any Tool Call that was held when the Daemon died is ended too, with an `ApprovalDecided` carrying
  `by: daemon_restart`. A question nobody can answer any more is not left open.
- The transcript is still there and still readable: `<the directory holding logPath>/<session
  id>.transcript`. Open it and find the Harness's own words from before the kill.
- The Session page still draws the whole history. An ended Session is readable for ever; that is what
  the log is for.
- The Client offers a **new** Session and no way to resume this one. A Session is one process, and
  that process is gone.
- Start a new Session on the same Host. It starts. The slot was freed by the sweep and not left held
  by a Session nobody can reach.

## Runs

| date | commit | Harness | what was seen | held |
| --- | --- | --- | --- | --- |
