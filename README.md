# Dispatch

Drive AI agent harnesses that run on other machines you own, from a browser, with the model behind
each one chosen when the session starts.

A Daemon lives on each Host and owns everything that happens there: it spawns the Harness, holds its
stdin, answers its permission requests, writes every normalised Event to a durable log, and kills the
process group when you say stop. A Hub holds a connection to every Host, merges what they report, and
serves the Client. Reach is an SSH tunnel, so the Daemons bind loopback and nothing hand-written
faces the internet.

Sessions survive the laptop closing. The Event log is the transport, the replay buffer and the
history at once, so reattaching and restarting are the same mechanism.

## The documents

The architecture is decided and the code is not written yet. Read in this order.

- **[`CONTEXT.md`](CONTEXT.md)** is the vocabulary. Small, and everything else assumes it.
- **[`SPEC.md`](SPEC.md)** is the frozen v1 scope, the build order and the twelve behaviours that
  define done.
- **[`docs/adr/`](docs/adr/)** holds the arguments, twelve of them. Each title is its decision, and
  the index in [`CLAUDE.md`](CLAUDE.md) is usually the whole answer.
- **[`docs/research/`](docs/research/)** holds what was measured on real Hosts against real Vendors,
  with the raw captures beside the findings. Several decisions in the ADRs exist because a capture
  contradicted the documentation.
