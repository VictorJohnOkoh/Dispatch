# Harness Control Surfaces: Hermes and Pi

Research for [issue 01 — Hermes and Pi control surfaces](../../.scratch/remote-harness-orchestrator/issues/01-hermes-and-pi-control-surfaces.md).

Date: 2026-08-07. All claims below are tagged `[source]`, `[docs]`, or `[inference]`.

- `[source]` — read from the project's own source code in its repository.
- `[docs]` — read from first-party documentation in the project's repository or its official docs site.
- `[inference]` — my reasoning from the above, not stated by the project.

---

## CORRECTION — 2026-08-14, from a live install

Everything below the next horizontal rule was researched from documentation and published source on
2026-08-07. Running it against a real **Hermes Agent v0.19.0 (2026.7.20)** on Windows contradicts part
of it. This section supersedes the original where they disagree; the original is kept because it
remains an accurate record of what the documentation *says*, and the gap between the two is itself the
finding.

Extended **2026-08-15** with C8 and C9, and with revisions to C6 and C7, after a second round of
captures aimed at the edit path rather than the terminal tool.

New evidence tags:

- `[capture]` — observed on a live install. Artefacts in
  [`captures/hermes/`](./captures/hermes/) (terminal path, 2026-08-14) and
  [`captures/hermes-edit/`](./captures/hermes-edit/) (edit path, 2026-08-15), produced by
  `scripts/capture-hermes.sh` + `scripts/acp-capture.py`.
- `[src]` — read from the installed Hermes source at
  `%LOCALAPPDATA%\hermes\hermes-agent`. This install is editable, so the shipping implementation is
  readable in full — which is how C6 and C9 were settled. Line numbers are v0.19.0-specific.

### C1. The `/v1/*` agent-run API does not exist

§1(c) lists `POST /v1/runs`, `GET /v1/runs/{id}/events`, `GET /v1/capabilities` and friends. **None of
them are present in v0.19.0.** `GET /openapi.json` on a live `hermes serve` enumerates ~280 routes, all
mounted under `/api/*`. `POST /v1/runs`, `GET /v1/capabilities`, `GET /v1/models` and even `GET /health`
all return **404**; the real liveness endpoint is `GET /api/health`. `[capture]`

The published docs describe a surface the shipped code does not have. Since §1(c) was `[docs]`-tagged
throughout, this is not a research error so much as a demonstration that this vendor's docs cannot be
relied on for API shape — which raises the bar for every other `[docs]`-only claim in this file.

The only WebSocket event channel, `@app.websocket("/api/events")`, is **dashboard plumbing, not a
control API**: it is gated behind `_DASHBOARD_EMBEDDED_CHAT_ENABLED`, its subscribers are receive-only
("Subscribers don't speak"), and it is fed by the PTY sidecar via `/api/pub` to give the React sidebar
its tool-call feed. There is no way to start a run with it. `[source]` `hermes_cli/web_server.py`

### C2. The recommended surface flips from HTTP+SSE to ACP

§8's "Best surface for a Go daemon: HTTP API server (`/v1/runs` + SSE)" is void — that surface does not
exist. **`hermes acp` is the answer**, and it is a better one than the original recommendation would
have been even if `/v1/runs` had been real:

- It is an **open standard** — Agent Client Protocol, JSON-RPC 2.0 over newline-delimited JSON on
  stdio (no `Content-Length` framing), verified against `agent_client_protocol` 0.9.0
  (`acp/connection.py` uses `readline()` + `json.loads`). `PROTOCOL_VERSION = 1`. `[source]`
- Schemas are published and typed, so the adapter is written against a spec rather than reverse-engineered.
- It needs no server, no port, no bearer token, and leaves nothing listening.

**This overturns the headline conclusion recorded in the map.** Hermes and Pi do *not* have
incompatible integration shapes. Both are subprocess-with-structured-stdio:

| | Hermes | Pi |
| --- | --- | --- |
| Invocation | `hermes acp` | `pi --mode rpc` |
| Transport | JSON-RPC 2.0 over ndjson on stdio | LF-delimited JSONL on stdio |
| Schema | ACP standard, published | Pi-specific |
| Approval | in-protocol (`session/request_permission`) | none |

The Harness adapter no longer has to span "HTTP server" and "subprocess" as two different worlds.

### C3. Configuration: `base_url` in `config.yaml`, not `OPENAI_BASE_URL` in `~/.hermes/.env`

§8 says "`OPENAI_BASE_URL` + `OPENAI_API_KEY`, or `providers:` block". On a live install the wiring is:

```yaml
model:
  default: gpt-oss-20b
  provider: lmstudio
  base_url: http://127.0.0.1:1234/v1
  api_mode: chat_completions
```

`GET /api/status` reports `hermes_home` as `%LOCALAPPDATA%\hermes`, with `config_path` and `env_path`
beneath it. **`~/.hermes/.env` is not read at all** — writing `OPENAI_BASE_URL` there had no effect
whatsoever, and a run configured with a deliberately invalid value still succeeded. `[capture]`

This settles open item 7 (the `base_url` vs `OPENAI_BASE_URL` naming question): the config key is
`base_url`, nested under `model:`.

