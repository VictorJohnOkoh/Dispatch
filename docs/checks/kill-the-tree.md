# Kill a Harness that will not die

`SPEC.md` behaviour 6. Stop runs the ladder and the whole process tree is gone, checked from a shell
on the Host rather than from the Client.

**This one gets skipped unless it is called out, so it is called out.** The Client can only tell you
what the Daemon believes. A shell tells you what is running.

## What you need

A Harness that starts a process of its own. OpenCode resolves to a package binary that spawns a
child, which is the case a naive kill gets wrong. Passthrough starts nothing and cannot be used here.

A shell on the Host. Not SSH from the Client with one command; sit at the Host, or hold a session
open, because you need to ask twice with the ids from the first answer.

## The run

1. Start a Session with OpenCode and submit a prompt, so the Harness is really working.
2. On the Host, list the Daemon's children and their children:

   ```powershell
   $all = Get-CimInstance Win32_Process
   function Descend($id) {
     $all | Where-Object { $_.ParentProcessId -eq $id } | ForEach-Object { $_; Descend $_.ProcessId }
   }
   Descend (Get-Process dispatch).Id | Select-Object ProcessId, Name, CommandLine
   ```

   ```bash
   pstree -p $(pgrep -x dispatch)
   ```

   **The PowerShell has to recurse**, which is why it is four lines and not one. Asking only for the
   Daemon's own children lists the Harness and stops there, and the grandchild is exactly what a
   naive kill leaves behind.

   Write the ids down. Every one of them, not just the Harness.
3. Stop the Session from the Client.
4. Wait five seconds, then ask for those ids again:

   ```powershell
   Get-Process -Id 1234, 5678 -ErrorAction SilentlyContinue
   ```

   ```bash
   ps -p 1234,5678
   ```

## What has to be true

- Nothing answers. Every id from step 2 is gone, including the ones the Harness started itself.
- The Session's `SessionEnded` reads `reason: stopped`.
- Start another Session on the same Host straight away. It starts, which says the slot was really
  freed.

A process that is still there is the orphan the Job Object exists to prevent. It holds the Model in
VRAM while the Daemon believes the slot is free, and the Client can never show you it.

## The harder half: a Harness that will not die

The ladder's later steps only run against something that ignores a polite stop. To reach them, start
a Session and then, on the Host, suspend the Harness process so it stops answering.

On Linux that is one command:

```bash
kill -STOP <harness pid>
```

**Windows has no equivalent in the shell.** `Suspend-Process` is not a cmdlet, and suspending a
process needs a tool that calls the API: `pssuspend.exe` from Sysinternals does it.

```powershell
.\pssuspend.exe <harness pid>
```

Without that tool this half cannot be run on Windows. Say so in the table rather than leaving the row
looking complete, because Windows is the OS the Job Object correction was written for and this is the
half that reaches it.

Then stop the Session from the Client. The tree still has to go, and the Daemon still has to end the
Session rather than wait for ever.

## Runs

| date | commit | OS | Harness | what was seen | held |
| --- | --- | --- | --- | --- | --- |
