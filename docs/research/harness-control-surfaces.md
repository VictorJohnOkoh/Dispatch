# Harness Control Surfaces: Hermes and Pi

Research for [issue 01 — Hermes and Pi control surfaces](../../.scratch/remote-harness-orchestrator/issues/01-hermes-and-pi-control-surfaces.md).

Date: 2026-08-07. All claims below are tagged `[source]`, `[docs]`, or `[inference]`.

- `[source]` — read from the project's own source code in its repository.
- `[docs]` — read from first-party documentation in the project's repository or its official docs site.
- `[inference]` — my reasoning from the above, not stated by the project.

---

## 0. Identifying the two harnesses

### Hermes

**Hermes Agent**, by Nous Research — <https://github.com/NousResearch/hermes-agent>, docs at
<https://hermes-agent.nousresearch.com/docs>. Python. This is the *harness* (a CLI/TUI agent with tools,
skills, sessions, a messaging gateway, an ACP server and an HTTP API server), **not** the Hermes *model*
family (Hermes 2/3/4, the fine-tuned Llama/Mistral weights). The harness is model-agnostic and can be
pointed at any provider — see §4. `[docs]`

Entry point binary: `hermes`. `[docs]`

### Pi

**Pi**, the coding-agent harness at <https://github.com/earendil-works/pi>. The npm scope is
`@earendil-works/pi-coding-agent`; the repo was previously published as `badlogic/pi-mono` (Mario
Zechner), and GitHub still resolves `badlogic/pi-mono` paths to the same tree. TypeScript/Node. `[docs]`

**Why this one.** Search for "pi coding agent harness" turns up several repos, and they are all
downstream of the same project:

| Candidate | Relationship |
| --- | --- |
| `earendil-works/pi` | **The upstream project.** Self-described "AI agent toolkit: unified LLM API, agent loop, TUI, coding agent CLI". This is the one this document covers. |
| `badlogic/pi-mono` | Former path of the same repository; links still resolve. |
| `tibormester/pi-harness`, `werg/pi-harness` | Forks/mirrors of the above ("AI agent toolkit"). |
| `nktkt/pi` | A third-party **Rust port** of `earendil-works/pi`. Explicitly a port, so its surfaces may lag. |
| `can1357/oh-my-pi` | A third-party fork with extra tooling (LSP, browser, subagents). |
| `Dicklesworthstone/pi_agent_rust` | Another third-party Rust reimplementation. |

There is also **Pi by Inflection AI**, a consumer chatbot. It is not a harness, has no CLI, and is
ruled out. `[inference]`

Everything below refers to `earendil-works/pi`. Where a fork matters (the Rust port in particular), the
answers do **not** automatically carry over.

---

## 1. Output — is it machine-readable?

### Pi — **Yes. Two structured modes, both first-class.**

Pi documents four modes: interactive TUI, print (`-p`), **JSON event stream (`--mode json`)**, and
**RPC (`--mode rpc`)**, plus an embeddable SDK.
`[docs]` <https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/index.md>

#### `--mode json` — one-way JSONL event stream on stdout

The wire type is `JsonAgentSessionEvent`, defined as `AgentSessionEvent` minus the cumulative snapshot
on message updates:

```typescript
type WithoutPartial<T> = T extends { partial: unknown } ? Omit<T, "partial"> : T;

type JsonAgentSessionEvent =
  | Exclude<AgentSessionEvent, { type: "message_update" }>
  | {
      type: "message_update";
      assistantMessageEvent: WithoutPartial<AssistantMessageEvent>;
    };
```

The base union it derives from:

```typescript
type AgentEvent =
  | { type: "agent_start" }
  | { type: "agent_end"; messages: AgentMessage[] }
  | { type: "turn_start" }
  | { type: "turn_end"; message: AgentMessage; toolResults: ToolResultMessage[] }
  | { type: "message_start"; message: AgentMessage }
  | { type: "message_update"; message: AgentMessage; assistantMessageEvent: AssistantMessageEvent }
  | { type: "message_end"; message: AgentMessage }
  | { type: "tool_execution_start"; toolCallId: string; toolName: string; args: any }
  | { type: "tool_execution_update"; toolCallId: string; toolName: string; args: any; partialResult: any }
  | { type: "tool_execution_end"; toolCallId: string; toolName: string; result: any; isError: boolean };
```

`[docs]` <https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/json.md>

**Verbatim example stream** (this is the most useful artifact in this document — the event model should
be designed against it):

```json
{"type":"session","version":3,"id":"uuid","timestamp":"...","cwd":"/path"}
{"type":"agent_start"}
{"type":"turn_start"}
{"type":"message_start","message":{"role":"assistant","content":[],...}}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"Hello"}}
{"type":"message_end","message":{...}}
{"type":"turn_end","message":{...},"toolResults":[]}
{"type":"agent_end","messages":[...]}
```

Notes that matter for a normaliser: `message_end` carries the **authoritative** final message;
`message_update` deltas deliberately omit the cumulative snapshot, so a consumer that wants running text
must accumulate deltas itself, or wait for `message_end`. `[docs]`

The stream is preceded by a `session` header line carrying `version`, `id`, `timestamp`, `cwd` — the
same header used in the on-disk session file (§6), so the JSON mode is effectively the session log
tee'd to stdout plus live streaming events. `[inference]`

Additional event types present in the full `AgentSessionEvent` union beyond the base `AgentEvent`:
`queue_update`, `compaction_start`, `compaction_end`, `auto_retry_start`, `auto_retry_end`,
`summarization_retry_scheduled`, `summarization_retry_attempt_start`, `summarization_retry_finished`.
`[docs]` <https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/sdk.md>

`AssistantMessageEvent` sub-types seen in docs: `text_delta`, `thinking_delta`, and tool-call deltas.
The exhaustive field list for each is **not** in the docs — read it from
`packages/coding-agent/src` at a pinned version, or capture it empirically. `[inference]`