### C4. Observed ACP event vocabulary, including a non-standard kind

`session/update` notifications actually seen, across a plain and a tool-calling turn: `[capture]`

| kind | plain | tool | in ACP 0.9.0 schema? |
| --- | --- | --- | --- |
| `agent_thought_chunk` | 19 | 49 | yes |
| `agent_message_chunk` | 1 | 0 | yes |
| `available_commands_update` | 1 | 1 | yes |
| `tool_call` | 0 | 1 | yes |
| **`usage_update`** | **2** | **1** | **NO** |

`usage_update` is not in the schema's `sessionUpdate` literal set (`agent_message_chunk`,
`agent_thought_chunk`, `available_commands_update`, `current_mode_update`, `plan`, `tool_call`,
`tool_call_update`, `user_message_chunk`). **A harness extends the standard protocol it speaks**, so
the Event model must tolerate unknown kinds rather than reject them.

The plain turn closed with `stopReason: "end_turn"` and a `usage` object:
`{cachedReadTokens, inputTokens: 14253, outputTokens: 30, thoughtTokens: 19, totalTokens}`.

`tool_call` payload shape: `{toolCallId: "tc-2347e4d298a7", kind: "execute", title: "terminal: ls -1 |
wc -l", locations: [], content: [{type: "content", content: {type: "text", text: "$ ls -1 | wc -l"}}]}`.

**Not captured: `tool_call_update`.** See C7 — the run could not be carried past the tool call. Whether
Hermes emits it at all is still open, and it is the remaining blocker on the Event model.

### C5. Session lifecycle, approval modes and the model catalog are all in-protocol

`initialize` returns `agentCapabilities` advertising `loadSession: true` and
`sessionCapabilities: {fork, list, resume}` — Hermes implements `session/fork`, `session/list`,
`session/resume`, `session/load`. `[capture]`

`session/new` returns, unprompted:

- **`modes`** — `availableModes: [default "Ask before edits", accept_edits "Auto-allow workspace and
  /tmp edits; still asks for sensitive paths", dont_ask "Auto-allow file edits for this session except
  sensitive paths"]` with `currentModeId: "default"`, switchable via `session/set_mode`.
- **`models`** — `availableModels`, **43 entries** across three id prefixes (`lmstudio:`, `nous:`,
  `moa:`), switchable via `session/set_model`.

This matters well beyond ticket 01. Approval Policy, Session lifecycle and model selection are not
things the orchestrator must invent on top of an opaque harness — they are already first-class verbs in
the protocol, and the design question becomes how much of that to expose rather than how to build it.
The three-mode ladder is also near-identical to T3 Code's permission modes; see
[t3-code-prior-art.md](./t3-code-prior-art.md).

### C6. Hermes delegated nothing to the client

Across both captured turns the only method Hermes ever sent was `session/update` — **zero**
`session/request_permission`, `fs/read_text_file`, `fs/write_text_file` or `terminal/*` calls, despite
the client advertising `fs` capability and the session running in `default` ("Ask before edits") mode.
`[capture]`

Hermes performs its own file and terminal work rather than delegating it over ACP. Two consequences:

1. §5's approval story is **not disproven but not observed either.** The mechanism exists in
   `acp_adapter/permissions.py`; it simply did not fire for a `ls`-class command, consistent with
   `approvals.mode: smart` gating only dangerous patterns. Absence here is weak evidence, not proof.
2. The client never sees the tool's actual effects — only that a tool ran. An orchestrator wanting to
   supervise file writes cannot do it through ACP alone with this harness.

**Confirmed from source, 2026-08-15** — this is by construction, not a property of the prompts used:
`[src]`

- `initialize` (`acp_adapter/server.py:1042`) accepts `client_capabilities` and **never reads or stores
  it**. Advertising `terminal: true` therefore cannot change Hermes' behaviour, and the route this
  section previously called "most promising" does not exist. It was not implemented.
- Grepping the whole non-`venv` tree, Hermes-as-agent invokes exactly **one** client method:
  `request_permission` (`server.py:1704`, `server.py:1709`). There is no `terminal/create`,
  `terminal/output`, `fs/read_text_file` or `fs/write_text_file` call anywhere.
  (`agent/copilot_acp_client.py` does handle `fs/*` — but that is Hermes acting as an ACP *client* to
  Copilot, the opposite direction, and it is not this surface.)

So on point 1, the approval mechanism **was** subsequently observed firing — see C8 — and it is the
sole client callback this harness has. Point 2 stands and is now permanent: with `fs` delegation absent
from the implementation, an orchestrator cannot observe file effects through ACP at all.


### C7. Hermes deadlocks on the first tool call of any kind in ACP mode on Windows, until the client closes stdin

> **Revised 2026-08-15.** First written as a *terminal tool* defect. It is not: the same hang occurs on
> `search_files` and on `patch`, neither of which runs a shell command. Scope corrected below.

The first tool call of a session appears to hang. It is not a slow sandbox — the tool's duration tracks
the client's timeout almost exactly: `[capture]`

