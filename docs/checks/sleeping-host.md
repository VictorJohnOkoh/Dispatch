# Start a Session on a sleeping Host

`SPEC.md` behaviour 1. The Host reads `Down unreachable`, the Client names it, and the start on that
Host is disabled rather than failing silently.

A person runs this one. It needs a machine that is really asleep, and a test can only fake one.

## What you need

Two Hosts in `hub.json`, both working, and one of them a machine you can suspend. `docs/install.md`
puts a Daemon on each. Two, because the check is as much about the Host that keeps working as about
the one that stopped.

## The run

1. Start the Hub with both Hosts reachable, open `/hosts`, and see both cards read `Ready`.
2. Suspend the second Host. Close the lid, or `shutdown /h` on Windows, or `systemctl suspend`.
3. Watch `/hosts`. The card changes on its own; do not reload the page.
4. Open `/new` and go to the Host step.

## What has to be true

- The card for the sleeping Host reads `Down unreachable`. Both words: `Down` is the state and
  `unreachable` is the cause, and the cause is what tells this apart from a Host that answered SSH
  with no Daemon behind it.
- The card is still there and it is dimmed. A Host is never hidden for failing.
- The other card still reads `Ready`, and the Client's stream is still open. One Host going away does
  not end the merged stream.
- On `/new`, the sleeping Host cannot be chosen. The wizard is four steps and a start that failed at
  the end of them would have wasted all four.
- No Event is written anywhere for the attempt. A Session that could not start is not a Session.

A Host that reads `Down no-daemon` instead means the machine answered SSH and nothing was listening
behind the tunnel. That is a different failure and this check has not been run.

## Then wake it up

Wake the Host and leave `/hosts` open. The card comes back to `Ready` on its own, because `Down` is
the state the Hub keeps retrying. Nothing to click.

## Runs

| date | commit | Host | what was seen | held |
| --- | --- | --- | --- | --- |
