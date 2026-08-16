# Hermes ACP captures on Linux — 2026-08-16

Four runs of `scripts/acp-capture.py` against Hermes under **WSL2 Ubuntu**, driving LM Studio on the
Windows host. Run to test whether **C7** (the stdin deadlock) and **C9** (the phantom denial) are
Windows-specific. Neither reproduces.

Evidence for the platform tables in **C7** and **C9**, and the whole of **C12**, in
[`../../harness-control-surfaces.md`](../../harness-control-surfaces.md).

## Like-for-like

Same Hermes **v0.19.0 (2026.7.20), upstream `71e7eb3c`**, same `agent_client_protocol` **0.9.0**, same
capture script, same model (`qwen/qwen3.5-9b`) on the same LM Studio instance. Installed from the
Windows source tree into a Linux venv (`pip install -e '.[acp]'`), so the code under test is identical.

| file | prompt | result |
| --- | --- | --- |
| `linux-tool-*` | terminal `ls -1 \| wc -l` | **C7 disproved.** Tool finished in **1.73s** against a 120s timeout (Windows: 118.83s). `stopReason: end_turn`. `tool_call_update` captured. |
| `linux-edit-*` | read, then patch | **C9 disproved.** Real `session/request_permission`, edit applied. |
| `linux-edit-2-*` | read, then patch | 12 tool calls, 4 approval requests, all real. |
| `linux-edit-3-*` | read, then patch | Real approval, edit applied. |

The three `linux-edit-*` runs all put `patch` after another tool — the exact condition that produced
3/3 phantom denials on Windows. Result on Linux: **0/3**.

## What the runs found that the Windows ones could not

`tool_call_update` arrives, its `toolCallId` matches its `tool_call`, and its `content` carries the
result as **prose** (`"terminal result\n- **output:** 2\n- **exit_code:** 0"`) rather than structured
fields.

More importantly, count them per run:

```
linux-tool     tool_call= 1  tool_call_update= 1  never completed= 0
linux-edit     tool_call= 2  tool_call_update= 0  never completed= 2
linux-edit-3   tool_call= 2  tool_call_update= 0  never completed= 2
linux-edit-2   tool_call=12  tool_call_update= 3  never completed= 9
```

The split is exactly by `kind`: `execute` completes, `read` and `edit` never do. **That gap is not a
Windows artefact** and is the live constraint C12 records.

## Caveats

- **WSL2, not bare metal.** A different kernel and a different pipe implementation from a native Linux
  host, though both are POSIX and neither inherits the handle the way Win32 does.
- The Vendor was reached over the WSL host IP (`172.25.112.1`), which required LM Studio to bind
  `0.0.0.0` for the duration. It has been reverted to `127.0.0.1`; verified afterwards that WSL gets no
  response and Windows loopback still returns 200.
- `qwen/qwen3.5-9b` chose tools erratically — one run made 12 calls for a one-line edit, and one
  produced a mangled `2|BETA` line. That is model quality, not Hermes behaviour, and it is why the
  approval counts vary between runs.
- Three runs is a smaller sample than the eight behind C7's Windows table. The effect size (1.73s vs
  118.83s) is what carries the conclusion, not the count.
