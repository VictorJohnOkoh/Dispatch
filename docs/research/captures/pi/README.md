# Pi capture — 2026-08-15

One run of `scripts/capture-pi.sh` against Pi driving LM Studio (`qwen/qwen3.5-9b`) on Windows.
Evidence for corrections **P1**–**P6** in
[`../../harness-control-surfaces.md`](../../harness-control-surfaces.md).

| file | what it shows |
| --- | --- |
| `plain-events.jsonl` | 50 events, `--mode json`, no tools. Full lifecycle `session` → `agent_settled`. |
| `tool-events.jsonl` | 122 events, `--mode json`, one `bash` call. **The complete tool lifecycle (P4).** |
| `rpc-events.jsonl` | **A failed run, kept deliberately** — see below. |
| `rpc-fixed-events.jsonl` | 53 events. The corrected RPC run: `{"id":"req-1","success":true}` through `agent_settled`. |
| `rpc-fixed-tool-events.jsonl` | 173 events. A tool prompt over RPC — two `bash` calls, full lifecycle each. |
| `sessions/` | Three session files, one per invocation, JSONL. |
| `baseline-stdout.txt` | `pi -p` output. Note the two leading blank lines before `hello`. |

## The RPC file is a failure, on purpose

`rpc-events.jsonl` contains one line:

```json
{"type":"response","command":"prompt","success":false,
 "error":"Cannot read properties of undefined (reading 'startsWith')"}
```

Two wizard bugs, both since fixed in `scripts/capture-pi.sh`, both worth keeping visible because the
failure mode is instructive:

1. The command field is **`message`**, not `prompt`. The wrong field produces an undefined-property
   crash rather than a schema error, so the response does not tell you what the right shape was.
2. `cat cmds.jsonl | pi --mode rpc` closes stdin, and Pi exits mid-turn. Verified separately:
   **5 events** that way versus **54 through `agent_settled`** with stdin held open (P3).

The `rpc-fixed-*` files are the corrected runs, driven by hand with the patched wizard's exact command
form. They settle the two questions stage 7 exists to answer:

- **`-e` extension loading works in RPC mode** — the provider registered and the turn completed.
- **`agent_settled` is present in RPC**, as it is in `--mode json`.

and add two more:

- The optional `id` is **echoed back on the response** (`{"id":"req-1","type":"response",
  "command":"prompt","success":true}`), which is the correlation handle a supervisor needs.
- **The tool lifecycle is identical over RPC** — `rpc-fixed-tool-events.jsonl` has two `bash` calls,
  each with `tool_execution_start` / `_update` / `_end`. RPC is a superset of `--mode json`:
  bidirectional commands plus the same event stream.

A second full wizard run (`pi-capture-20260815-231334`) reproduced the `--mode json` vocabulary exactly
— same event kinds, only the `message_update` chunk counts differ — and stopped before stage 7, so it
added nothing these files do not already show. Not copied in.

## Caveats

- One model only (`qwen/qwen3.5-9b`), one provider. `thinkingSignature: "reasoning_content"` is
  LM Studio's field name and is unlikely to generalise.
- The prompts are trivial by design; `ls -la` would not trip any dangerous-pattern gate even if Pi had
  one, so P6's "nothing is gated" rests on Pi having no gate at all rather than on this prompt.
- `curl-errors.log` holds two `curl: (23)` failures from the LM Studio probe, and `manifest.txt`
  records `HTTP 200000` — both cosmetic wizard bugs on Windows, not Pi behaviour. The probe succeeded.
- `apiKey: "lm-studio"` in `lmstudio-provider.ts` is LM Studio's placeholder, not a credential.
- `workdir/` stripped; it held one `sample.txt`.
