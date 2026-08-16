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

> **Corrected 2026-08-14 against a live Hermes v0.19.0.** The original answer below picked the wrong
> Hermes surface, and the surface it picked does not exist. See the **CORRECTION** section at the top
> of [the research doc](../../../docs/research/harness-control-surfaces.md) for evidence; the revision
> is summarised immediately below.

### Revision (2026-08-14, empirical)

**The two Harnesses have the same integration shape, not incompatible ones.** Both are
subprocess-with-structured-stdio: `hermes acp` speaks JSON-RPC 2.0 over newline-delimited JSON, `pi
--mode rpc` speaks LF-delimited JSONL. The Harness adapter does not have to span "HTTP server" and
"subprocess".

**Hermes' HTTP+SSE run API does not exist.** `POST /v1/runs`, `GET /v1/runs/{id}/events` and
`GET /v1/capabilities` are documented but absent from v0.19.0 — they 404. The whole API is under
`/api/*`, and the one WebSocket event channel is dashboard plumbing with receive-only subscribers.
Consequence for this ticket's method: `[docs]`-only claims about this vendor are now suspect by default.

**ACP is the better surface anyway** — an open standard with published schemas, no server, no port, no
token.

**Approval, session lifecycle and model selection are already in-protocol.** `initialize` advertises
`sessionCapabilities: {fork, list, resume}`; `session/new` returns three approval modes (`default`,
`accept_edits`, `dont_ask`, switchable via `session/set_mode`) and a 43-entry model catalog (switchable
via `session/set_model`). These are not things to build on top of an opaque harness.

**Empirically confirmed:** event vocabulary, including the non-standard `usage_update` kind that is not
in the ACP schema; `stopReason: end_turn`; the `usage` object; the `tool_call` payload; `hermes -z`
exits 0; `base_url` (not `OPENAI_BASE_URL`) is the config key, settling old item 7.

**Approval interception is real, and its payload is the richest control-plane data on either harness.**
`session/request_permission` carries the full diff (`oldText`/`newText`) before the write, so a
supervisor can show exactly what will change. Two options only — `allow_once` / `deny`. Beware: its
`toolCallId` (`edit-approval-1`) does **not** match the streamed `tool_call` id (`tc-…`) for the same
edit, so approvals cannot be correlated to tool calls by id.

**Two defects found**, both in the research doc as C7 and C9:

- Hermes deadlocks on the **first tool call of any kind** — `terminal`, `search_files` and `patch` all
  hang, because they share one lazily-created execution environment whose child inherits the ACP stdin
  pipe. Seven runs, duration always within ~6s of the client's own timeout (118.8/120, 237.7/240,
  272.7/280, 294.2/300, 414.4/420, 418.8/420, 898.6/900) versus 1.18s with stdin closed up front. The
  original write-up blamed the terminal tool specifically; that was too narrow, and "avoid the terminal
  tool" is not a workaround.
- Hermes can report an edit as `"denied by ACP client"` in 0.00s **without ever asking the client** —
  three occurrences, all when `patch` followed another tool in the same turn. It fails closed, which is
  the safe direction, but it means **a denial is not evidence of a decision**. Approval Policy must not
  render "denied" as a user choice.

**Closed off:** advertising the ACP `terminal` capability would achieve nothing. `initialize` accepts
`client_capabilities` and never reads it, and Hermes-as-agent calls exactly one client method,
`request_permission` — no `fs/*`, no `terminal/*` anywhere in the implementation. So C6's "delegates
nothing" is by construction, and an orchestrator cannot observe file effects through ACP with this
harness at all.

**`tool_call_update` is emitted but not capturable on Windows** — closed as an impossibility rather than
left open. It is sent on the step *after* a tool returns, and the two escapes from the deadlock exclude
each other: hold stdin open and the tool never returns before the client's timeout; close stdin and the
tool returns in 3.08s and the turn completes internally, but Hermes tears down its writer too and the
client receives nothing after the approval response. Read the payload off `build_tool_complete` in the
source, or retest on POSIX. This does not block the Event model — the tool-completion shape is known,
just not observed.

### Pi, captured 2026-08-15

**Pi's event stream is the better of the two, and the Event model should be shaped against it.** Its
tool lifecycle is complete — `tool_execution_start` / `_update` / `_end`, full args before execution,
streaming partial output, explicit `isError`, and **one stable `toolCallId` across all three** plus the
`toolResult` message. Hermes offers a start event, no incremental output, an unreachable completion, and
an approval id that does not match its own tool-call id. Designing against Hermes and extending for Pi
would discard information Pi provides for free; do the reverse.