#### `--mode rpc` — bidirectional JSONL over stdin/stdout

Framing is explicit and strict:

> "Uses strict JSONL with LF (`\n`) as the sole delimiter. Clients should split on `\n` only and strip
> trailing `\r` from input. Node's `readline` module is incompatible due to Unicode line separator
> handling."

`[docs]` <https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/rpc.md>

That last sentence is a real trap for a Go implementation too: `bufio.Scanner` with `ScanLines` splits
on `\n` and strips a trailing `\r`, which is exactly the documented requirement — but do **not** use a
splitter that also honours U+2028/U+2029. `[inference]`

Commands go to stdin one JSON object per line. Replies have `type: "response"` with success/failure and
an optional `id` for correlation. Events stream to stdout as JSON lines.

RPC event stream (superset of JSON mode):

| Event | Purpose |
| --- | --- |
| `agent_start` / `agent_end` / `agent_settled` | agent lifecycle |
| `turn_start` / `turn_end` | turn boundaries, with message and tool results |
| `message_start` / `message_end` | message lifecycle |
| `message_update` | streaming deltas (text, thinking, tool calls) |
| `bash_execution_update` | output chunks from a direct `bash` RPC command |
| `tool_execution_start` / `tool_execution_update` / `tool_execution_end` | tool invocation lifecycle |
| `queue_update` | steering / follow-up queue changes |
| `compaction_start` / `compaction_end` | context compression, with statistics |
| `auto_retry_start` / `auto_retry_end` | transient-error recovery |
| `summarization_retry_*` | compaction / branch-summary retries |
| `extension_error` | extension failures |
| `extension_ui_request` / (client sends) `extension_ui_response` | see §3 |

`[docs]` rpc.md.

`agent_settled` exists only in RPC mode's list, not in the `--mode json` documentation. Treat "the agent
is done and will not auto-retry" as RPC-only until confirmed. `[inference]`

#### `-p` / `--print`

Plain text, no structure, exits when done. Reads piped stdin and merges it into the initial prompt:
`cat README.md | pi -p "Summarize this text"`. `[docs]` usage.md.

### Hermes — **No structured stdout from the normal CLI. Three separate protocol surfaces instead.**

`hermes chat` and `hermes` produce a human TUI. The non-interactive knobs are:

- `hermes chat -q "..."` — one-shot query, human-formatted.
- `hermes chat -Q` / `--quiet` — "Programmatic mode: suppress banner/spinner/tool previews."
- `hermes -z "<prompt>"` — "Single prompt in, final response text out, nothing else on stdout or
  stderr." No banner, no spinner, no tool previews, no `Session:` line. Documented usage:
  `answer=$(hermes -z "summarize this" < /file.txt)`.
- `--usage-file <path>` — writes a JSON usage report **after** the run, containing
  `estimated_cost_usd`, `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens`,
  `reasoning_tokens`, `total_tokens`, `api_calls`, `model`, `provider`, `session_id`, `service_tier`,
  and `completed` / `failed` flags.

`[docs]` <https://hermes-agent.nousresearch.com/docs/reference/cli-commands>

So: **there is no `--json` / `--stream-json` flag on `hermes chat` that turns the turn into an event
stream.** `--json` exists on various read-only commands (`hermes status`, listings, etc.), not on the
agent loop. `[docs]` cli-commands.md. This is a plain, unambiguous "no" — do not try to parse the TUI.

Hermes instead documents **three** programmatic protocols:

> "**ACP (Agent Client Protocol)** uses JSON-RPC over stdio... **TUI Gateway** provides JSON-RPC over
> stdio (or WebSocket)... **API Server** exposes HTTP + Server-Sent Events."

`[docs]` <https://hermes-agent.nousresearch.com/docs/developer-guide/programmatic-integration>

#### (a) ACP — `hermes acp`

JSON-RPC over stdio, the Zed/Agent Client Protocol. Also reachable as `hermes-acp` or
`python -m acp_adapter`. Requires an extra: `uv pip install -e '.[acp]'`. `[docs]` cli-commands.md.

> "Stdout is reserved for ACP JSON-RPC transport. Human-readable logs go to stderr."

`[source]` (documented from `acp_adapter/server.py`, `acp_adapter/events.py`)
<https://hermes-agent.nousresearch.com/docs/developer-guide/acp-internals>

The event bridge (`acp_adapter/events.py`) converts three `AIAgent` callbacks —
`tool_progress_callback`, `thinking_callback`, `step_callback` — into ACP `session_update`
notifications. `[source]`

Because it is ACP, the event vocabulary is the **ACP spec's**, not a Hermes-specific one: `session/new`,
`session/load`, `session/prompt`, `session/cancel`, `session/update` notifications carrying
`agent_message_chunk`, `agent_thought_chunk`, `tool_call`, `tool_call_update`, and
`session/request_permission`. Hermes does not restate the ACP schema in its own docs — read it from
<https://agentclientprotocol.com>. `[inference]`

#### (b) TUI Gateway JSON-RPC — the richest surface

Newline-delimited JSON-RPC over stdio (`tui_gateway/entry.py` + `tui_gateway/server.py`) or WebSocket
(`tui_gateway/ws.py`). This is the backbone of Hermes's own Ink TUI, so it is guaranteed to expose
everything the TUI shows. `[source]`

Documented method catalogue (the docs call this "selected", i.e. not exhaustive):

```
prompt.submit, prompt.background, session.steer, session.create, session.list,
session.active_list, session.activate, session.close, session.interrupt, session.history,
session.compress, session.branch, session.title, session.usage, session.status,
clarify.respond, sudo.respond, secret.respond, approval.respond,
config.set / config.get, commands.catalog, command.resolve, command.dispatch,
cli.exec, reload.mcp, reload.env, process.stop, delegation.status,
subagent.interrupt, subagent.steer, spawn_tree.save / list / load,
terminal.resize, clipboard.paste, image.attach
```

