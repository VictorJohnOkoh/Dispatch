# Approve a tool call from a phone

`SPEC.md` behaviour 3. The `ApprovalRequested` arrives, the decision goes back, and the Tool Call ends
with an outcome that says a human decided it.

A person runs this one, on a phone, over a tunnel. It is the check that the approval loop works when
the person is not at the machine, which is the whole reason approvals are on the Event stream.

## Before you start: the Hub has no authentication

The Hub listens on `127.0.0.1` and asks nobody who they are. Anything that reaches it can start a
Session on your Hosts and run tools there. **Use a tunnel that reaches only your own devices.**
Tailscale or a WireGuard network is the shape that fits. Do not put the Hub on a public address for
this check, and do not leave the tunnel up afterwards.

## What you need

- A Host with a Harness that runs tools, and a Model that will use one. Ask for a file to be edited.
- A phone on the tunnel, with the Hub's address open in its browser.

## The run

1. Start a Session, and on the wizard's policy step set `edit` to `wait`.
2. On the phone, open `/hosts/{host}/sessions/{session}`.
3. From the desktop, submit a prompt that makes the Harness edit a file in the Workspace Root.
4. Answer on the phone. Leave the desktop page open as well.

## What has to be true

- The question reaches the phone without a reload. It arrives on the Event stream, the same stream
  the desktop is reading.
- The question names the tool and enough of what it would do to decide on. A question you cannot
  answer without walking to the machine is a question that failed.
- Tapping the answer ends the hold. The Harness moves on within a second or two.
- The `ToolCallEnded` says a human decided: the `ApprovalDecided` before it carries `by: user`, not
  `by: policy`.
- The desktop page shows the same thing, at the same time. Both readers are on one merged stream and
  neither is the owner of the decision.
- Answer a second one with the phone screen locked and reopened first. The stream resumes from its
  Cursor rather than starting again.

## Then take the tunnel down

## Runs

| date | commit | tunnel | what was seen | held |
| --- | --- | --- | --- | --- |
