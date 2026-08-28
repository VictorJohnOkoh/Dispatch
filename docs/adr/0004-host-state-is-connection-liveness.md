# The Hub owns a four-state Host State, and presence is connection liveness

A Host's availability could have been polled. The Hub would ask each Daemon "are you there" on a
timer, and the answer would be the Host's state. Instead the Hub holds one long-lived Event stream
per Host and the Host's state *is* the state of that stream. Presence is not a question the Hub
asks. It is a property of a connection the Hub already needs for Events.

The Hub owns this. It is the only component that knows more than one Host exists, so it is the only
component that can hold a set of Host States. A Daemon reports facts about its own Host and never
holds a Host State, not even its own.

## What the Hub can actually observe

Two measurements bound every decision below.

**The Hub is an SSH client in-process**, using `golang.org/x/crypto/ssh`, behind a one-method
`HostDialer` seam. It makes the SSH connection itself, then opens a `direct-tcpip` channel to the
Daemon's loopback port. This is the same SSH feature `ssh -L` uses, without publishing a local port.
It gives the Hub two failures instead of one:

```go
client, err := ssh.Dial("tcp", host.Addr, cfg)     // err -> unreachable
conn, err := client.Dial("tcp", "127.0.0.1:7777")  // err -> no-daemon
```

Spawning `ssh -L` was rejected. It reports a refused channel as English on stderr while staying
alive, so the second failure would arrive as text to parse. It also needs `ssh` on PATH, and this
repo has already lost captures to PATH twice: the Hermes launcher chain, and `WinError 2` on a bare
`opencode`. An externally managed tunnel was rejected because it collapses the two failures into one
`connection refused`, which removes the distinction this ADR exists to make.