| client timeout | tool | duration |
| --- | --- | --- |
| 120s | `terminal` | 118.83s |
| 240s | `search_files` | 237.73s |
| 280s | `terminal` | 272.72s |
| 300s | `search_files` | 294.21s |
| 420s | `terminal` | 418.80s |
| 420s | `patch` | 414.35s |
| 900s | `terminal` | 898.64s |
| 900s | `patch` | 898.43s |

Eight runs, three different tools, always within ~6s of the client's own timeout. With stdin closed
before the tool ran, the identical setup took **1.18s**.

**The duration is a function of the client's timeout, so no timeout value can outlast it.** Raising the
budget raises the hang by the same amount. The 900s `patch` run finished at 898.43s and the agent's next
API call landed 2s *after* the client had already given up — the near-miss is structural, not bad luck.

The mechanism is visible in the source. `tools/file_tools.py:1042` logs
`Creating new local environment for task ...` and calls `_create_environment(env_type="local")` — the
same machinery `tools/terminal_tool.py:2279` uses. **Every** file, search and terminal tool shares one
per-process execution environment, created lazily on the first tool call. Its child process inherits the
ACP stdin pipe and blocks reading it, so the client's teardown is what releases it.

Two consequences the original wording got wrong:

- **Avoiding the terminal tool is not a workaround.** A read-only prompt that touches any file tool pays
  the same cost.
- **The environment is per-process, not per-workdir.** A subsequent session in a fresh `hermes acp`
  process pays it again. Within one process it is one-time: after the hang cleared, `read_file` took
  **0.79s** and **1.05s** on two later runs.

Workarounds in `acp-capture.py`, neither complete: `--close-stdin-on-tool-call` closes stdin at the
first `tool_call` (~17s instead of ~15 minutes); `--close-stdin-on-permission` closes it one step later,
after answering an approval, for the edit path where approval precedes the spawn. Both cost the tail of
the conversation — Hermes stops emitting once stdin closes, which is why `tool_call_update` and the
`session/prompt` response are still missing.

`tool_call_update` is emitted from `make_step_cb` (`acp_adapter/events.py:209`) via `build_tool_complete`
(`acp_adapter/tools.py:1305`), which fires on the *following* agent step. So the deadlock starves it by
construction: the update cannot arrive until after the tool returns, and the tool does not return until
the client gives up.

**This makes `tool_call_update` unreachable on Windows in ACP mode** — though not on Linux, where the
deadlock does not occur at all (see the platform table above). The two escapes on Windows are mutually
exclusive: `[capture]`

| | keep stdin open | close stdin after approval |
| --- | --- | --- |
| `patch` duration | 898.43s | **3.08s** |
| turn completes internally | no — client gives up first | yes, `reason=text_response(finish_reason=stop)` |
| frames after the approval response | n/a | **none** |

The decisive run is the second column: same tool, prompt and machine, and closing stdin cut `patch`
from 898.43s to **3.08s** — which is the proof that the inherited stdin pipe *is* the deadlock, not
merely correlated with it. But Hermes' ACP connection tears down its writer along with its reader, so
although the agent's own log records the turn ending normally, the last frame the client ever received
was `session/request_permission`. Hold stdin open and the tool never returns; close it and the tool
returns but nobody is listening.

On Windows, short of a Hermes fix, the payload has to be read off `build_tool_complete` rather than
captured. **On Linux it was captured directly** — see C12.

**Windows only — confirmed 2026-08-16.** The same Hermes v0.19.0 (same build, `upstream 71e7eb3c`,
same `agent_client_protocol` 0.9.0) installed under WSL2 Ubuntu ran the identical terminal prompt with
the identical 120s client timeout and finished in **1.73s**. `[capture]`

| | Windows | Linux (WSL2) |
| --- | --- | --- |
| `terminal` tool, 120s timeout | 118.83s | **1.73s** |
| `session/prompt` returns | never | `stopReason: end_turn` |
| `tool_call_update` | never seen | **captured** |

So the deadlock is a Windows pipe-inheritance defect, not a Hermes design property, and everything C7
declared unreachable is reachable on Linux.

### C8. The edit-approval payload, captured

`session/request_permission` fires for file edits, via `make_acp_edit_approval_requester`
(`acp_adapter/server.py:1707`). The full frame: `[capture]`

```json
{"jsonrpc": "2.0", "id": 0, "method": "session/request_permission", "params": {
  "sessionId": "b0060094-46f2-4f44-acf8-7620cc0a048d",
  "options": [
    {"kind": "allow_once",  "name": "Allow edit", "optionId": "allow_once"},
    {"kind": "reject_once", "name": "Deny",       "optionId": "deny"}
  ],
  "toolCall": {
    "toolCallId": "edit-approval-1",
    "title": "Approve edit: notes.txt",
    "kind": "edit",
    "status": "pending",
    "content": [{"type": "diff", "path": "notes.txt",
                 "oldText": "alpha\nbeta\ngamma\n", "newText": "alpha\nBETA\ngamma\n"}],
    "rawInput": {"tool": "patch", "arguments": {"mode": "replace", "path": "notes.txt",
                                                "old_string": "beta", "new_string": "BETA"}}
  }
}}
```

Four things the Event model has to account for:

