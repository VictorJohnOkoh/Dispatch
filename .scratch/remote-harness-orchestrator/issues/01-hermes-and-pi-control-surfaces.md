# Hermes and Pi control surfaces

Type: research
Status: resolved
Blocked by: —

## Question

For the Hermes harness (Nous Research) and the Pi harness, establish from primary sources — official docs, the actual repositories, CLI `--help` output, source code:

1. **Output.** Does either emit machine-readable structured output (JSON lines, an event stream, a protocol) or only human-formatted text on stdout? If structured, what is the exact schema — every event kind, every field? If only text, is there a flag or mode that changes that?
2. **Tool-call visibility.** Is a tool call observable as a discrete thing before it executes, or only after, or not at all?
3. **Approval interception.** Is there any hook, flag, callback, or protocol affordance that lets an external supervisor approve or refuse a tool call before it runs?
4. **Model and vendor configuration.** How is each pointed at an OpenAI-compatible endpoint and a specific model — flags, env vars, config file? Can it be set per-invocation?
5. **Process shape.** Long-lived interactive process reading stdin, or one-shot per prompt? How is input delivered? How does it signal completion? What are its exit codes?
6. **Session resumption.** Does either have a native concept of resuming a prior conversation, and if so, what does it persist and where?

The whole Event model depends on answers to 1–3, and the Approval Policy is only implementable if 3 exists. Where a capability is absent, say so plainly and note the closest available workaround rather than inventing one.

Start from documentation and source. Where documentation is thin or contradicts itself, say which, and flag what needs empirical confirmation against a real install — that is what [Stand up a Host](./03-stand-up-a-host.md) exists to enable.

Capture findings as a Markdown file in the repo and link it from the Answer.

## Answer

Full findings: [docs/research/harness-control-surfaces.md](../../../docs/research/harness-control-surfaces.md).

Harnesses identified: **Hermes Agent** (`NousResearch/hermes-agent`, Python — the harness, not the model
family) and **Pi** (`earendil-works/pi`, TypeScript, formerly `badlogic/pi-mono`; the several `pi-harness`
repos are forks and two Rust repos are ports).

**Output.** Pi emits structured events natively: `--mode json` (one-way JSONL) and `--mode rpc`
(bidirectional LF-delimited JSONL). Full event union and a verbatim example stream are in the doc.
Hermes emits **no** structured stdout from `hermes chat`; `-z` gives final text only. Structure comes
from three separate surfaces instead: ACP stdio (`hermes acp`), TUI-gateway JSON-RPC, and an HTTP+SSE
API server (`hermes serve`, `GET /v1/runs/{id}/events`).

**Tool calls before execution.** Both, on their structured surfaces — Pi `tool_execution_start`,
Hermes `tool.start` / ACP `tool_call`.

**Approval.** Hermes: yes, three ways — `pre_tool_call` shell hook (JSON on stdin,
`{"action":"block"}` on stdout), `approval.request`/`approval.respond`, ACP `session/request_permission`.
Pi: no built-in approval and no flag; only a custom extension's blockable `tool_call` hook.

**Needs a real install:** Hermes event payload field names (documented by name only); whether Pi's
`ctx.ui.confirm` routes over RPC; whether Hermes hooks fail open; exit codes for both.
