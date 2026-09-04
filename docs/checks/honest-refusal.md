# The refusal is honest

`SPEC.md` behaviour 4. Set `execute` to `refuse`, ask the Session to run a command, and see
`ToolCallEnded{refused}` written from the Daemon's own `ApprovalDecided`, never from the Harness.

A person runs this one. The claim is about who wrote a line, and the only way to be sure is to watch
a real Harness be told no and then look at what the Daemon wrote.

## What you need

A Host with a Harness that runs shell commands: OpenCode or Pi. Passthrough runs no tools and has no
Approval Policy at all, so it cannot be used here.

## The run

1. Start a Session, and on the wizard's policy step set `execute` to `refuse`. Leave the rest alone.
2. Submit a prompt that makes the Harness run a command. "Run `dir` and tell me what is in the
   directory" does it.
3. Read the Session page, and then read the Event log.

## What has to be true

- **No question is asked.** `refuse` refuses without asking, so there is no `ApprovalRequested` and
  nothing to answer. A question here means the slot fell through to `wait`.
- An `ApprovalDecided` carries `decision: refused` and `by: policy`.
- A `ToolCallEnded` carries `outcome: refused`, and it comes **after** that decision. The Daemon
  writes both, and it writes them for a Tool Call the Harness never got to run.
- The command did not run. Check on the Host rather than in the Client: the directory listing is not
  in the transcript, and whatever the command would have changed is unchanged.
- The Session is still usable. Submit an ordinary prompt afterwards and it answers. A refusal ends one
  Tool Call and not the Session.
- The Harness's own account of the call, whatever it is, does not become a second `ToolCallEnded`. A
  Tool Call the Daemon refused is over.

The failure this check exists to catch is a `ToolCallEnded` that came from the Harness reporting its
own refusal. That line would be the Harness marking its own work, which is the one thing the Event
model does not allow.

## Runs

| date | commit | Harness | what was seen | held |
| --- | --- | --- | --- | --- |
