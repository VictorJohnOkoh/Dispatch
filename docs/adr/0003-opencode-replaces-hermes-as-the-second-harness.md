# OpenCode replaces Hermes as the second Harness, and Hermes becomes a test fixture

v1 could have shipped Hermes as the second Harness, as the charting assumed. Instead Hermes leaves the v1 Harness list and stays in the repo as the adversarial test fixture, and OpenCode takes its place once a capture on a real Host proves three things. We chose this because the second Harness exists to give the user a choice of what drives the Model, and Hermes is a choice the user cannot successfully make. It has never completed a tool call on a Host.

The two halves of this decision rest on very different amounts of evidence, so they are recorded separately. Hermes leaving is settled. OpenCode entering is conditional, and the conditions are named below.

## What is settled

Hermes v0.19.0 fails the only test that matters for an entry in the Client:

- It has never completed a tool call on a Host. It answers `--version` at the Host's keyboard and fails over SSH inside its launcher chain. Eight causes were ruled out by measurement and the cause is still open.
- It emits a tool completion only for `execute` tools, never for `read` or `edit`, 12/12 consistent on Windows and on Linux. A Client that waits for a completion hangs on every file operation.
- It reports `denied by ACP client` without ever asking the client, so it cannot tell a human's refusal from an internal failure.

## What is conditional

OpenCode enters v1 only when a capture on a remote Host, over SSH, shows all three:

1. `opencode acp` starts under a supervisor that owns stdin, and a real tool call runs to completion.
2. `session/request_permission` fires separately for `read`, `edit` and `execute`.
3. Terminal Events are known per tool class, with counts.

Gate 3 asks for knowledge, not perfection. A quiet tool class does not block v1, because the adapter synthesises the terminal Event. An unknown tool class does block it.

Nothing about OpenCode is established yet. What is known comes from a version string, a `--help` listing, a `permission` block found inside the shipped binary, and a working local `provider` config on the development machine. Under the rule this project already paid to learn, none of that is evidence.

## Considered options

- **Keep Hermes in v1.** Costs nothing to decide now. Rejected: it ships a Harness that has never completed a tool call on a Host, so the choice becomes a broken entry in the Client, and every bug report against it is unbounded work while the launch failure's cause is still open.
- **Drop Hermes and ship one Harness.** Honest and smallest. Rejected as the first answer, kept as the fallback: it leaves the reason for a second Harness unmet, and it leaves the adapter seam untested against anything but Pi.
- **Replace Hermes with OpenCode, gated on a capture.** Chosen.
- **Delete the Hermes research along with the Harness.** Rejected: stdin ownership, a denial that is not a decision, and the supervisor timeout that is not a neutral observer are rules about the seam, not about Hermes. Deleting the evidence leaves the rules with nothing behind them.

## Consequences

- Hermes stays in `docs/research/` and becomes the adversarial fixture for the Harness adapter tests. Its captured transcripts replay without a GPU, which is a partial answer to the open question of how to test this system without one.
- The terminal-Event rule holds for every Harness. Each tool call gets a terminal Event, and the adapter synthesises one when the Harness stays quiet. Hermes is the fixture that proves the synthesis fires.
- Ticket #6 stays unblocked. Because Hermes remains as a fixture, the Event model keeps its degrade witness and does not wait on the OpenCode capture. Only #7 waits.
- The adapter seam stays at one shape. `opencode acp` speaks the same JSON-RPC 2.0 over ndjson as `hermes acp`, so the Harness interface still covers subprocess-with-structured-stdio and nothing else. `opencode serve` was not chosen, and it would have added a second shape.
- The Daemon owns the Approval Policy ladder on every Harness. It launches each Harness asking for everything, then answers each `session/request_permission` itself: allow for immediate, forward to the user for wait, deny for refuse. It never sets a Harness's native mode and never passes `--auto`. This makes OpenCode's missing three-mode ladder irrelevant, and it makes Pi's permission-gate extension mandatory rather than optional.
- A tool class that never asks is written as `deny` in OpenCode's own `permission` block. The class is then refused rather than ungated, and the Approval Policy is still honoured.
- A launch failure on the Host is fatal. If gate 1 fails, v1 ships Pi alone rather than a second Harness that cannot start.
- The Daemon writes a per-Session config beside the Session's working directory, rather than editing a file the user owns. Two Sessions on different Models cannot then fight over one file, and the Model chosen at Session start has somewhere to land. This assumes OpenCode discovers configuration from the working directory, which the capture must confirm. If it does not, configuration falls back to a manual per-Host prerequisite, and the Model can no longer be chosen per Session.
- Reaching all three Vendors is recorded, not gated. A Vendor that fails is a configuration bug, not grounds to reject a Harness. Pointing OpenCode at one is a `provider` block with an `@ai-sdk/openai-compatible` `baseURL` on loopback, the same shape as Pi's `models.json` write, so the Data Plane invariant holds.
- `CONTEXT.md` and the architecture sketch still name Hermes as a Harness. They change when the capture returns, so that neither claims something unproven in the meantime.

This decision does not reopen ADR-0001. The Daemon still owns Session lifecycle and admission control, and the Approval Policy rule above follows directly from that ownership.