Documented events:

```
message.delta, message.complete, tool.start, tool.progress, tool.complete,
approval.request, clarify.request, sudo.request, sudo.expire, secret.request,
secret.expire, gateway.ready, plus session lifecycle and error events.
```

> "Expiry events carry the original `{ request_id }`; external hosts should clear only the matching
> pending prompt."

`[docs]` programmatic-integration.md.

Note the gateway also stipulates that Hermes redirects Python `print()` to stderr so stdout stays clean
for the protocol. `[source]` (per repo issue discussion, see below)

**Known operational hazard, from the repo's own issue tracker:** every RPC handler in
`tui_gateway/server.py` historically ran synchronously on the single stdin-read loop in
`tui_gateway/entry.py`, so a slow handler blocks the dispatcher and inbound RPCs — *including
`approval.respond`* — sit unread in the pipe.
`[source]` <https://github.com/NousResearch/hermes-agent/issues/12546> (a fix routing slow handlers to a
`ThreadPoolExecutor` is described there). Verify against whatever version gets installed.

#### (c) HTTP API server — `hermes serve` / `hermes gateway`

Enabled with `API_SERVER_ENABLED=true` and `API_SERVER_KEY=...` in `~/.hermes/.env`; bearer-token auth
via `Authorization`. `[docs]`
<https://hermes-agent.nousresearch.com/docs/user-guide/features/api-server>

Endpoints:

```
POST /v1/chat/completions      OpenAI Chat Completions, stateless, SSE streaming
POST /v1/responses             OpenAI Responses API, server-side state via previous_response_id
POST /v1/runs                  start an agent run
GET  /v1/runs/{run_id}         poll run state
GET  /v1/runs/{run_id}/events  SSE stream of the run's tool-call progress, token deltas, lifecycle
POST /v1/runs/{run_id}/stop    interrupt
POST /v1/runs/{run_id}/approval  resolve a pending approval
GET  /v1/capabilities          machine-readable description of the stable surface
GET  /v1/models
GET  /api/model/options
GET  /health, /health/detailed
```

`[docs]` api-server.md, programmatic-integration.md.

The `/v1/runs/{id}/events` SSE stream carries the same `tool.start` / `tool.progress` / `tool.complete`
/ `approval.request` vocabulary as the TUI gateway, plus `subagent.start` / `subagent.complete` when
work is delegated. `[docs]` api-server.md.

**Gap.** Neither the docs nor the reachable part of `gateway/platforms/api_server.py` spell out the
per-event JSON payload keys for `tool.start` etc. The source file is large and truncates before the
handler bodies when fetched over HTTP. **This is the single biggest thing to capture empirically** —
see §7.

---

## 2. Tool-call visibility — before, after, or not at all?

### Pi — **Before execution, as a discrete event, in every structured mode.**

The documented event order for one prompt:

```
input → before_agent_start → agent_start
  → turn_start
  → context (can modify messages)
  → [LLM call]
  → tool_execution_start
  → tool_call (can block)
  → [tool executes]
  → tool_execution_update (streaming)
  → tool_execution_end
  → tool_result (can modify result)
  → turn_end
  → agent_end
  → agent_settled (when no more auto-retries)
```

`[docs]` <https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/extensions.md>

`tool_execution_start` carries `{ toolCallId, toolName, args }` and is emitted on the JSON/RPC wire
**before** the tool runs. So an external observer sees the full, resolved arguments of a tool call
before its side effects happen. `[docs]` json.md + extensions.md.

Note the ordering subtlety: `tool_execution_start` fires *first*, and the blockable `tool_call` hook
fires *after* it. So on the wire you see the start event, then either the tool's updates/end or a
blocked result. `[inference]`

Streaming tool-call argument deltas also arrive earlier still, inside `message_update` —
"streaming deltas (text, thinking, tool calls)". `[docs]` rpc.md.

### Hermes — **Before execution, but only on the protocol surfaces.**

- TUI gateway / API server: `tool.start` → `tool.progress` → `tool.complete`, and separately
  `approval.request` when a call is gated. `[docs]`
- ACP: `tool_call` / `tool_call_update` `session/update` notifications, bridged from
  `tool_progress_callback`. `[source]` acp-internals.md.
- Plugin hooks (in-process, Python): `pre_tool_call` fires before, `post_tool_call` after. `[docs]`
- Shell hooks (subprocess): `pre_tool_call` / `post_tool_call`. `[docs]`

On plain `hermes chat` stdout: tool previews are rendered as human text and are suppressed entirely by
`-Q` / `-z`. Not machine-readable. `[docs]`

---

## 3. Approval interception — can an external supervisor gate a tool call?

**Both, yes — but by completely different mechanisms, and one of Pi's is easy to misread.**

### Hermes — yes, three independent ways. This is a genuine strength.

#### (a) Shell hooks — the best fit for an out-of-process Go daemon

A `pre_tool_call` shell hook is a subprocess that receives the tool call as JSON on stdin and answers
with a decision as JSON on stdout. Configuration in `~/.hermes/config.yaml`:

```yaml
hooks:
  pre_tool_call:
    - matcher: "terminal"
      command: "~/.hermes/agent-hooks/block-dangerous.sh"
      timeout: 5

  post_tool_call:
    - matcher: "write_file|patch"
      command: "~/.hermes/agent-hooks/auto-format.sh"
```

The payload delivered on the hook's stdin, verbatim from the docs:

```json
{
  "hook_event_name": "pre_tool_call",
  "tool_name": "terminal",
  "tool_input": {"command": "rm -rf /"},
  "session_id": "sess_abc123",
  "extra": {"task_id": "..."}
}
```