1. **The full diff is in the request.** `oldText`/`newText` arrive before the write, so an approval UI
   can show exactly what will change without reading the file. This is the richest control-plane payload
   found on either harness.
2. **`rawInput` leaks the harness-native tool name and arguments** (`patch`, `old_string`/`new_string`)
   straight through the standard envelope. Useful for debugging, but it is a normalisation boundary
   leak: anything keying off it is coupled to Hermes.
3. **Only two options, hardcoded** at `edit_approval.py:308` — `allow_once` and `deny`. No
   `allow_always`, despite C5's session modes offering exactly that persistently. The per-call gate and
   the session mode are separate systems.
4. **The `toolCallId` does not match the streamed `tool_call`.** The approval carries
   `edit-approval-1`; the `session/update` for the same edit carried `tc-7bd48457f3f0`. An orchestrator
   correlating approvals to tool calls by id will silently fail to match, and must correlate on
   `path` + ordering instead.
### C9. Hermes can report an edit as "denied by ACP client" without ever asking the client

Reproduced three times: `patch` and `write_file` returned
`{"error": "Edit approval denied by ACP client; file was not modified."}` in **0.00s**, with **zero**
`session/request_permission` frames on the wire and no corresponding warning in the agent's stderr.
`[capture]`

The intent is documented at `acp_adapter/edit_approval.py:237` — *"Requester exceptions deny by
default"* — and the code returns `False` when the request cannot be scheduled onto the event loop. So
Hermes **fails closed**, which is the safe direction, and answers old item 5 of §7 for the ACP path.

The failure is ordering-dependent: `[capture]`

| `patch` position in the turn | runs | `session/request_permission` sent? |
| --- | --- | --- |
| first tool of the session | 2 | yes, both — edit applied |
| after another tool ran | 3 | no, all three — phantom denial |

This correlates with the thread-local approval callback that `acp_adapter/server.py:1743` explicitly
warns about ("Approval callback is per-thread"): later tools land on a different executor thread. Not
proven — the exact mechanism was not chased further, as it is Hermes' bug rather than a fact the
orchestrator spec needs.

**Windows only — confirmed 2026-08-16, and this contradicts the earlier prediction.** This document
previously reasoned that C9 "is a threading story and probably is not" Windows-specific. That was
wrong. On WSL2 Ubuntu the phantom denial did not occur once in three runs: `[capture]`

| condition | Windows | Linux (WSL2) |
| --- | --- | --- |
| `patch` after another tool | 3/3 phantom denials | **0/3** — real `session/request_permission` every time |

One Linux run made **12 tool calls and 4 approval requests**, all of them real and all honoured. The
edits applied. Whatever the mechanism is, it does not survive the platform change — so C9 is a
Windows-specific defect and not a property of the ACP adapter's threading model.

**What the orchestrator must take from this: a denial is not evidence of a decision.** Approval Policy
cannot render "denied" as a user choice, because this harness emits the same result for an internal
scheduling failure. The Event model needs to distinguish *refused by a human* from *refused by
default*, and on this surface only the presence of a preceding `session/request_permission` frame
separates them.


### C10. Exit codes, and other small settled items

- `hermes -z <prompt>` exits **0** on success. `[capture]` (previously listed as unknown)
- `hermes acp --check` exits **0** when the adapter and its dependencies import cleanly.
- `hermes acp --version` prints `0.19.0`.
- ACP mode requires **no** API key; `GET /api/status` reported `auth_required: false`.
- `initialize` advertises `authMethods`, including one with `type: "terminal"` instructing the client
  to run `hermes --setup` — an agent asking the client to run an interactive program on its behalf.
- No stray `trajectory_samples.jsonl` / `failed_trajectories.jsonl` appeared in the working directory.
### C11. `pre_tool_call` fails **open** — the opposite of the ACP edit path

Settled from source rather than by test. `agent/shell_hooks.py` returns `None` — meaning "no block" —
on **every** failure path in `_spawn` and `_make_callback`: `[src]`

| Failure of the hook script | Result |
| --- | --- |
| Raises / crashes | warning logged, `return None` → **tool runs** |
| Not found | `"command not found"`, `return None` → **tool runs** |
| Not executable | `"command not executable"`, `return None` → **tool runs** |
| Times out (`spec.timeout`) | warning logged, `return None` → **tool runs** |
| Exits non-zero | warning logged, **stdout still parsed**; no directive → **tool runs** |

The last row is deliberate, per the comment at the call site: scripts that "signal failure via exit code
can also return a block directive", so a non-zero exit is not itself a refusal. A hook blocks **only**
by printing `{"decision":"block"}` or `{"action":"block"}` on stdout.

**So the same harness fails in both directions**: the ACP edit path denies when its requester breaks
(C9), while the shell hook permits when its script breaks. Two mechanisms, one product, opposite safety
defaults. Any Approval Policy built on Hermes must state which mechanism it relies on — a guarantee
proven for one does not transfer to the other.

Consent note: shell hooks need first-use approval, recorded in
`~/.hermes/shell-hooks-allowlist.json`. A non-TTY caller — which any orchestrator is — must pass
`--accept-hooks`, `HERMES_ACCEPT_HOOKS=1`, or `hooks_auto_accept: true`, or registration fails and the
hook silently never runs. That is a third way to end up unguarded.

