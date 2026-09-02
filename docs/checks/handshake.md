# Break the Handshake on purpose

`SPEC.md` behaviour 12. Run an old Daemon against a new Hub, see the Host read `Incompatible`, and
confirm from the Daemon's own log that the Hub stopped retrying.

A person runs this one. It needs two builds that disagree, which no test can arrange from one tree,
and it is the only check that the Handshake is real.

You need a Host that already runs a Daemon over SSH. `docs/install.md` puts one there.

## The two builds

```bash
bash scripts/handshake-builds.sh
```

It writes `build/handshake/`: `dispatch-daemon` from the tree as it stands, `dispatch-hub` from a
copy with `internal/protocol/protocol.go`'s `Version` raised by one, and a `record.txt` naming the
commit and the two versions. Keep the `record.txt` of a run you report. Everything else is
reproducible from it.

The Hub is the new build because that is the way skew happens: a machine you update is the one you
are sitting at, and the Host across the room is the one still serving last month's version.

## The run

1. Copy `dispatch-daemon` to the Host, replacing the binary there, and start it.
2. Start `dispatch-hub` on the Client machine against your usual `hub.json`.
3. Open `/hosts`.

## What has to be true

- The card for that Host reads `Incompatible`, and it names both versions: what this Hub requires,
  and what that Host answered with.
- The card is still there. A Host is never hidden for failing, and this is the one state the Hub
  stops working on rather than one it drops.
- The Daemon's log on the Host holds **one** `the Handshake was refused` line, and no more of them
  however long you leave the page open. That line is the whole evidence: the Hub asked once, was
  refused, and never dialled again. Leave it for a few minutes, because a Host on the backoff curve
  writes its second line inside one.
- Every other Host on the same Hub keeps working, and the Client's stream stays open.

A second line while that page is open is the failure this check exists to catch. It means the Hub is
retrying a Host that can never come `Ready`.

Reloading the page does write another one, and that is not the failure. Host State lives for one
Client stream, so a reload is the user asking again, which is ADR 0004's one way out of
`Incompatible`.

## Then put it back

Copy the Daemon your Host had before, or build one from the tree, and restart it. Then reload the
page: an `Incompatible` Host is never retried on its own, so the reload is what asks again.

## Runs

| date | commit | Daemon | Hub | result |
| --- | --- | --- | --- | --- |
| | | | | |
