# Pi approval-gate captures — 2026-08-16

Two runs of `scripts/pi-rpc-capture.py` against Pi in `--mode rpc`, loading Pi's own bundled
`examples/extensions/permission-gate.ts` (copied here verbatim). Same prompt each time —
`rm -rf scratch.txt` — answered `Yes` in one and `No` in the other.

Evidence for correction **P7** in [`../../harness-control-surfaces.md`](../../harness-control-surfaces.md).
This is Pi's counterpart to the Hermes approval payload in [C8](../../harness-control-surfaces.md).

| file | outcome |
| --- | --- |
| `gate-allow-*` | Answered `Yes`. Tool ran, **file deleted**, `isError: false`. |
| `gate-deny-*` | Answered `No`. Tool blocked, **file survived**, `isError: true`. |

## The exchange

```json
<<< {"type":"tool_execution_start","toolCallId":"768363200","toolName":"bash",
     "args":{"command":"rm -rf scratch.txt"}}
<<< {"type":"extension_ui_request","id":"22ae4ead-...","method":"select",
     "title":"⚠️ Dangerous command:\n\n  rm -rf scratch.txt\n\nAllow?","options":["Yes","No"]}
>>> {"type":"extension_ui_response","id":"22ae4ead-...","value":"Yes"}
<<< {"type":"tool_execution_end","toolCallId":"768363200","toolName":"bash",
     "result":{"content":[{"type":"text","text":"(no output)"}]},"isError":false}
```

Denial replaces the last frame with:

```json
<<< {"type":"tool_execution_end","toolCallId":"519156158","toolName":"bash",
     "result":{"content":[{"type":"text","text":"Blocked by user"}],"details":{}},"isError":true}
```

## Three traps for the Event model

1. **`tool_execution_start` fires before the gate resolves.** The stream announces the tool as started
   while it is still waiting for a decision. A start event is not evidence that anything ran.
2. **The UI request carries no `toolCallId`.** It has only `id`, `method`, `title` and `options` — and
   the command appears only inside `title`, a human-facing display string with emoji and newlines in
   it. Correlating an approval to its tool call needs ordering, not a key. Worse than Hermes' C8, which
   at least supplies a structured `rawInput`.
3. **A refusal is reported as a tool error**, not as a distinct event kind: `isError: true` with the
   reason as free text. The string (`"Blocked by user"`) is chosen by the extension author, not by the
   protocol, so it cannot be matched on.

## Caveats

- The gate is an **example extension**, not a Pi feature. Pi gates nothing on its own (P6). This shows
  the mechanism is reachable and what it looks like, not that any deployment has it.
- `permission-gate.ts` blocks by default when `ctx.hasUI` is false — an extension-author choice, not a
  Pi guarantee. Other extensions may fail open.
- One model (`qwen/qwen3.5-9b`); the assistant's prose around the tool call varies per run.
- `ctx.ui.confirm` itself was not exercised — this gate uses `ctx.ui.select`. The documented `confirm`
  response shape (`confirmed: true/false`) is handled by the capture script but untested here.