### C12. `tool_call_update`, captured at last — and Hermes only sends it for `execute` tools

With C7 out of the way on Linux, the completion event arrives: `[capture]`

```json
{"sessionUpdate": "tool_call_update",
 "toolCallId": "tc-6387a9b13966",
 "kind": "execute",
 "status": "completed",
 "content": [{"type": "content",
              "content": {"type": "text",
                          "text": "terminal result\n- **output:** 2\n- **exit_code:** 0"}}]}
```

Two things settle immediately:

- **The `toolCallId` matches its `tool_call`** (`tc-6387a9b13966` in both). So C8's id mismatch is
  specific to the *approval* request, not to the tool lifecycle. Tool call → completion correlates by
  id; approval → tool call does not.
- **The result is prose, not structure.** The exit code is embedded in a markdown string —
  `"- **exit_code:** 0"` — rather than carried as a field. Compare Pi's `isError` boolean beside
  structured content (P4). Any normaliser that wants Hermes' exit codes must parse formatted display
  text, which is fragile by construction.

**The new finding is what does *not* arrive.** Across four Linux runs, `tool_call_update` is emitted
**only** for `kind: "execute"`. Not once for `read` or `edit`: `[capture]`

| run | `tool_call` | `tool_call_update` | never completed |
| --- | --- | --- | --- |
| terminal prompt | 1 | 1 | 0 |
| edit prompt | 2 | 0 | 2 |
| edit prompt (repeat) | 2 | 0 | 2 |
| edit prompt (12-tool run) | 12 | 3 | 9 |

In the 12-tool run the split is exact: the three completions were `python:` and two `terminal:` calls —
every `execute`. The nine without were `read: notes.txt`, `patch (replace): notes.txt` and
`write: notes.txt` — every `read` and `edit`.

**So the Hermes tool lifecycle is incomplete on every platform, and Linux only narrows the gap.** A
Client that renders a `tool_call` as "running" and waits for its completion will hang forever on every
file operation. The Event model must either synthesise a terminal state for `read`/`edit` calls — the
next `tool_call` or the `session/prompt` response are the only available signals — or mark them as
fire-and-forget by kind. This is not a Windows artefact and will not be fixed by changing host.

### Still open after this correction

1. ~~**Does Hermes emit `tool_call_update`?**~~ **Closed 2026-08-16 — captured on Linux.** Not
   capturable on Windows (the two escapes past C7 exclude each other), but C7 does not reproduce under
   WSL2, and the payload arrives normally there. See C12 — including the discovery that it is sent
   **only for `execute` tools**, never for `read` or `edit`, on any platform.
2. ~~**Does `pre_tool_call` fail open?**~~ **Closed 2026-08-16 — yes, it does.** See C11.
3. ~~**Does Pi's `ctx.ui.confirm` route over `--mode rpc`?**~~ **Closed 2026-08-16 — yes.** Extension
   dialogs cross RPC as an `extension_ui_request` / `extension_ui_response` pair. See P7.
4. ~~Whether C7 and C9 reproduce on Linux.~~ **Closed 2026-08-16 — neither does.** Both are
   Windows-specific. The prediction recorded here (that C9 "probably is not" Windows-only) was wrong.

**Nothing on the original list remains open.** What replaced it is C12's finding: `read` and `edit`
tool calls never reach a terminal state in the ACP stream on any platform. That is a live constraint on
the Event model rather than an open question.

---

## CORRECTION — 2026-08-15, Pi from a live install

Pi **0.9.x** on Windows, driving LM Studio (`qwen/qwen3.5-9b`). Numbered **P1–P6** to keep them
distinct from the Hermes corrections above. Artefacts in [`captures/pi/`](./captures/pi/).

The headline: **§1's description of Pi's event stream was right, and understated it.** Where the Hermes
corrections are mostly retractions, these are mostly confirmations plus detail that changes design
decisions.

### P1. An arbitrary OpenAI-compatible endpoint needs no extension

§4 says an endpoint outside Pi's built-in provider list is reachable "only by registering a provider
from an extension". **Not so.** `~/.pi/agent/models.json` is a first-class config route: `[capture]`

```json
{"providers": {"lmstudio": {
  "baseUrl": "http://127.0.0.1:1234/v1",
  "api": "openai-completions",
  "apiKey": "lm-studio",
  "models": [{"id": "qwen/qwen3.5-9b"}, {"id": "gpt-oss-20b"}]
}}}
```

`pi --list-models` then reports the models with **context window and max-output autodetected**
(128K/16.4K), which the extension route makes you hardcode. This materially simplifies the Vendor story:
pointing Pi at a Daemon-managed local endpoint is a config write, not code generation.

Two traps, both of which cost a wizard run:

- The key is **`baseUrl`**, camelCase. `baseURL` is rejected with
  `Provider "lmstudio": "baseUrl" is required when defining custom models`.
- An invalid `models.json` **also breaks `-e` extension loading** when both define the same provider
  name, and the error is attributed to the extension. Once the config is valid the two coexist fine.

### P2. `-p` is not one-shot — it reads stdin to EOF

