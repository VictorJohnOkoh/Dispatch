# The Dispatch Gate for Pi — 2026-09-03

Five runs of `scripts/pi-gate-capture.py` against Pi 0.84.3 in `--mode rpc`, loading
[`internal/harness/dispatch-gate.js`](../../../../internal/harness/dispatch-gate.js), the Gate the
Daemon ships. This closes the hole [#56](https://github.com/VictorJohnOkoh/Dispatch/issues/56) named.

The earlier [`../pi-gate/`](../pi-gate/) captures proved the mechanism with Pi's own bundled
`permission-gate.ts`, which fires on `bash` alone and only for three regexes, and never announces
itself. These runs replace it with a Gate in ADR 0008's sense.

Provider `lmstudio`, model `qwen/qwen3.5-9b`, one prompt per tool. Pi's own extensions are
TypeScript and these are plain JavaScript, because this repo is Go and vanilla JS. Pi loads either
through `-e`, which these runs show.

| run | extensions | decisions | outcome |
| --- | --- | --- | --- |
| `gate-allow-*` | Gate + probe tools | allow everything | 6 tool calls, all held, all ran |
| `gate-deny-*` | Gate + probe tools | deny everything | 5 tool calls, all held, none ran |
| `no-gate-*` | none | — | no announcement, `get_state` still answered |
| `broken-gate-*` | one unparseable `.js` | — | Pi exited 1 before the first command |
| `silent-gate-*` | a Gate that throws in `session_start` | — | Pi kept running, no announcement, `extension_error` |

Files hashed at capture time (sha256, first 16): `dispatch-gate.js` `25aeff568aa06649`,
`pi-gate-probe-tools.js` `3638c03406f34383`, `pi-gate-silent-gate.js` `5bba7646f750ef1c`.

## The answer #56 asked for: yes, it announces before `Start` returns

`Start` returns when the Daemon has one command round-trip, so the capture sends `get_state` as the
very first frame on stdin and compares sequence numbers. In both gated runs the announcement is frame
2 and the probe response is frame 3.

```json
>>> {"id":"start-probe","type":"get_state"}
<<< {"type":"extension_ui_request","id":"1b959a09-…","method":"notify",
     "message":"{\"protocol\":\"dispatch.gate/1\",\"event\":\"ready\",\"reason\":\"startup\",
                 \"kinds\":[\"read\",\"edit\",\"execute\",\"fetch\",\"other\"]}",
     "notifyType":"info"}
<<< {"id":"start-probe","type":"response","command":"get_state","success":true,"data":{…}}
```

The announcement is a `session_start` handler calling `ctx.ui.notify`, which is fire-and-forget, so it
costs no round-trip and cannot make the launch wait.

**So the Adapter declares Gates, and a failed load fails the launch.** That is the first of ADR 0008's
two branches. The second branch, no Gates and every slot forced to `auto`, is not needed.

## A failed load has two shapes, and both were captured

**The extension does not parse.** `broken-gate-*`. Pi exited 1 in 0.52 s, before answering any
command, with the reason on stderr:

```
Error: Failed to load extension "…/broken-gate.js": Failed to load extension: ParseError: Missing semicolon.
Hint: Start without extensions using "pi -ne".
```

**The extension loads and then fails.** `silent-gate-*` uses
[`scripts/pi-gate-silent-gate.js`](../../../../scripts/pi-gate-silent-gate.js), which registers its
`tool_call` handler and throws in `session_start`. Pi kept running and answered the probe, and the
announcement never arrived. Pi reports this as a frame of its own, which arrives before the probe
response:

```json
<<< {"type":"extension_error","extensionPath":"…\\pi-gate-silent-gate.js",
     "event":"session_start","error":"the Gate failed to announce"}
```

So the Daemon has three signals and all three are available before `Start` returns: a Pi that exits
during launch, an `extension_error` frame, and a probe answered with no `ready` frame ahead of it. The
third is the general one and the other two are more specific. `no-gate-*` is the third shape with no
extension supplied at all — `get_state` answered as frame 2, no announcement.

## Coverage: all five ToolKinds, and no tool call sailed past

`kinds_gated` in the two gated summaries. Every kind is held in both; the counts differ only because
the allow run's model chose `bash` twice.

| ToolKind | allow | deny | tool exercised | tools in the table not exercised |
| --- | --- | --- | --- | --- |
| `read` | 1 | 1 | `read` | `grep`, `find`, `ls` |
| `edit` | 1 | 1 | `write` | `edit` |
| `execute` | 2 | 1 | `bash` | `powershell` |
| `fetch` | 1 | 1 | `fetch` | — |
| `other` | 1 | 1 | `note` | anything unnamed |

`ungated_tool_calls` is empty in both runs. Every `tool_execution_start` was matched by a Gate request
for the same `toolCallId`. The mapping is a table in one file, so an unexercised name is a table entry
and not a separate behaviour.

**`fetch` and `other` needed a fixture.** Both come from
[`scripts/pi-gate-probe-tools.js`](../../../../scripts/pi-gate-probe-tools.js), two no-op tools loaded
only by the capture. Pi has eight built-in tools and none of them is a fetch, and none of them falls
outside the Gate's table, so against a stock Pi the `fetch` and `other` slots cannot be driven at all.
A `fetch` Gate declared for a stock Pi is a Gate that never fires. That is a fact about Pi's tool set,
not about the Gate.

## File state settles allow against deny

The summaries record the working directory before and after, because the wording of a tool result is
weaker evidence than the files.

| run | `workdir_before` | `workdir_after` |
| --- | --- | --- |
| `gate-allow` | `notes.txt`, `scratch.txt` | `hello.txt`, `notes.txt`, `scratch.txt` |
| `gate-deny` | `notes.txt`, `scratch.txt` | `notes.txt`, `scratch.txt` |

The deny run was told to write `hello.txt` and to `rm -rf scratch.txt`. Neither happened.

## The request frame, and the `toolCallId` the old capture lost

```json
<<< {"type":"extension_ui_request","id":"59c9096a-…","method":"select",
     "title":"{\"protocol\":\"dispatch.gate/1\",\"event\":\"request\",
               \"toolCallId\":\"796707030\",\"toolName\":\"bash\",\"kind\":\"execute\"}",
     "options":["allow","deny"]}
>>> {"type":"extension_ui_response","id":"59c9096a-…","value":"allow"}
```

Trap 2 in [`../pi-gate/README.md`](../pi-gate/README.md) was that Pi's UI request carries no
`toolCallId`, so an approval could only be correlated by ordering. Putting JSON in `title` removes the
problem: `title` is the one field the extension controls, and the Daemon reads it as a payload rather
than as prose. The announcement uses `notify`'s `message` the same way.

Traps 1 and 3 stand unchanged and are the Adapter's problem at
[#57](https://github.com/VictorJohnOkoh/Dispatch/issues/57): `tool_execution_start` still fires before
the Gate resolves, and a denial still arrives as a tool error rather than as a kind of its own —

```json
<<< {"type":"tool_execution_end","toolCallId":"650693561","toolName":"read",
     "result":{"content":[{"type":"text","text":"denied by Dispatch"}],"details":{}},
     "isError":true}
```

The string is now chosen by this repo rather than by an example, so the Adapter can match on it. It
still reaches the model as tool output, which is the model learning it was refused.

## Caveats

- One model, one Host, one Pi version. A `qwen3.5-9b` that ignores the prompt and picks a different
  tool changes which tools appear, not whether they were held: the allow run answered "use the ls
  tool" with `bash`, and the Gate held that too.
- The Gate blocks on a cancelled or timed-out dialog as well as on `deny`. Failing open would not be a
  Gate. Only `deny` is captured; the timeout path is untested, and Pi's dialog `timeout` field is
  unused here.
- The Gate is loaded with `-e`. Pi's project trust guards which extensions load, and it is not a tool
  gate — see `security.md`. Nothing here depends on it.