**No Vendor exposes health uniformly.** From [#3](../research/vendor-discovery-apis.md), only two
facts exist on all three Vendors: whether the Vendor answers, and which Models it has loaded. Ollama
exposes neither busy nor loading, and blocks the request instead. LM Studio reports busy only
through its CLI. Only llama.cpp answers all three.

## The state machine

A state earns its place when the Hub *behaves* differently. Anything that only changes the Client's
wording is a cause field.

| State | Meaning | Hub behaviour |
| --- | --- | --- |
| `Connecting` | An attempt is in flight, or a stream has dropped and not yet failed three times | Hold the last display |
| `Ready` | Channel open, Handshake passed | Sessions may start |
| `Down{cause}` | `unreachable` or `no-daemon` | Retry with backoff, forever |
| `Incompatible` | Connected, Handshake rejected on protocol version | Stop. Wait for the user |

Every transition and its trigger:

| From | Trigger | To |
| --- | --- | --- |
| anything | The Hub starts, or the user adds a Host | `Connecting` |
| `Connecting` | Handshake passes | `Ready` |
| `Connecting` | Handshake rejected on protocol version | `Incompatible` |
| `Connecting` | Three attempts in a row fail at `ssh.Dial` | `Down{unreachable}` |
| `Connecting` | Three attempts in a row fail at the channel | `Down{no-daemon}` |
| `Ready` | Two SSE keepalives missed, or the stream closes | `Connecting` |
| `Ready` | An SSH keepalive goes unanswered | `Connecting` |
| `Down` | The backoff timer fires | `Connecting` |
| `Incompatible` | The user commands a retry | `Connecting` |

The `Down` cause comes from the most recent failed attempt, so a Host that is switched off while its
Daemon was already dead reports `unreachable` rather than keeping the older cause.

`unreachable` and `no-daemon` share one state because the Hub retries both the same way. Only the
Client's wording differs, and "go to the machine" against "start the Daemon" is a sentence, not a
transition. `Incompatible` is separate because it never fixes itself. Retrying it hammers a Host
that can never come `Ready`, and ADR 0001 named the version handshake as a failure mode to handle
explicitly.

"Vendor down" is not a Host State at all. A Host runs several Vendors, and a dead Ollama does not
stop a Session on LM Studio. "SSH tunnel broken" is not a state either. Under an in-process dialer a
broken tunnel *is* a failed SSH handshake.

Host State is derived, never stored. Every Host is `Connecting` when the Hub starts.

## Presence

An open TCP connection is not proof of life. If the Host loses power, the Hub's socket stays open
until the OS gives up, which can take minutes. So connection liveness needs bytes on a schedule, and
two different keepalives prove two different things:

- **SSH keepalive**, Hub to Host, every 15s. `sshd` answers this even when the Daemon is dead or
  deadlocked, so it proves the machine.
- **SSE keepalive**, Daemon to Hub, every 10s on the Host's Event stream. Two missed and the Hub
  kills the stream. This proves the Daemon's own loop is running.

The pair keeps the `Down` cause correct after a drop, not only at connect time. SSH fails means
`unreachable`. SSH answers while the stream is silent means `no-daemon`.

Cost is roughly six small frames per minute per Host in each direction, one SSH connection and one
channel per Host, and detection inside about 25 seconds. Polling a health endpoint would be about 12
full HTTP round trips per minute per Host over SSH, would leave the Event stream's own health
unmeasured, and would be a second mechanism to keep alive.

This requires a **Host-level** Event stream, one per Host, carrying every Session's Events. That is
an input to [#10](https://github.com/VictorJohnOkoh/Capstone/issues/10), not a free assumption.

## What a Host claims about its Vendors

A Vendor is `Reachable` or `Unreachable`, with the loaded-Model list beside it and an optional
Vendor-specific detail the Client renders only when it is present.

There is no common idle/loading/busy ladder. Filling one would force the Ollama adapter to report
"idle" for a Vendor that is blocked mid-load and cannot answer for the next thirty seconds. That is
an invented value the user acts on. The richer llama.cpp signals live in the optional detail rather
than in a shape two of three Vendors cannot fill.

The user does not need Vendor busy to choose a Host. The Daemon's admission control already answers
whether a Session may start.

The Daemon polls its own Vendors over loopback and pushes changes onto the Host stream. The Hub
never speaks to a Vendor, so the Data Plane stays on one Host. `/health`, `/props` and `/models` are
exempt from llama.cpp's idle timer, so polling does not pin a Model in memory.

## Reconnection

Exponential backoff from 1s to a 60s cap, with full jitter. The backoff resets only after the
connection has been `Ready` for 60 continuous seconds. Resetting on handshake success alone lets a
flapping Host redial at full speed forever.

A blink and a week-long outage need no separate policies. The curve already returns a blinking Host
in a second or two and costs a switched-off Host one attempt per minute. Retries never stop, because
a configured Host that the user switches on should come back without being told to. `Incompatible`
is exempt and does not retry at all.

## What the user sees

A dropped stream moves the Host to `Connecting`, and the Client keeps the row's content with a
"reconnecting" mark. Only after three failed attempts, about seven seconds, does it become `Down`.
`Connecting` absorbs the blink, so the grace period is a transition rule and not a fifth state. The
cost is up to seven seconds of content that is already wrong, which beats repainting every row twice
for a two-second network blip.

A `Down` Host keeps its place in the list. Its last-known Vendors and Sessions stay visible as
**Stale**, dimmed and stamped with the time they were true, and starting a Session is disabled.
Hiding a `Down` Host would hide the machine the Client should be telling the user to switch on, and
would leave nowhere to show the `Down` cause.

## A Session's Events during an outage

The Daemon keeps running and its Event log keeps growing. On reconnect the Hub resumes each Session
with `Last-Event-ID` and the gap replays with the same Event ids.

The Daemon writes `hub_detached` and `hub_attached` into each live Session's log. The Client draws
the outage band from those two Events. They are named from the writer's viewpoint: the Daemon never
knows whether its Host was unreachable, only that the Hub stopped listening, and the name must not
claim more than that.

These Events appear exactly when a gap exists, which is what makes them worth the cost of sitting in
a Session's history:

- Machine off, or Daemon crashed. The Sessions died with it. There is no gap, there is a
  termination.
- Machine up, network or `sshd` down. Daemon and Sessions alive, the Daemon sees its stream drop,
  and it is there to write the Events.

The alternative was a Hub-side display band with a silent replay, which keeps the Session's history
free of facts about its observer. It was rejected because it does not survive a Hub restart, and
because a durable record of what the user could not see is worth one Event per Session per outage.

## Considered options

- **Poll each Daemon for presence.** One mechanism, easy to reason about. Rejected: the Hub already
  holds a connection per Host for Events, so polling adds a second mechanism that measures something
  the first one could have told it, and leaves the Event stream's own liveness unmeasured.
- **Connection liveness with no keepalive.** Free. Rejected: a powered-off Host holds the socket
  open for minutes, so the state would be wrong for exactly as long as it matters.
- **One keepalive, at the application layer only.** One timer. Rejected: it cannot separate a dead
  machine from a dead Daemon after a drop, which gives back most of what the in-process dialer was
  chosen for.
- **Both keepalives, layered.** Chosen.
- **Five states, splitting `unreachable` and `no-daemon`.** Rejected: two states with identical
  behaviour, which invites a per-state backoff policy nobody asked for.
- **Three states, folding the version mismatch into `Down`.** Rejected: it retries a Host that can
  never come `Ready`.

## Consequences

- The Hub carries SSH authentication. Key loading, agent or passphrase handling, and `known_hosts`
  checking through `ssh/knownhosts` are all the Hub's code. It does not read `~/.ssh/config`, so a
  Host needing `ProxyJump` or a host alias needs that config expressed in the Host profile instead.
- SSH keepalives are the Hub's job. `x/crypto/ssh` has no `ServerAliveInterval`, so the Hub sends
  `keepalive@openssh.com` itself and times out the reply.
- The `HostDialer` seam gives testing a Hub with no SSH at all. A plain `net.Dial` at a local Daemon
  is the test implementation, and it is where the Tailscale reach upgrade lands later.
- [#10](https://github.com/VictorJohnOkoh/Capstone/issues/10) inherits two requirements: a
  Host-level Event stream that multiplexes every Session, and an SSE keepalive frame in the wire
  protocol. It also inherits an open question this ADR does not answer, which is what happens when a
  `Last-Event-ID` is unknown to the Daemon because its log rotated. That is a resync, and it needs a
  shape.
- [#9](https://github.com/VictorJohnOkoh/Capstone/issues/9) inherits the matching half of
  `hub_detached`. A Daemon that crashes and restarts against its durable log leaves a gap with no
  Event, so it must write `daemon_started` on boot.
- [#6](https://github.com/VictorJohnOkoh/Capstone/issues/6) gains two Event types that no Harness
  produces. `hub_detached` and `hub_attached` are written by the Daemon about itself, which makes
  them the first Events in the model with no Harness origin.
- [#11](https://github.com/VictorJohnOkoh/Capstone/issues/11) inherits a Client that must render a
  Host it cannot currently reach, with dimmed Stale content and a disabled Session start, rather
  than a list of only what works.
- The Vendor adapter interface in [#8](https://github.com/VictorJohnOkoh/Capstone/issues/8) returns
  two typed fields and one loose one. Any pressure to promote a detail key into the typed part is a
  claim that every Vendor can measure it, and Ollama is the test of that claim.
- The Client's wording and the Daemon's log deliberately disagree. The log says `hub_detached`
  because that is what the Daemon saw. The screen says "Host unreachable" because that is what the
  user must fix. The translation lives in the Client.
