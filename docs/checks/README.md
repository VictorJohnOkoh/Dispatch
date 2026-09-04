# The thirteen behaviours

`SPEC.md` decides what proves v1 is done: not a test suite, but thirteen behaviours you can watch on
real machines. **v1 is done when all thirteen hold.**

A person walks these. Every sheet below says what to do, what has to be true, and holds a table of
runs at the bottom. Fill the table in when you run one.

Three get skipped unless they are called out, so each of the three says so at the top: number 6 needs
a shell on the Host, number 12 needs two builds, and number 13 fails if the install instructions do
not exist.

## The walk

| # | Behaviour | Sheet | Last run | Held |
| --- | --- | --- | --- | --- |
| 1 | Start a Session on a sleeping Host | [sleeping-host.md](sleeping-host.md) | — | — |
| 2 | Close the laptop mid-Session and reattach | [reattach.md](reattach.md) | — | — |
| 3 | Approve a tool call from a phone | [approve-from-a-phone.md](approve-from-a-phone.md) | — | — |
| 4 | The refusal is honest | [honest-refusal.md](honest-refusal.md) | — | — |
| 5 | Kill the Daemon and restart it | [daemon-restart.md](daemon-restart.md) | — | — |
| 6 | Kill a Harness that will not die | [kill-the-tree.md](kill-the-tree.md) | — | — |
| 7 | Start a second Session on a busy Host | [busy-host.md](busy-host.md) | — | — |
| 8 | Run the same prompt on two Hosts at once | [two-hosts.md](two-hosts.md) | — | — |
| 9 | Point a Session outside the Workspace Root | [outside-the-root.md](outside-the-root.md) | — | — |
| 10 | Swap the Harness and change nothing else | [swap-the-harness.md](swap-the-harness.md) | — | — |
| 11 | See all three Capability values | [three-capability-values.md](three-capability-values.md) | — | — |
| 12 | Break the Handshake on purpose | [handshake.md](handshake.md) | 2026-09-02 | built, not run |
| 13 | Put a Daemon on a Host that never had one | [first-host.md](first-host.md) | — | — |

## What you need for the whole walk

Two Hosts and a Client machine. One Host with all three Vendors on it, and both OpenCode and Pi in its
`daemon.json`. A phone, a private tunnel, and a machine that has never had a Daemon.

Numbers 1, 8 and 12 need the second Host. Number 11 needs all three Vendors. Numbers 4, 6, 9 and 10
need a Harness that runs tools, so passthrough cannot stand in for one.

## The order

Walk them in order. It is not arbitrary: the early ones prove the Host answers at all, and the later
ones assume a Session that starts.

Number 13 is the exception. Run it first if you have a machine that has never had a Daemon, because
it is the one check a second run cannot repeat.

## What to record

Each sheet's table takes one row per run: the date, the commit, what was seen, and whether the
behaviour held. Write what you saw and not whether you were satisfied. "The card read `Down
unreachable` and the wizard would not let me pick that Host" is a record. "Worked" is not.

**Anything that did not hold is filed as its own issue.** Do not fix it inside the walk. A run that
stopped to fix something is a run that measured the fix and not the build.