The decision the script writes to stdout, verbatim:

```json
{"action": "block", "message": "Forbidden operation"}
{"context": "Injected context for pre_llm_call"}
{"action": "continue", "message": "Format code first"}
```

`[docs]` <https://hermes-agent.nousresearch.com/docs/user-guide/features/hooks>

`matcher` is a regex and applies only to tool-scoped events. There is a **consent gate**: the first time
a shell hook runs, Hermes prompts for approval unless `--accept-hooks`, `HERMES_ACCEPT_HOOKS=1`, or
`hooks_auto_accept: true` is set; decisions persist to `~/.hermes/shell-hooks-allowlist.json`. A headless
daemon **must** set one of those or the very first hook invocation will hang on a prompt. `[docs]`

Utilities: `hermes hooks list`, `hermes hooks test <event>` (fires hooks against synthetic payloads —
useful for a Go-side integration test), `hermes hooks doctor`. `[docs]`

**Caveat.** The docs state all four hook systems have "non-blocking error handling — failures in any hook
are logged but never crash the agent". A hook that times out or crashes therefore does **not**
fail closed into a deny; it is logged and the agent proceeds. For an approval policy this is the wrong
default and must be verified empirically — a supervisor that dies must not silently become
permit-everything. `[inference]` from `[docs]`.

**Caveat 2.** The documented hook payload keys are `tool_name` / `tool_input`, and the shell-hook
`timeout` is in seconds. There is no documented way to make the *timeout itself* deny. `[docs]`

#### (b) Protocol-level approval requests

- TUI gateway: `approval.request` event out, `approval.respond` method in. There are matching expiry
  events carrying the original `{ request_id }`. `[docs]`
- API server: `approval.request` on the SSE stream, `POST /v1/runs/{run_id}/approval` to resolve it.
  Advertised in `GET /v1/capabilities` as the `run_approval` feature so a client can detect support
  before showing an approval UI. `[docs]` api-server.md.
- ACP: `session/request_permission`. `acp_adapter/permissions.py` builds the request via
  `_build_permission_tool_call()` with an id shaped `"perm-check-{N}"` and an update carrying
  `title=title, kind='execute', status='pending'`. Option mapping, verbatim from
  `_OPTION_ID_TO_HERMES`:

  | ACP option id | Hermes decision |
  | --- | --- |
  | `allow_once` | `once` |
  | `allow_session` | `session` |
  | `allow_always` | `always` |
  | `deny` | `deny` |
  | `deny_always` | `deny` |

  Default timeout `60.0` seconds; on timeout the future is cancelled and the result is `"timeout"`,
  distinct from an explicit denial. Invalid outcomes, missing futures, exceptions, `None` responses, and
  unknown `option_id` values all fall back to `"deny"`.
  `[source]` `acp_adapter/permissions.py`, via
  <https://hermes-agent.nousresearch.com/docs/developer-guide/acp-internals>

  The ACP docs are explicit that the host decides: "Whether you actually see a prompt is up to the host.
  A host is free to answer the request programmatically instead of showing it to you." `[docs]` acp.md.
  That sentence is the licence for a daemon to be the approver.

#### (c) In-process Python plugin hooks

`ctx.register_hook()` registers `pre_tool_call`, documented as "Block a tool call and return an error to
the model". Also blockable: `pre_llm_call`, `pre_verify`. Observer-only: `post_tool_call`,
`pre_approval_request`, `post_approval_response`, session/subagent lifecycle. Transform hooks
(`transform_tool_result`, `transform_terminal_output`, `transform_llm_output`) can rewrite content but
not gate. `[docs]` hooks.md.

#### What triggers a *built-in* Hermes approval

Only a curated dangerous-pattern list (`tools/approval.py`) — `rm -r`, writes to `/etc/` or `~/.ssh/`,
`chmod 777`, `DROP TABLE`, `curl | sh`, `systemctl stop`, etc. Ordinary tool calls are **not** gated by
default. The interactive prompt is:

```
⚠️  DANGEROUS COMMAND: [description]
    [command text]

    [o]nce  |  [s]ession  |  [a]lways  |  [d]eny

    Choice [o/s/a/D]:
```

Config:

```yaml
approvals:
  mode: smart                     # smart | manual | off
  timeout: 300                    # seconds
  cron_mode: deny                 # deny | approve (headless jobs)
  mcp_reload_confirm: true
  destructive_slash_confirm: true
```

`smart` (default) uses an auxiliary LLM to risk-assess and auto-approves low risk; `manual` always
prompts; `off` disables checks. Timeout defaults to deny after 300s. `[docs]`
<https://hermes-agent.nousresearch.com/docs/user-guide/security>

Also: `approvals.deny` fnmatch globs block **before** `--yolo` / `/yolo` / `mode: off` are consulted; a
hardline blocklist (`rm -rf /`, fork bombs, `mkfs.*`, raw device writes) can never be overridden; and
dangerous-command checks are **skipped entirely** when running under a `docker` / `modal` /
`singularity` backend, because the container boundary is treated as the security mechanism. `[docs]`

`--yolo`, `/yolo`, and `HERMES_YOLO_MODE=1` bypass approvals. `[docs]`

**Important design consequence.** If the daemon wants to gate *every* tool call rather than only the
dangerous ones, the built-in approval system is the wrong lever — set `approvals.mode` to taste and put
the real gate in a `pre_tool_call` shell hook (or supervise `approval.request` on the gateway/API
surface, accepting that it only fires for gated calls). `[inference]`

### Pi — yes, but **only via an extension**. There is no built-in approval and no CLI flag.

State this plainly, because Pi's own docs say it twice:

> "Built-in tools can read files, write files, edit files, and run shell commands with the permissions
> of the pi process." ... "There is no tool approval prompt."

`[docs]` <https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/security.md>