**Usage accounting and Vendor identity come free.** Every assistant turn carries `api`, `provider`,
`model` and a `usage` object (input/output/cacheRead/cacheWrite/reasoning/total, plus a `cost`
breakdown). Per *turn*, not per session. The Control Plane stream names the Data Plane endpoint the
Daemon routed to — a correlation that would otherwise have to be reconstructed.

**Pointing Pi at a local Vendor is a config write, not code.** `~/.pi/agent/models.json` registers an
arbitrary OpenAI-compatible endpoint with no extension, and autodetects context window and max output.
This overturns old item 4's "extension only". Trap: the key is `baseUrl`, camelCase.

**Both harnesses trap on stdin, in opposite directions.** Pi's `-p` reads stdin to EOF even with the
prompt as an argument, so it hangs forever at a terminal; `--mode rpc` is a session, not a pipe, and
`cat cmds | pi` truncates the run mid-turn (5 events vs 54). Hermes hangs because a *child* inherited
the stdin pipe (C7). **The Daemon must own stdin explicitly for every Harness** rather than inherit it —
that is now a supervision requirement, not a detail.

**`--mode rpc` is a superset of `--mode json`** — same event stream plus an inbound command channel,
including the full tool lifecycle. Extension loading works there, `agent_settled` is present, and a
command's optional `id` is echoed back on its response, which is the correlation handle the Daemon
needs. So the Daemon builds against RPC and loses nothing, and one event adapter serves both modes.

**Confirmed:** nothing is gated in Pi (no approval event for `bash: ls -la`), so Approval Policy cannot
be uniform; all Pi modes exit 0 on success.

### Approval, settled on both Harnesses (2026-08-16)

**Both can gate. Neither reports a refusal the orchestrator can trust structurally.** This is the
load-bearing conclusion for Approval Policy, and it is the same on both:

- Hermes cannot distinguish a human's refusal from an internal failure — it reports both as
  `"denied by ACP client"` (C9).
- Pi does not mark refusal as anything but a tool error: `isError: true` with free text chosen by the
  extension author, not by the protocol (P7).

**So the Approval Policy must record its own decisions and treat Harness output as corroboration, not
as the source of truth.** It cannot reconstruct what the user chose by reading Events.

**Hermes fails in both directions at once.** `pre_tool_call` fails **open** — every failure path in
`agent/shell_hooks.py` (crash, missing, non-executable, timeout, non-zero exit) returns "no block", so
a broken hook silently permits (C11). The ACP edit path fails **closed** (C9). Same product, opposite
defaults, so a guarantee proven for one does not transfer to the other. Shell hooks additionally need
first-use consent recorded in an allowlist; a non-TTY caller — which the Daemon is — must pass
`--accept-hooks` or the hook never registers at all.

**Pi's gate works over RPC**, via `extension_ui_request` / `extension_ui_response` (P7). Verified both
ways against the filesystem with Pi's bundled `permission-gate.ts`. Two structural traps: the UI request
carries **no `toolCallId`** (correlation is by ordering; the command is embedded in a display string),
and **`tool_execution_start` fires before the gate resolves** — a start event is not evidence that
anything ran. Pi also supports a dialog `timeout` with agent-side auto-resolve, which Hermes has no
equivalent of.

### Platform, settled 2026-08-16 (WSL2 Ubuntu, identical build)

**Both Hermes defects are Windows-specific.** The stdin deadlock (C7) vanished: the same terminal
prompt at the same 120s timeout finished in **1.73s** instead of 118.83s, and `session/prompt` returned
`end_turn`. The phantom denial (C9) did not occur once in three runs of the exact condition that
produced it 3/3 on Windows — including one run with 12 tool calls and 4 real approvals. My earlier
prediction that C9 was platform-independent was wrong.

**`tool_call_update` is captured** (C12). Its `toolCallId` **matches** its `tool_call`, so C8's id
mismatch is specific to the approval request, not to the lifecycle. Its result is prose rather than
structure — the exit code is embedded in a markdown string, so reading it means parsing display text.

**But a real gap survives the platform change, and it is what now constrains the Event model:** Hermes
emits `tool_call_update` **only for `kind: "execute"`**. Never for `read` or `edit` — 12/12 consistent
across the Linux runs. A Client that renders a `tool_call` as "running" and waits for its completion
will **hang forever on every file operation**. The Event model must synthesise a terminal state for
those kinds, or treat them as fire-and-forget. Choosing a different host does not fix it.

**Nothing from the original open list remains.** The one live constraint is the completion gap above.

Captures: `docs/research/captures/hermes/`. Tooling: `scripts/capture-hermes.sh`, `scripts/acp-capture.py`.

---

### Original answer (2026-08-07, from documentation and published source)

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
