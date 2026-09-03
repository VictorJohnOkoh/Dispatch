# The Dispatch Gate for Pi — 2026-09-03

Four runs of `scripts/pi-gate-capture.py` against Pi 0.84.3 in `--mode rpc`, loading
[`internal/harness/dispatch-gate.ts`](../../../../internal/harness/dispatch-gate.ts), the Gate the
Daemon ships. This closes the hole [#56](https://github.com/VictorJohnOkoh/Dispatch/issues/56) named.

The earlier [`../pi-gate/`](../pi-gate/) captures proved the mechanism with Pi's own bundled
`permission-gate.ts`, which fires on `bash` alone and only for three regexes, and never announces
itself. These runs replace it with a Gate in ADR 0008's sense.

Provider `lmstudio`, model `qwen/qwen3.5-9b`, one prompt per tool.

| run | extensions | decisions | outcome |
| --- | --- | --- | --- |
| `gate-allow-*` | Gate + probe tools | allow everything | 6 tool calls, all held, all ran. `hello.txt` written. |
| `gate-deny-*` | Gate + probe tools | deny everything | 5 tool calls, all held, none ran. `hello.txt` absent, `scratch.txt` survived. |
| `no-gate-*` | none | — | no announcement, `get_state` still answered. |
| `broken-gate-*` | one unparseable `.ts` | — | Pi exited 1 before the first command. |

Files hashed at capture time (sha256, first 16):
`dispatch-gate.ts` `3d7b487d11137da3`, `pi-gate-probe-tools.ts` `e1e36e309f104dbb`.

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
costs no round-trip and cannot deadlock the launch.

**So the Adapter declares Gates, and a failed load fails the launch.** That is the first of ADR 0008's
two branches, and the ugly branch is not needed.

**A failed load already fails the launch on its own.** `broken-gate-*` gave Pi an unparseable
extension and Pi exited 1 in 0.62 s, before answering anything, with the reason on stderr:

```
Error: Failed to load extension "…/broken-gate.ts": Failed to load extension: ParseError: Missing semicolon.
Hint: Start without extensions using "pi -ne".
```

The Daemon therefore gets two independent signals, and should use both: a Pi that exits during launch,
and a Pi that starts but answers the probe with no `ready` frame ahead of it. `no-gate-*` is the second
shape — `get_state` answered as frame 2 and no announcement at all.

## Coverage: all five ToolKinds, and nothing sails past

`kinds_gated` in the two summaries:

| ToolKind | allow run | deny run | tool |
| --- | --- | --- | --- |
| `read` | 1 | 1 | `read` |
| `edit` | 1 | 1 | `write` |
| `execute` | 2 | 1 | `bash` |
| `fetch` | 1 | 1 | `fetch` |
| `other` | 1 | 1 | `note` |

`ungated_tool_calls` is empty in both. Every `tool_execution_start` was matched by a Gate request for
the same `toolCallId`.

`fetch` and `note` come from [`scripts/pi-gate-probe-tools.ts`](../../../../scripts/pi-gate-probe-tools.ts),
two no-op tools loaded only by the capture. Pi has eight built-in tools and none of them is a fetch,
and none of them falls outside the Gate's table, so without those two the `fetch` and `other` slots
could not be driven at all. They are a fixture, not something the Daemon ships.

## The request frame, and the `toolCallId` the old capture lost

```json
<<< {"type":"extension_ui_request","id":"59c9096a-…","method":"select",
     "title":"{\"protocol\":\"dispatch.gate/1\",\"event\":\"request\",
               \"toolCallId\":\"796707030\",\"toolName\":\"bash\",\"kind\":\"execute\"}",
     "options":["allow","deny"]}
>>> {"type":"extension_ui_response","id":"59c9096a-…","value":"allow"}
```

Trap 2 in [`../pi-gate/README.md`](../pi-gate/README.md) was that Pi's UI request carries no
`toolCallId`, so an approval could only be correlated by ordering. Putting JSON in `title` fixes it:
`title` is the one field the extension controls, and the Daemon reads it as a payload rather than as
prose. The same trick carries the announcement in `notify`'s `message`.

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
  Gate. This is captured only for `deny`; the timeout path is untested.
- `read`, `grep`, `find` and `ls` all map to `read`, and only `read` was exercised. The mapping is a
  table in one file, not a per-tool behaviour.
- The Gate is loaded with `-e`. Pi's project trust guards which extensions load, and it is not a tool
  gate — see `security.md`. Nothing here depends on it.
