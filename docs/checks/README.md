# The thirteen behaviours

`SPEC.md` decides what proves v1 is done: not a test suite, but thirteen behaviours you can watch on
real machines. **v1 is done when all thirteen hold.**

A person walks these. Every sheet below says what to do, what has to be true, and holds a table of
runs at the bottom. Fill the table in when you run one.

Three get skipped unless they are called out, and each of the three sheets says why at the top:
number 6 needs a shell on the Host, number 12 needs two builds, and number 13 fails if the install
instructions do not exist.

## The walk

| # | Behaviour | Sheet | Last run | Held |
| --- | --- | --- | --- | --- |
| 1 | Start a Session on a sleeping Host | [sleeping-host.md](sleeping-host.md) | not run | |
| 2 | Close the laptop mid-Session and reattach | [reattach.md](reattach.md) | not run | |
| 3 | Approve a tool call from a phone | [approve-from-a-phone.md](approve-from-a-phone.md) | not run | |
| 4 | The refusal is honest | [honest-refusal.md](honest-refusal.md) | not run | |
| 5 | Kill the Daemon under a live Session and restart it | [daemon-restart.md](daemon-restart.md) | not run | |
| 6 | Kill a Harness that will not die | [kill-the-tree.md](kill-the-tree.md) | not run | |
| 7 | Start a second Session on a busy Host | [busy-host.md](busy-host.md) | not run | |
| 8 | Run the same prompt on two Hosts at once | [two-hosts.md](two-hosts.md) | not run | |
| 9 | Point a Session at a directory outside the Workspace Root | [outside-the-root.md](outside-the-root.md) | not run | |
| 10 | Swap the Harness and change nothing else | [swap-the-harness.md](swap-the-harness.md) | not run | |
| 11 | See all three Capability values in one Model list | [three-capability-values.md](three-capability-values.md) | not run | |
| 12 | Break the Handshake on purpose | [handshake.md](handshake.md) | 2026-09-02 | built, not run |
| 13 | Put a Daemon on a Host that has never had one | [first-host.md](first-host.md) | not run | cannot hold yet |

**Number 13 cannot hold yet.** It is `dispatch host add`, and that command is not in the build; issues
#80 to #83 are the work. Its sheet measures the manual fallback in the meantime and records the run
as not held. v1 is not done until that one holds with the rest.

**Number 2 has a hole in it.** `HubDetached` and `HubAttached` are declared and nothing writes either,
which is issue #109. The sheet says what not to look for. The rest of that behaviour is checkable now.

## What you need for the whole walk

Two Hosts and a Client machine. One Host with all three Vendors on it, and both OpenCode and Pi in its
`daemon.json`. A phone, a private tunnel, and a machine that has never had a Daemon.

Numbers 1, 8 and 12 need the second Host. Number 11 needs all three Vendors. Numbers 4, 6, 9 and 10
need a Harness that runs tools, so passthrough cannot stand in for one.

## The order

Walk them in order. It is not arbitrary: the early ones prove the Host answers at all, and the later
ones assume a Session that starts.

Number 13 is the one exception, and it is an exception to the order and not to the walk. Run it
first if you have a machine that has never had a Daemon, because a second run on that machine is a
person who now knows the answers. Then come back to number 1 and go through in order.

## What to record

Each sheet's table takes one row per run: the date, the commit, what was seen, and whether the
behaviour held. Write what you saw and not whether you were satisfied. "The card read `Down
unreachable` and the wizard would not let me pick that Host" is a record. "Worked" is not.

**Anything that did not hold is filed as its own issue.** Do not fix it inside the walk. A run that
stopped to fix something is a run that measured the fix and not the build.