> pi "intentionally does not include built-in MCP, sub-agents, permission popups, plan mode, to-dos, or
> background bash" — these are left to extensions.

`[docs]` usage.md.

Pi's "project trust" (`.pi/settings.json`, `~/.pi/agent/trust.json`, `defaultProjectTrust`,
`--approve` / `-a`, `--no-approve` / `-na`) is **not** a tool gate:

> "Project trust is only an input-loading guard. It prevents a repository from silently changing pi's
> settings or extensions before you approve it. It does not make untrusted code, untrusted prompts, or
> untrusted model output safe."

`[docs]` security.md. Do not model this as an approval mechanism.

**The real interception point is the `tool_call` extension hook**, which the docs describe as:

> "Fired after `tool_execution_start`, before the tool executes. **Can block.**"

```typescript
pi.on("tool_call", async (event, ctx) => {
  // event.toolName - "bash", "read", "write", "edit", etc.
  // event.toolCallId - unique identifier
  // event.input - tool parameters (mutable)

  return { block: true, reason: "Dangerous command", terminate: true };
});
```

Return `{ block: true, reason?: string, terminate?: boolean }` to deny. `terminate: true` additionally
stops the agent from making automatic follow-up calls. `event.input` is **mutable** — handlers can patch
arguments before execution, and mutations chain across handlers. `[docs]` extensions.md.

Documented permission-gate pattern, verbatim:

```typescript
pi.on("tool_call", async (event, ctx) => {
  if (event.toolName === "bash" &&
      event.input.command?.includes("rm -rf")) {
    const ok = await ctx.ui.confirm("Dangerous!", "Allow rm -rf?");
    if (!ok) return { block: true, reason: "Blocked by user" };
  }
});
```

`[docs]` extensions.md.

**How that reaches an external Go supervisor.** In `--mode rpc`, the extension UI protocol is on the
wire: extensions raise dialog methods `select`, `confirm`, `input`, `editor`, which are emitted as
requests and **expect an `extension_ui_response` from the RPC client**, correlated by `id`, carrying
`value` / `confirmed` / `cancelled`. Fire-and-forget methods (`notify`, `setStatus`, `setWidget`,
`setTitle`, `set_editor_text`) expect no reply. `[docs]` rpc.md.

So the working design for Pi is: **ship a small first-party Pi extension loaded with `-e` whose
`tool_call` handler calls `ctx.ui.confirm(...)`; in RPC mode that confirm surfaces to the daemon as an
extension-UI request, and the daemon's reply becomes the allow/deny.** This is composed from two
documented mechanisms rather than being a single documented feature — flag it as **must be
empirically confirmed**, in particular whether `ctx.ui.confirm` is routed over RPC (versus only over the
TUI) and whether it works at all in `--mode json` (which is a one-way stream and has no stdin channel to
answer on — I expect it does **not**). `[inference]`

If that composition fails, the fallback is the **SDK**: `createAgentSession()` in-process in Node with
the extension registered directly, and the Go daemon talking to that Node shim over its own protocol.
Heavier, but fully supported. `[inference]`

---

## 4. Model and vendor configuration

### Pi — per-invocation flags, first-class, and a documented OpenAI-compatible path

```
--provider <name>          # Provider identifier
--model <pattern>          # Pattern/ID with optional :<thinking>
--api-key <key>            # API key override
--thinking <level>         # off, minimal, low, medium, high, xhigh, max
--models <patterns>        # Comma-separated, for Ctrl+P cycling
--list-models [search]     # Display available models
```

`[docs]` README / usage.md. All settable per invocation; `pi --model my-provider/my-model "prompt"` is
the documented form.

Built-in providers include Anthropic, OpenAI, Azure OpenAI, DeepSeek, NVIDIA NIM, Google Gemini, Google
Vertex, Amazon Bedrock, Mistral, Groq, Cerebras, Cloudflare, xAI, OpenRouter, Vercel AI Gateway, ZAI,
OpenCode, Hugging Face, Fireworks, Together AI, Baseten, Kimi, MiniMax, Xiaomi MiMo, **llama.cpp**, plus
subscription auth for Claude Pro/Max, ChatGPT Plus/Pro and GitHub Copilot. `[docs]` README.

An arbitrary OpenAI-compatible endpoint is registered by an extension, verbatim:

```typescript
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

export default function (pi: ExtensionAPI) {
  pi.registerProvider("my-provider", {
    name: "My Provider",
    baseUrl: "https://api.example.com/v1",
    apiKey: "$MY_PROVIDER_API_KEY",
    api: "openai-completions",
    models: [
      {
        id: "my-model",
        name: "My Model",
        reasoning: false,
        input: ["text", "image"],
        cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
        contextWindow: 128000,
        maxTokens: 4096
      }
    ]
  });
}
```

`apiKey` supports `$ENV_VAR` / `${ENV_VAR}` resolved at runtime. `api: "openai-completions"` selects the
OpenAI-compatible streaming implementation. Supplying `models` **replaces** all existing models for that
provider. `[docs]`
<https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/custom-provider.md>

**So pointing Pi at an arbitrary vLLM/SGLang endpoint requires shipping an extension file**, not just a
flag. That extension can be generated by the daemon per session and passed with `-e`. `[inference]`

Environment variables Pi itself sets or reads:

```
PI_CODING_AGENT=true          # set inside pi
AI_AGENT=pi                   # process attribution
PI_CODING_AGENT_DIR           # config dir override
PI_OFFLINE=1
PI_SKIP_VERSION_CHECK=1
PI_TELEMETRY={0|1}
PI_CACHE_RETENTION=long
```

And the bash tool receives `PI_SESSION_ID`, `PI_SESSION_FILE`, `PI_PROVIDER`, `PI_MODEL`,
`PI_REASONING_LEVEL`. `[docs]` README / environment-variables.md. `PI_CODING_AGENT_DIR` is the useful
one for per-session isolation. `[inference]`