§5 lists `-p` as a one-shot invocation. It is, but it **also concatenates stdin**, and it blocks until
EOF even when the prompt is supplied as an argument. `[capture]`

| stdin | result |
| --- | --- |
| closed (`< /dev/null`) | `hello`, exit 0, ~5s |
| open and silent (a terminal) | **hangs indefinitely**, no output on either stream |

This is a direct constraint on process supervision, and the second harness in a row where stdin
handling is the trap — compare C7, where Hermes hangs because a *child* inherited the stdin pipe.
**A supervisor must own stdin explicitly for both harnesses**, closing or feeding it deliberately
rather than inheriting whatever it was started with.

### P3. `--mode rpc` is a session, not a pipe

`cat commands.jsonl | pi --mode rpc` truncates the run: EOF on stdin makes Pi exit mid-turn. Measured
**5 events** that way, stopping at `message_start`, versus **54 events** through `agent_settled` with
stdin held open. `[capture]`

The RPC `prompt` command's field is **`message`**, not `prompt`. Sending `prompt` yields
`{"success": false, "error": "Cannot read properties of undefined (reading 'startsWith')"}` — an
undefined-property crash rather than a schema error, so the response does not tell you the right shape.
The installed `docs/rpc.md` does, in full; §1's "only partly documented" was based on the published
docs, and the npm package ships more.

An optional `id` on the command is **echoed back on the response** —
`{"id":"req-1","type":"response","command":"prompt","success":true}` — which is how a supervisor
correlates a reply to the command that caused it.

Corrected runs settle the two questions this stage exists to answer, and one more: `[capture]`

- **`-e` extension loading works in RPC mode.** The provider registered and the turn completed.
- **`agent_settled` appears in RPC**, as in `--mode json`. It is not a JSON-mode artefact.
- **The tool lifecycle is identical over RPC** — two `bash` calls, each with full
  `tool_execution_start` / `_update` / `_end`.

So **`--mode rpc` is a superset of `--mode json`**, not a different vocabulary: the same event stream
plus an inbound command channel. The Daemon can build against RPC without losing anything, and an
adapter written for one mode's events works for the other.

### P4. The tool lifecycle is complete, and correlates by a stable id

The contrast with Hermes is the sharpest finding in this document. `[capture]`

```json
{"type":"tool_execution_start","toolCallId":"654406736","toolName":"bash","args":{"command":"ls -la"}}
{"type":"tool_execution_update","toolCallId":"654406736","toolName":"bash","args":{...},
 "partialResult":{"content":[{"type":"text","text":"total 5\n..."}],"details":{}}}
{"type":"tool_execution_end","toolCallId":"654406736","toolName":"bash",
 "result":{"content":[...]},"isError":false}
```

- **Full args before execution**, so a gate has everything it needs to decide.
- **One `toolCallId` across all three events**, and the same id appears again on the `toolResult`
  message — unlike Hermes, where the approval id and the `tool_call` id differ (C8).
- **Streaming partial output** via `tool_execution_update`, which Hermes has no equivalent of.
- **Explicit `isError`**, separate from the content.

