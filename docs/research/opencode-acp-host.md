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
| Harness | OpenCode 1.18.23, `opencode acp`. The llama-swap re-capture is 1.18.25 |
| Spawned as | `C:/Users/Victor/AppData/Roaming/npm/node_modules/opencode-ai/bin/opencode.exe acp` |
| Vendor | Ollama, then repeated against LM Studio and llama-swap |
| Models | one per Vendor, listed under [Vendor coverage](#vendor-coverage) |
| Date | 2026-08-27, 19:23 to 22:47 +01:00. llama-swap re-captured 2026-08-28, 20:23 |

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

Recorded, not gated. **ADR 0003's per-Session config assumption holds**, with two
qualifications the ADR did not state.

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
rather than replacing it. Two Sessions on different Models cannot fight over one
file, so that claim in ADR 0003 survives. A per-Session config does **not isolate**
a Session: every provider in the user's global config stays visible and reachable
from inside it.

**The Model chosen at Session start lands, but does not always win.** The file's
`model` key was honoured in the 2026-08-28 llama-swap run, where `session/new`
reported `currentValue: capstone/qwen3.5-9b`. It was overridden in all six
2026-08-27 runs, which reported `currentValue: opencode/big-pickle`, a hosted
OpenCode Zen model, while their own config named a `capstone/` Model. OpenCode Zen
was configured on the Host then and is absent from the later run's provider list,
so something in the global config outranked the working-directory one. Nothing in
either capture ever calls a method to set the option.

So writing the file is not enough to know which Model answers. `session/new`
reports what the Session will actually use, and that is the field to read.
[ADR 0006](../adr/0006-the-harness-adapter-interface.md) makes reading it back part
of the adapter's `Start`, and has the Daemon log the Model it confirmed rather than
the one it asked for.

## Vendor coverage

Recorded, not gated. All three Vendors were driven to a tool call, and all three
pass all three gates.

| Vendor | Model | started / ended / asked | gates |
| --- | --- | --- | --- |
| Ollama | `qwen3.5:9b` | `edit` 1/1/1, `execute` 1/1/1, `read` 2/2/0 | pass |
| LM Studio | `qwen/qwen3.5-9b` | `edit` 1/1/1, `execute` 1/1/1, `read` 1/1/0 | pass |
| llama-swap | `qwen3.5-9b` | `edit` 1/1/1, `execute` 1/1/1, `read` 1/1/0 | pass |

Same shape every time, and `read` silent every time. The llama-swap row is the
2026-08-28 re-capture, on OpenCode 1.18.25; the other two are 2026-08-27, on
1.18.23. See [The llama-swap row was wrong for a day](#the-llama-swap-row-was-wrong-for-a-day).

**The Event vocabulary is Vendor-independent, with one exception.** Compared
frame by frame:

```
methods        fs/write_text_file, initialize, session/close, session/new,
               session/prompt, session/request_permission, session/update
sessionUpdate  agent_message_chunk, agent_thought_chunk, available_commands_update,
               tool_call, tool_call_update
kinds          edit, execute, read
stopReason     end_turn
```

All three agree on every line above. **`usage_update` is the exception**: Ollama
and LM Studio emit it, and the llama-swap run does not. Two things differ between
that run and the others, the Vendor and the OpenCode version, so which one causes
it is not established here.

It costs nothing. Per-Prompt token counts come from the `session/prompt`
**response**, which carries `usage` in all three, and `usage_update` is a
context-window notification carrying `used`, `size` and a `cost` that is always
zero on a local Vendor. But it does set a rule for the adapter, the mirror of the
one Hermes' out-of-schema `usage_update` already set: an adapter must tolerate a
native kind that is absent, as well as one it does not recognise.

The other difference is `failed`, which appears in the Ollama statuses because one
read there failed on a model error. That is a difference in what happened, not in
the vocabulary. So the Event model can still keep Vendor identity in metadata.

### The llama-swap row was wrong for a day

**Between 2026-08-27 and 2026-08-28, `captures/opencode/llamaswap/` held the LM
Studio run**, and this table's third row rested on it. The two directories were
byte-identical across all three `*-frames.jsonl` and `*-raw.log` files, shared
OpenCode session ids that are minted fresh per `session/new`
(`ses_fbaced840ffecF2q3xGDWdrzPa` for the edit run in both), shared frame
timestamps to the microsecond, and the `llamaswap/manifest.txt` of the day read
`Vendor: lmstudio at http://127.0.0.1:1234` where llama-swap is on `:8080`.

The cause was the capture script's Vendor fallback. llama-swap was not serving,
`--vendor llamaswap` fell back to the first Vendor that was, and the run was filed
under the Vendor that had been **asked for** rather than the one that **answered**.
The fallback warns and carries on, which is the whole problem: a warning in a
scrolling log is not a label on an artefact.

This is the sixth adjacent-check failure in this repo's research and the second in
this capture alone. It generalises past capture scripts, and
[ADR 0006](../adr/0006-the-harness-adapter-interface.md) takes it as a rule for the
Daemon: a Session records the Vendor and Model that served it, never the ones it
was configured with.

The 2026-08-28 re-capture is a real llama-swap run, over SSH to the same Host, and
it passes all three gates. It is distinct from `lmstudio/` by sha256, by session id
and by timestamp, and its `session/new` reports `currentValue: capstone/qwen3.5-9b`.

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

  The Adapter in `internal/harness/acp.go` now has to answer refusals, and what it
  sends is the option whose `kind` is `reject_once`, picked by kind rather than by
  the id that spells it. That much is in the recorded options. **What OpenCode does
  after receiving one is still owed a capture**, and until that capture exists
  nothing may claim the tool did not run on the strength of it: the Daemon's own
  `ApprovalDecided` is the record of the refusal, and the Harness is read only as
  corroboration.

## Conclusion

ADR 0003's conditional half resolves in favour of OpenCode. Gate 1 passes, which
was the fatal one. Gates 2 and 3 pass, with `read` exempt and the cost of that
exemption now measured rather than inferred.

`CONTEXT.md` named Hermes as a Harness and now names OpenCode, which ADR 0003
required once the capture returned.
