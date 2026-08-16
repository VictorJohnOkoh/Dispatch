# Hermes ACP edit-path captures — 2026-08-15

Six runs of `scripts/acp-capture.py` against `hermes acp` (v0.19.0, Windows, LM Studio
`gpt-oss-20b`), all asking for the same one-line edit to a three-line `notes.txt`.

These exist to answer two questions the earlier terminal-tool captures
(`../hermes/`) could not: what `session/request_permission` actually looks like, and whether the
deadlock recorded as C7 is specific to the terminal tool. It is not.

Evidence for corrections **C6**, **C7**, **C8** and **C9** in
[`../../harness-control-surfaces.md`](../../harness-control-surfaces.md).

| run | flags | first tool | outcome |
| --- | --- | --- | --- |
| `edit` | none | `patch` | **Approval captured in full.** Deadlocked afterwards on environment setup; file unmodified. |
| `edit2` | `--close-stdin-on-permission` | `search_files` | Deadlock 237.73s/240. Phantom denial ×2. Flag never fired — no approval was ever requested. |
| `edit3` | none | `patch` | Approval granted over the wire, **edit applied**. Deadlock 414.35s/420 — 6s short of capturing `tool_call_update`. |
| `edit4` | `--close-stdin-on-permission` | `search_files` | Deadlock 294.21s/300. Phantom denial ×1. |
| `edit5` | none, `--timeout 900` | `patch` | Deadlock 898.43s/900; agent's next API call landed **2s after** the client gave up. Proof that the hang tracks the timeout rather than a fixed cost. |
| `edit6` | `--close-stdin-on-permission` | `patch` | **The decisive run.** Same prompt as `edit5`; `patch` took **3.08s**. Turn completed internally (`finish_reason=stop`) but no frame reached the client after the approval response. |

**`edit5` and `edit6` together are the impossibility proof for `tool_call_update`** (C7): 898.43s vs
3.08s for the identical tool isolates the inherited stdin pipe as the cause, and `edit6` shows that the
fix which releases the deadlock also silences the connection.

`edit` has no `-summary.json`: the run was killed by the harness timeout during teardown, before the
summary was written. Its `-frames.jsonl` is complete and is the source of the C10 payload.

## Reading these

The approval frame is the point of the whole exercise:

```
python -c "import json;[print(json.dumps(json.loads(l)['frame'],indent=2)) for l in open('edit-frames.jsonl',encoding='utf-8') if 'request_permission' in l]"
```

The deadlock is only visible in stderr, not on the wire — the client sees an ordinary gap between
frames. Compare the `Creating new local environment` timestamp against the `tool ... completed`
one:

```
grep -E "Creating new|completed \(|returned error|API call" edit3-stderr.log
```

## Caveats

- **The model chose different tools across runs** despite near-identical prompts, which is why
  `patch` is first in only two of four. That nondeterminism is itself the reason C9's ordering
  correlation took four runs to see.
- `agent_thought_chunk` dominates the frame counts (64–215 per run) because `gpt-oss-20b` emits
  heavy reasoning. Not representative of other models.
- Absolute paths point into a scratch directory that no longer exists. Session ids are per-run.
- No credentials are present; the one auth line in `edit3-stderr.log` records that a JWT was used,
  not its value.