| | Pi | Hermes (ACP) |
| --- | --- | --- |
| tool start with args | yes | yes |
| incremental output | yes | no |
| tool completion | yes | emitted but [unreachable on Windows](#c7-hermes-deadlocks-on-the-first-tool-call-of-any-kind-in-acp-mode-on-windows-until-the-client-closes-stdin) |
| stable correlation id | yes | no — approval id ≠ tool call id |

**The Event model should be shaped against Pi's vocabulary and degrade for Hermes**, not the reverse.
Designing against the weaker surface would discard information this one provides for free.

### P5. Rich per-turn metadata, and the Vendor is named in the stream

Every assistant `message_end` and `turn_end` carries `api`, `provider`, `model` and a `usage` object
with `input` / `output` / `cacheRead` / `cacheWrite` / `reasoning` / `totalTokens` plus a parallel
`cost` breakdown. `stopReason` is `"pending"` on `message_start` and resolves to `"toolUse"` or
`"stop"`. Reasoning is a first-class content block (`{"type":"thinking","thinkingSignature":...}`).
`[capture]`

Two consequences. Usage accounting is **per turn**, not merely per session, so the Hub gets cost and
token data without instrumenting anything. And the Data Plane's identity is **visible in the Control
Plane stream** — the events name the provider and model the Daemon routed to, which is exactly the
correlation the orchestrator would otherwise have to reconstruct.

`agent_end` repeats the **entire conversation** in a `messages` array (~6% of stream bytes here, and it
grows with history). Worth dropping or truncating at the normalisation boundary rather than storing
twice.

### P6. Nothing is gated, confirmed

Pi ran `bash: ls -la` with no approval event of any kind. §3's "nothing is gated" is confirmed
empirically rather than merely documented, and the asymmetry with Hermes stands: **Approval Policy
cannot be uniform across harnesses.** Hermes gates in-protocol; Pi has nothing to intercept without
writing an extension.

Also settled: `pi -p`, `--mode json` and `--mode rpc` all exit **0** on success (§8 listed exit codes as
undocumented for both harnesses); the `session` event is emitted first and carries
`{version: 3, id, timestamp, cwd}`; and `agent_settled` appears in **both** `--mode json` and
`--mode rpc`, as a bare marker distinct from `agent_end`.

### P7. Approval **is** reachable over RPC, through an extension

P6 stands — Pi gates nothing by default. But an extension can gate, and its dialogs cross the RPC
boundary. Captured both ways with Pi's own bundled `examples/extensions/permission-gate.ts`:
`[capture]`

```json
<<< {"type":"tool_execution_start","toolCallId":"768363200","toolName":"bash",
     "args":{"command":"rm -rf scratch.txt"}}
<<< {"type":"extension_ui_request","id":"22ae4ead-…","method":"select",
     "title":"⚠️ Dangerous command:\n\n  rm -rf scratch.txt\n\nAllow?","options":["Yes","No"]}
>>> {"type":"extension_ui_response","id":"22ae4ead-…","value":"Yes"}
<<< {"type":"tool_execution_end","toolCallId":"768363200","isError":false,
     "result":{"content":[{"type":"text","text":"(no output)"}]}}
```

Answering `"No"` blocks it: the file survived, and `tool_execution_end` carried
`"isError": true` with `"Blocked by user"` as its content. Both outcomes verified against the
filesystem, not just the stream.

`docs/rpc.md` documents the general mechanism: `select`, `confirm`, `input` and `editor` block on a
matching `extension_ui_response`, while `notify` / `setStatus` / `setWidget` / `setTitle` are
fire-and-forget. A dialog may carry a **`timeout`**, after which the agent auto-resolves and the client
never has to track it — Pi has a built-in "nobody answered" path that Hermes lacks.

Three traps for the Event model, all confirmed in the capture:

1. **`tool_execution_start` fires before the gate resolves.** The stream announces the tool as started
   while it is still awaiting a decision. A start event is not evidence that anything ran.
2. **The UI request has no `toolCallId`** — only `id`, `method`, `title` and `options`, with the
   command embedded in `title`, a display string containing emoji and newlines. Correlation is by
   ordering, not by key. This is *worse* than Hermes' C8, which at least carries a structured
   `rawInput`.
3. **A refusal is a tool error, not an event kind** — `isError: true` plus free text chosen by the
   extension author. There is no protocol-level "refused" signal to match on.

So both harnesses can gate, and **neither reports refusal in a form the orchestrator can trust
structurally**: Hermes cannot distinguish a human's refusal from an internal failure (C9), and Pi does
not mark refusal as anything but an error string. The Approval Policy must track its own decisions
rather than infer them from the Harness's output.

---

## CORRECTION — 2026-09-03, the Gate Dispatch ships

### P8. The Gate announces before `Start` returns, and P7's trap 2 is avoidable

P7 used Pi's bundled example. `internal/harness/dispatch-gate.js` is the Gate the Daemon ships, and
`captures/pi-gate-dispatch/` is five runs of it against Pi 0.84.3. `[capture]`

**Pi loads a plain `.js` extension through `-e`.** Every path in `extensions.md` is `*.ts`, and the
auto-discovery globs are `.ts` only, so this was not documented. It matters because it keeps the Gate
in the two languages this repo already has. Auto-discovery was not tested and the globs suggest it
would not find a `.js` file, but the Daemon passes `-e` and never relies on discovery.

**It announces.** A `session_start` handler calls `ctx.ui.notify`, which is fire-and-forget, and the
notification lands as frame 2 with the first command's response as frame 3. ADR 0008's requirement
that a loadable Gate announce itself before `Start` returns is met, so the Pi Adapter declares Gates
rather than forcing every slot to `auto`.

```json
>>> {"id":"start-probe","type":"get_state"}
<<< {"type":"extension_ui_request","id":"1b959a09-…","method":"notify",
     "message":"{\"protocol\":\"dispatch.gate/1\",\"event\":\"ready\",…}","notifyType":"info"}
<<< {"id":"start-probe","type":"response","command":"get_state","success":true,"data":{…}}
```

**A failed load has three signals, and all three come before `Start` returns.** An extension that does
not parse makes Pi exit 1 in under a second, before it answers any command, with the parse error on
stderr. An extension that loads and then throws in `session_start` leaves Pi running, and Pi reports
it as a frame of its own that arrives before the probe response:

```json
<<< {"type":"extension_error","extensionPath":"…\pi-gate-silent-gate.js",
     "event":"session_start","error":"the Gate failed to announce"}
```

A probe answered with no announcement ahead of it is the third and most general shape. Any of the
three is enough to fail the launch.

**Trap 2 is avoidable.** P7 recorded that the UI request carries no `toolCallId`, leaving correlation
to ordering. `title` is the one field the extension controls, so the Gate puts JSON there and the
Daemon reads it as a payload:

```json
<<< {"type":"extension_ui_request","id":"59c9096a-…","method":"select",
     "title":"{\"protocol\":\"dispatch.gate/1\",\"event\":\"request\",
               \"toolCallId\":\"796707030\",\"toolName\":\"bash\",\"kind\":\"execute\"}",
     "options":["allow","deny"]}
```

Traps 1 and 3 stand: `tool_execution_start` still fires before the Gate resolves, and a denial still
arrives as `isError: true` with free text. The text is now chosen by this repo, so it can be matched
on, which is a smaller claim than a protocol signal.

**Coverage.** All five ToolKinds were held in both the allow and the deny run, and
`ungated_tool_calls` is empty in both. File state settles the pair rather than the wording: the deny
run was told to write a file and to delete another, and its working directory is unchanged.

`fetch` and `other` needed two no-op fixture tools (`scripts/pi-gate-probe-tools.js`), because Pi's
eight built-in tools reach `read`, `edit` and `execute` only. So a `fetch` Gate declared against a
stock Pi is a Gate that never fires. That is a fact about Pi's tool set rather than about the Gate.

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

> **SUPERSEDED — see [C1](#c1-the-v1-agent-run-api-does-not-exist).** Every `/v1/*` endpoint below is
> absent from Hermes v0.19.0; they 404 on a live server. This section records what the documentation
> claims, not what ships.

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
| Structured surface that does exist | ~~ACP stdio, TUI gateway JSON-RPC (stdio/WS), HTTP+SSE API server~~ → **ACP stdio, TUI gateway JSON-RPC. The HTTP+SSE run API does not exist ([C1](#c1-the-v1-agent-run-api-does-not-exist))** | (same process, no extra server needed) |
| Best surface for a Go daemon | ~~HTTP API server (`/v1/runs` + SSE)~~ → **`hermes acp` ([C2](#c2-the-recommended-surface-flips-from-httpsse-to-acp))** | `--mode rpc` |
| Tool call visible before execution | Yes, on protocol surfaces (`tool.start`) and via hooks | Yes (`tool_execution_start` on the wire) |
| Full tool lifecycle | start only in practice ([C7](#c7-hermes-deadlocks-on-the-first-tool-call-of-any-kind-in-acp-mode-on-windows-until-the-client-closes-stdin)) | **start + streaming update + end, one stable `toolCallId` ([P4](#p4-the-tool-lifecycle-is-complete-and-correlates-by-a-stable-id))** |
| Approval interception | **Yes, three ways**: `pre_tool_call` shell hook (JSON stdin → `{"action":"block"}` stdout), protocol `approval.request`/`approval.respond`, ACP `session/request_permission` — the ACP one **confirmed firing, with the full diff in the payload ([C8](#c8-the-edit-approval-payload-captured))**, but it **fails closed and can deny without asking ([C9](#c9-hermes-can-report-an-edit-as-denied-by-acp-client-without-ever-asking-the-client))**, while the shell hook **fails open ([C11](#c11-pre_tool_call-fails-open-the-opposite-of-the-acp-edit-path))** | **Yes, but only via a custom extension's blockable `tool_call` hook**; no built-in approval, no CLI flag — **confirmed working over RPC ([P7](#p7-approval-is-reachable-over-rpc-through-an-extension))** |
| Delegates tool execution to the client | ~~unknown~~ → **No, by construction. `initialize` ignores `clientCapabilities`; `request_permission` is the only client method Hermes calls ([C6](#c6-hermes-delegated-nothing-to-the-client))** | n/a — same process |
| Gated by default? | Only a dangerous-pattern list; `approvals.mode: smart` auto-approves low risk | Nothing is gated — **confirmed empirically ([P6](#p6-nothing-is-gated-confirmed))** |
| OpenAI-compatible endpoint | ~~`OPENAI_BASE_URL` + `OPENAI_API_KEY`~~ → **`model.base_url` in `config.yaml` under `%LOCALAPPDATA%\hermes`; `~/.hermes/.env` is not read ([C3](#c3-configuration-base_url-in-configyaml-not-openai_base_url-in-hermesenv))** | ~~Extension only~~ → **`~/.pi/agent/models.json`, no extension needed ([P1](#p1-an-arbitrary-openai-compatible-endpoint-needs-no-extension))** |
| Per-invocation model override | `-m/--model`, `--provider`, `HERMES_INFERENCE_MODEL` | `--provider`, `--model`, `--api-key`, `--thinking` |
| Long-lived process | ACP / gateway / serve; `-z` and `-q` are one-shot | `--mode rpc`; `-p` and `--mode json` are one-shot — but **all of them read stdin to EOF ([P2](#p2--p-is-not-one-shot-it-reads-stdin-to-eof))** |
| Session store | SQLite `~/.hermes/state.db` | JSONL tree `~/.pi/agent/sessions/--<path>--/*.jsonl` |
| Session id | `YYYYMMDD_HHMMSS_<hex>` | UUID (header) + 8-hex entry ids |
| Resume | `-c`, `-r <id>`, `-c "<title>"` | `-c`, `-r`, `--session`, `--fork` |
| Config/state dir override | `HERMES_HOME` | `PI_CODING_AGENT_DIR`, `--session-dir` |
| Exit codes | ~~undocumented~~ → **`-z` exits 0 ([C10](#c10-exit-codes-and-other-small-settled-items))** | ~~undocumented~~ → **`-p`, `--mode json`, `--mode rpc` all exit 0 ([P6](#p6-nothing-is-gated-confirmed))** |
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