### Hermes — env vars and `config.yaml`, with per-run flag overrides

Per-invocation:

```
-m, --model <model>       # override model for this run
--provider <provider>     # force a specific provider
```

available on both `hermes chat` and `hermes -z`. `[docs]` cli-commands.md.

Environment variables, verbatim descriptions:

- `HERMES_INFERENCE_MODEL` — "Force the model for `hermes -z` / `hermes chat` without mutating
  `config.yaml`" ← **the right lever for a daemon.**
- `HERMES_MODEL` — "Override model name at process level (used by cron scheduler; prefer `config.yaml`
  for normal use)"
- `OPENAI_BASE_URL` — "Base URL for custom endpoint (VLLM, SGLang, etc.)"
- `OPENAI_API_KEY` — "API key for custom OpenAI-compatible endpoints (used with `OPENAI_BASE_URL`)"
- `OPENROUTER_API_KEY`, `OPENROUTER_BASE_URL`, `ANTHROPIC_API_KEY`, `ANTHROPIC_BASE_URL`
- `HERMES_HOME` — "Override Hermes config directory (default: `~/.hermes`)" ← per-session isolation.
- `HERMES_IGNORE_USER_CONFIG` — skip `~/.hermes/config.yaml`, use built-in defaults.
- `HERMES_YOLO_MODE=1`, `HERMES_EXEC_ASK`, `HERMES_WRITE_SAFE_ROOT`
- `HERMES_QUIET`, `HERMES_TUI`

`[docs]` <https://hermes-agent.nousresearch.com/docs/reference/environment-variables>

Config-file form for a custom OpenAI-compatible gateway:

```yaml
providers:
  my-gateway:
    api: https://llm.internal.example.com/v1
    api_key: sk-...
    extra_headers:
      CF-Access-Client-Id: "xxxx.access"
      CF-Access-Client-Secret: "yyyy"
```

`api` is the endpoint URL (the doc notes it "replaces `base_url` in newer configs"); `discover_models:
false` skips the `/models` probe and uses only manually listed model names. `extra_headers` is supported
on OpenAI-compatible routes but **not** on `anthropic_messages` or `bedrock_converse`. `[docs]`
configuring-models.md.

**The `base_url` → `api` key rename is exactly the kind of doc-vs-install drift that should be verified
on a real host.** `[inference]`

Interactive setup is `hermes model` (a wizard with OAuth flows / API keys / endpoint config); mid-session
switching is `/model <name> --provider <provider>` and works on all three protocol surfaces. `[docs]`

Hermes also ships `hermes proxy` — "Local OpenAI-compatible HTTP proxy with OAuth credential attachment"
— and `hermes egress`, a credential-injection firewall for remote sandboxes. Both are potentially
relevant to a remote-host design. `[docs]` cli-commands.md.

---

## 5. Process shape

### Pi

| Mode | Shape |
| --- | --- |
| default | Long-lived interactive TUI. |
| `-p` / `--print` | **One-shot.** Prompt from argv; piped stdin is merged into the initial prompt; prints and exits. |
| `--mode json` | One-shot per prompt, events to stdout as JSONL. Stdin is the piped-prompt channel, not a command channel. `[inference]` |
| `--mode rpc` | **Long-lived.** Reads commands from stdin, writes responses + events to stdout, both LF-delimited JSONL. This is the mode to build a daemon against. |
| SDK | In-process Node, `createAgentSession()` / `createAgentSessionRuntime()`. |

`[docs]` index.md, usage.md, json.md, rpc.md, sdk.md.

Input in RPC mode: `prompt` (with optional images, and `streamingBehavior` of `"steer"` or `"followUp"`
when the agent is already active), `steer` (delivered after the current tool call completes),
`follow_up` (delivered when the agent finishes), `abort`, `new_session`. There is also a direct `bash`
command that streams `bash_execution_update` events, and `abort_bash`. `[docs]` rpc.md.

Completion signalling: `agent_end` ends a run; `agent_settled` indicates no further auto-retries are
pending. In `-p`/`--mode json` the process exiting is the terminal signal. `[docs]` + `[inference]`.

Files can be attached by argv with an `@` prefix: `pi @file.ts "review this"`. `[docs]` README.

**Exit codes are not documented anywhere in Pi's docs.** I could not find a statement about them.
Do not guess. `[docs]` index.md explicitly contains nothing on exit codes.

### Hermes

| Invocation | Shape |
| --- | --- |
| `hermes` / `hermes chat` | Long-lived interactive TUI. |
| `hermes chat -q "..."` | **One-shot.** |
| `hermes -z "<prompt>"` | **One-shot, script-grade.** Only the final response text on stdout; nothing on stderr. Reads redirected stdin (`hermes -z "summarize this" < /file.txt`). |
| `hermes acp` | **Long-lived** JSON-RPC stdio server. Stdout protocol-only, logs to stderr. |
| TUI gateway | **Long-lived** JSON-RPC over stdio or WebSocket. |
| `hermes serve` / `hermes gateway` | **Long-lived HTTP daemon.** Headless, no browser UI; `--host`, `--port`, `--insecure`, `--skip-build`, `--stop`, `--status`. Docs specifically recommend it "for headless deployment on remote hosts". |

`[docs]` cli-commands.md.

Relevant per-run limits: `--max-turns <N>` — "Maximum tool-calling iterations per turn (default: 500)".
`--source <tag>` tags the session for later filtering (default `cli`). `[docs]`

`hermes serve` requires the `[web]` extra, and its embedded chat socket needs `[pty]` on POSIX.
`hermes acp` requires the `[acp]` extra. Provisioning must install these. `[docs]`

**Exit codes are not documented.** The `--usage-file` JSON does carry `completed` / `failed` flags,
which is the documented way to tell success from failure for `hermes -z`. `[docs]` cli-commands.md.

