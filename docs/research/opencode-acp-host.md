# OpenCode ACP on a Host

Answers issue #16 and resolves the conditional half of
[ADR 0003](../adr/0003-opencode-replaces-hermes-as-the-second-harness.md).

**All three gates pass. OpenCode enters v1.**

Capture bytes: [`captures/opencode/`](captures/opencode/). Counted by
`scripts/opencode-gates.py`, which reads the frames rather than this script's own
output.

## What was captured

| | |
| --- | --- |
| Host | `Victor@ZenitoBurrito:22`, Windows 11, reached over SSH |
| Client | `MINGW64_NT-10.0-26200 -PC`, a different machine |
| Harness | OpenCode 1.18.23, `opencode acp` |
| Spawned as | `C:/Users/Victor/AppData/Roaming/npm/node_modules/opencode-ai/bin/opencode.exe acp` |
| Vendor | Ollama, then repeated against LM Studio and llama-swap |
| Models | one per Vendor, listed under [Vendor coverage](#vendor-coverage) |
| Date | 2026-08-27, 19:23 to 22:47 +01:00 |

Three runs per Vendor, one per tool class, so the counts cannot be confused with
each other. No TTY on either side. The supervisor owned stdin.

The gate counts below are from the Ollama capture. LM Studio and llama-swap give
the same three verdicts — see [Vendor coverage](#vendor-coverage).

## Gate 1 — a tool call completes on the Host over SSH: PASS

All three runs ended `stop_reason: end_turn` with `agent_exit_code: 0`, and the
Session changed the Host's disk: `out.txt` holds `banana`.

`opencode acp` starts under a supervisor that owns stdin rather than inheriting
it. This is the bar Hermes never cleared.

**A supervisor must resolve the package binary, not the PATH name.** Spawning the
bare name fails:

```
opencode      FileNotFoundError: [WinError 2] The system cannot find the file specified
opencode.CMD  1.18.23   (works, but puts a cmd.exe between supervisor and Harness)
node_modules/opencode-ai/bin/opencode.exe   1.18.23
```

That is the same shape as the Hermes launcher-chain failure: on the PATH,
answers a shell, and unusable by a supervisor. The Daemon must do what
`resolve-harness-exe.py` does.

## Gate 2 — `session/request_permission` per tool class: PASS

| tool class | started | ended | asked permission |
| --- | --- | --- | --- |
| `edit` | 1 | 1 | 1 |
| `execute` | 1 | 1 | 1 |
| `read` | 2 | 2 | 0 |

`read` is exempt under ADR 0003, and the exemption fired exactly as predicted:
two reads, no gate. `edit` and `execute` each asked once per call.

The request carries `toolCall.kind` and a `title` — the file path for `edit`, the
command line for `execute` — so **the Daemon can decide on content, not only on
class**. Options offered:

```
once     allow_once     "Allow once"
always   allow_always   "Always allow"
reject   reject_once    "Reject"
```

There is no *reject always*. A standing refusal has to be held by the Daemon, not
delegated to the Harness, which is what ADR 0003 already requires.

**Ordering matters for the Client.** The tool call is announced `pending`, moves
to `in_progress`, and only then is permission requested. A Client that renders
`in_progress` as "this ran" shows a tool as having executed before the human has
been asked.

## Gate 3 — terminal Events per tool class: PASS

Four tool calls started, four reached a terminal status. No quiet classes, so the
adapter's synthesis path was never needed here.

This is the inverse of Hermes, which on the same counter gave `edit` 6 started
and 0 ended, `read` 7 and 0.

One read reached `failed`, and it is worth keeping. The model asked for
`work\note`; OpenCode answered

```
File not found: C:\Users\Victor\capstone-opencode\work\note

Did you mean one of these?
C:\Users\Victor\capstone-opencode\work\notes.txt
```

and the model retried and completed. `failed` is used correctly as a terminal
status and carries the reason in a `content` block. A model error is not a
Harness defect, and the Harness recovered from it inside one turn.

## The ACP method set

Recorded, not gated. Counts across the Ollama capture's three runs.

| direction | method | n |
| --- | --- | --- |
| agent → client | `session/update` | 42 |
| agent → client | `session/request_permission` | 2 |
| agent → client | `fs/write_text_file` | 1 |
| client → agent | `initialize` | 3 |
| client → agent | `session/new` | 3 |
| client → agent | `session/prompt` | 3 |
| client → agent | `session/close` | 3 |

**OpenCode delegates writes to the client and does not delegate reads.** The
client advertised `fs.readTextFile: true` and `fs/read_text_file` was never
called; the single write went through `fs/write_text_file`. This changes #7 in
two directions:

- The Daemon gains a second lever on writes that Hermes never offered (C6:
  Hermes delegated nothing). A write can be refused at the fs method even after
  the permission gate has passed.
- The Daemon has **no interception point at all for reads**. This is stronger
  than ADR 0003 assumed. The ADR says the Approval Policy cannot honour *wait* or
  *refuse* for reads because there is no permission key. The capture shows the
  read never crosses the seam either, so there is nothing to hook. Workspace Root
  really is the only bound.

`terminal/*` was never used: the client advertised `terminal: false` and OpenCode
ran `bash` in-process.

Agent capabilities, identical in every run against every Vendor:

```json
{"loadSession": true,
 "mcpCapabilities": {"http": true, "sse": true},
 "promptCapabilities": {"embeddedContext": true, "image": true},
 "sessionCapabilities": {"close": {}, "fork": {}, "list": {}, "resume": {}}}
```

`protocolVersion: 1`.

## Configuration discovery

Recorded, not gated. **ADR 0003's per-Session config assumption holds**, with one
qualification the ADR did not state.

`opencode models`, run from the Session's working directory, lists the provider
written beside it:

```
opencode/big-pickle
...
capstone/qwen3.5:9b        <- the per-Session config
lmstudio/gpt-oss-20b
...
```

The working-directory `opencode.json` **merges** with the user's global config
rather than replacing it. The Model chosen at Session start does land, and two
Sessions on different Models cannot fight over one file — both claims in ADR 0003
survive. But a per-Session config does **not isolate** a Session: every provider
in the user's global config stays visible and reachable from inside it.

## Vendor coverage

Recorded, not gated. All three Vendors were driven to a tool call, and all three
pass all three gates.

| Vendor | Model | started / ended / asked | gates |
| --- | --- | --- | --- |
| Ollama | `qwen3.5:9b` | `edit` 1/1/1, `execute` 1/1/1, `read` 2/2/0 | pass |
| LM Studio | `qwen/qwen3.5-9b` | `edit` 1/1/1, `execute` 1/1/1, `read` 1/1/0 | pass |
| llama-swap | `qwen3.5-9b` | `edit` 1/1/1, `execute` 1/1/1, `read` 1/1/0 | pass |

Same shape every time, and `read` silent every time.

**The Event vocabulary is Vendor-independent.** Compared frame by frame, all
three agree exactly:

```
methods        fs/write_text_file, initialize, session/close, session/new,
               session/prompt, session/request_permission, session/update
sessionUpdate  agent_message_chunk, agent_thought_chunk, available_commands_update,
               tool_call, tool_call_update, usage_update
kinds          edit, execute, read
stopReason     end_turn
```

The only difference is `failed`, which appears in the Ollama statuses because one
read there failed on a model error. It is a difference in what happened, not in
the vocabulary. This is the property Pi was proven to have, so the Event model
can keep Vendor identity in metadata.

**Each run used to overwrite the last.** Every capture landed in one directory,
so LM Studio overwrote Ollama and llama-swap overwrote LM Studio, and the first
LM Studio run was gone before anyone noticed. Ollama was recovered from
`7db708f`, LM Studio was re-run and recovered from the Host, and the script now
lands under `captures/opencode/<vendor>/`. `lmstudio/` is missing the two files
the Client writes — see the capture README.

## What this capture does not establish

Named here so the answer on #16 cannot quietly overclaim.

- **One Model per Vendor.**
- **One run per class.** Counts are 1, 1 and 2. This does not match the 12/12
  rigour that produced the Hermes findings, and a silent class could still be
  hiding behind a single sample.
- **`webfetch` was never exercised**, though it is the third class OpenCode's
  permission block can gate.
- **No refusal was tested.** The capture answered every `session/request_permission`
  with allow. Whether `reject` actually stops the tool, and what the Harness
  reports when it does, is untested. Hermes reported `denied by ACP client`
  without ever asking, so this is not a safe assumption to carry over.

## Conclusion

ADR 0003's conditional half resolves in favour of OpenCode. Gate 1 passes, which
was the fatal one. Gates 2 and 3 pass, with `read` exempt and the cost of that
exemption now measured rather than inferred.

`CONTEXT.md` and `docs/architecture-sketch.html` still name Hermes as a Harness.
ADR 0003 says they change when the capture returns.