---

## 6. Session resumption

### Pi — native, tree-structured, on disk as JSONL

Flags:

```
-c, --continue          # Continue most recent session
-r, --resume            # Interactive session picker at startup
--session <path|id>     # Use a specific session file or partial session ID
--fork <path|id>        # Fork a session file or partial ID into a new session
--session-dir <dir>     # Custom storage directory
--no-session            # Ephemeral mode; do not save
-n, --name <name>       # Set display name
```

`[docs]` README, sessions.md.

Storage path:

```
~/.pi/agent/sessions/--<path>--/<timestamp>_<uuid>.jsonl
```

where `<path>` is the working directory with `/` replaced by `-`. `[docs]` session-format.md.

Format — JSONL, first line is the header, subsequent lines form a **tree** via `id` / `parentId`:

```json
{"type":"session","version":3,"id":"uuid","timestamp":"2024-12-03T14:00:00.000Z","cwd":"/path/to/project"}
{"type":"message","id":"a1b2c3d4","parentId":null,"timestamp":"2024-12-03T14:00:01.000Z","message":{"role":"user","content":"Hello"}}
{"type":"message","id":"b2c3d4e5","parentId":"a1b2c3d4","timestamp":"2024-12-03T14:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Hi!"}],"provider":"anthropic","model":"claude-sonnet-4-5","stopReason":"stop"}}
```

Compaction entry:

```json
{"type":"compaction","id":"f6g7h8i9","parentId":"e5f6g7h8","timestamp":"2024-12-03T14:10:00.000Z","summary":"User discussed X, Y, Z...","firstKeptEntryId":"c3d4e5f6","tokensBefore":50000}
```

`id` is an 8-character hex string; `parentId` is `null` for the first entry. Entry types include
`message`, `compaction`, branch summaries, custom entries (extension data that stays out of LLM
context), custom message entries (extension data that enters the conversation), and labels
(bookmarks). Persisted content covers "message entries, model changes, thinking-level changes, labels,
compactions, branch summaries, and extension entries". The tree enables in-place branching without new
files. `[docs]` session-format.md, sessions.md.

RPC-side session commands: `switch_session`, `fork`, `clone`, `get_fork_messages`, `get_entries` (an
append-only history read with **cursor support** — ideal for a daemon rebuilding state after a
reconnect), `get_tree`, `get_last_assistant_text`, `set_session_name`, `export_html`. `[docs]` rpc.md.

Sessions are also exportable to HTML via `--export <in> [out]` or the `/export` path. `[docs]`

### Hermes — native, SQLite-backed

Flags: `--continue` / `-c` (most recent), `-c "my project"` (by name), `--resume <id>` / `-r <id>`, plus
`--resume "session_title"`. `[docs]` cli.md, sessions.md.

Storage: **`~/.hermes/state.db`**, SQLite with FTS5 full-text search. Persisted:

- session id, originating platform, user id
- human-readable title (unique constraint)
- model configuration and a **system-prompt snapshot**
- "Full message history (role, content, tool calls, tool results)"
- input/output token counts
- created / terminated timestamps
- parent session reference for lineage tracking after compression

`[docs]` sessions.md, cli.md.

Session id format: `YYYYMMDD_HHMMSS_<hex>` — 6 hex chars for CLI/TUI (`20250305_091523_a1b2c3`),
8 for gateway sessions (`20250305_091523_a1b2c3d4`). `[docs]` sessions.md.

On resume Hermes shows a compact recap panel and restores the working directory unless
`--no-restore-cwd`. `[docs]`

Management: `hermes sessions` (browse, export, prune, optimize). ACP has its own session verbs
(new/load/resume/fork/list/cancel); forking deep-copies message history into a new session with a
distinct id and working directory. `[source]` acp-internals.md.

**Separate concept — trajectories.** Hermes also writes ShareGPT-style JSONL to the *current working
directory*: `trajectory_samples.jsonl` (successful) and `failed_trajectories.jsonl` (incomplete/failed).
Roles map system→`system`, user→`human`, assistant→`gpt`, tool→`tool`; each turn has a `value` string.
Assistant turns always include a `<think>\n{reasoning}\n</think>\n` block (empty if none, and native
thinking tokens are normalised into that wrapper); tool calls appear as JSON inside `<tool_call>` tags
and results inside `<tool_response>` tags. The batch-runner variant adds `prompt_index`, `metadata`,
`partial`, `api_calls`, `toolsets_used`, `tool_stats`, `tool_error_counts`. `[docs]`
<https://hermes-agent.nousresearch.com/docs/developer-guide/trajectory-format>

This is a **training-data export format, not an event stream** — it is written at the end, it is
lossy (everything is flattened to strings), and it lands in `cwd`. Do not build the event model on it.
It is, however, useful as a post-hoc cross-check, and it is a stray-file hazard for a daemon that runs
the harness in a user's repo. `[inference]`

---

## 7. What must be confirmed empirically

Ordered by how much of the design hangs on it.

1. **Hermes: the exact JSON payload of `tool.start` / `tool.progress` / `tool.complete` /
   `approval.request` / `message.delta`.** Documented by name only. `gateway/platforms/api_server.py`
   truncates before the handler bodies over plain HTTP. Capture by running `hermes serve` and reading
   `GET /v1/runs/{id}/events`, and/or by driving the TUI gateway over stdio. **Nothing about the Hermes
   half of the event model is safe to freeze until this is done.**
2. **Pi: whether `ctx.ui.confirm()` inside a `tool_call` extension hook is actually routed to the RPC
   client as an extension-UI dialog request.** The whole Pi approval story is this composition. If it
   does not hold, fall back to the SDK. Also confirm it is a hard no in `--mode json`.
3. **Hermes: whether a `pre_tool_call` shell hook that times out or crashes fails open.** The docs say
   hook failures "never crash the agent", which reads as fail-open. An approval policy needs fail-closed;
   find out whether `approvals.timeout` / `cron_mode: deny` can be composed to get it.
4. **Exit codes for both.** Undocumented in both projects. Determine at minimum: clean completion,
   user abort / interrupt, model or provider error, and (Pi) blocked-tool termination.
5. **Pi: the exhaustive `AssistantMessageEvent` sub-type list and field shapes** (`text_delta`,
   `thinking_delta`, tool-call deltas, `contentIndex` semantics). Read from
   `packages/coding-agent/src` at a pinned version — do not rely on the docs' prose.
6. **Whether `--mode json` emits `agent_settled`.** It is listed for RPC only.
7. **Hermes config key drift: `api` vs `base_url`** in the `providers:` block. The doc flags a rename.
8. **Hermes TUI gateway single-threaded dispatcher** (issue #12546) — whether the installed version
   still blocks `approval.respond` behind a slow handler.
9. **Hermes first-run consent for shell hooks** — confirm `HERMES_ACCEPT_HOOKS=1` fully suppresses the
   prompt in a headless context, or the daemon deadlocks on the first tool call.
10. **Hermes trajectory files landing in `cwd`** — confirm and find the opt-out, or the harness litters
    the user's repo.
11. **Pi extension loading in RPC mode** — that `-e <source>` works with `--mode rpc` and that an
    extension can be supplied as a generated file path per session.

[Stand up a Host](../../.scratch/remote-harness-orchestrator/issues/03-stand-up-a-host.md) is the vehicle
for all of the above.

---

## 8. Summary table

| | **Hermes** | **Pi** |
| --- | --- | --- |
| Repo | `NousResearch/hermes-agent` (Python) | `earendil-works/pi` (TypeScript) |
| Structured stdout from the plain CLI | **No** | **Yes** — `--mode json`, `--mode rpc` |
| Structured surface that does exist | ACP stdio, TUI gateway JSON-RPC (stdio/WS), HTTP+SSE API server | (same process, no extra server needed) |
| Best surface for a Go daemon | HTTP API server (`/v1/runs` + SSE) or TUI gateway JSON-RPC | `--mode rpc` |
| Tool call visible before execution | Yes, on protocol surfaces (`tool.start`) and via hooks | Yes (`tool_execution_start` on the wire) |
| Approval interception | **Yes, three ways**: `pre_tool_call` shell hook (JSON stdin → `{"action":"block"}` stdout), protocol `approval.request`/`approval.respond`, ACP `session/request_permission` | **Yes, but only via a custom extension's blockable `tool_call` hook**; no built-in approval, no CLI flag |
| Gated by default? | Only a dangerous-pattern list; `approvals.mode: smart` auto-approves low risk | Nothing is gated |
| OpenAI-compatible endpoint | `OPENAI_BASE_URL` + `OPENAI_API_KEY`, or `providers:` block in `config.yaml` | Extension calling `pi.registerProvider({ baseUrl, api: "openai-completions" })` |
| Per-invocation model override | `-m/--model`, `--provider`, `HERMES_INFERENCE_MODEL` | `--provider`, `--model`, `--api-key`, `--thinking` |
| Long-lived process | ACP / gateway / serve; `-z` and `-q` are one-shot | `--mode rpc`; `-p` and `--mode json` are one-shot |
| Session store | SQLite `~/.hermes/state.db` | JSONL tree `~/.pi/agent/sessions/--<path>--/*.jsonl` |
| Session id | `YYYYMMDD_HHMMSS_<hex>` | UUID (header) + 8-hex entry ids |
| Resume | `-c`, `-r <id>`, `-c "<title>"` | `-c`, `-r`, `--session`, `--fork` |
| Config/state dir override | `HERMES_HOME` | `PI_CODING_AGENT_DIR`, `--session-dir` |
| Exit codes | undocumented | undocumented |
| Licence | see repo | MIT |

---

## Sources

**Hermes**

- <https://github.com/NousResearch/hermes-agent>
- <https://hermes-agent.nousresearch.com/docs/reference/cli-commands>
- <https://hermes-agent.nousresearch.com/docs/reference/environment-variables>
- <https://hermes-agent.nousresearch.com/docs/user-guide/cli>
- <https://hermes-agent.nousresearch.com/docs/user-guide/security>
- <https://hermes-agent.nousresearch.com/docs/user-guide/sessions>
- <https://hermes-agent.nousresearch.com/docs/user-guide/configuring-models>
- <https://hermes-agent.nousresearch.com/docs/user-guide/features/hooks>
- <https://hermes-agent.nousresearch.com/docs/user-guide/features/api-server>
- <https://hermes-agent.nousresearch.com/docs/user-guide/features/acp>
- <https://hermes-agent.nousresearch.com/docs/developer-guide/programmatic-integration>
- <https://hermes-agent.nousresearch.com/docs/developer-guide/acp-internals>
- <https://hermes-agent.nousresearch.com/docs/developer-guide/trajectory-format>
- <https://github.com/NousResearch/hermes-agent/tree/main/tui_gateway>
- <https://github.com/NousResearch/hermes-agent/blob/main/gateway/platforms/api_server.py>
- <https://github.com/NousResearch/hermes-agent/issues/12546>
- <https://agentclientprotocol.com> (the ACP spec Hermes implements)

**Pi**

- <https://github.com/earendil-works/pi>
- <https://github.com/earendil-works/pi/blob/main/packages/coding-agent/README.md>
- <https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/index.md>
- <https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/usage.md>
- <https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/json.md>
- <https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/rpc.md>
- <https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/sdk.md>
- <https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/extensions.md>
- <https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/security.md>
- <https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/sessions.md>
- <https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/session-format.md>
- <https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/custom-provider.md>
