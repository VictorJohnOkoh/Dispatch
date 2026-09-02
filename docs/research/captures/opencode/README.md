# OpenCode ACP capture — 2026-08-27

Frozen bytes behind [`../../opencode-acp-host.md`](../../opencode-acp-host.md)
and the answer on issue #16.

Produced by `scripts/capture-opencode-host.sh`, driving `opencode acp` on the
Host `ZenitoBurrito` over SSH from a separate Client. `scripts/acp-capture.py`
was reused unchanged, so these transcripts stay comparable with the Hermes ones.

One directory per Vendor. All three pass all three gates.

| directory | Vendor | Model | gates |
| --- | --- | --- | --- |
| `ollama/` | Ollama, `127.0.0.1:11434` | `qwen3.5:9b` | 1, 2, 3 pass |
| `lmstudio/` | LM Studio, `127.0.0.1:1234` | `qwen/qwen3.5-9b` | 1, 2, 3 pass |
| `llamaswap/` | llama-swap, `127.0.0.1:8080` | `qwen3.5-9b` | 1, 2, 3 pass |

Inside each:

| file | what it is |
| --- | --- |
| `manifest.txt` | the run's own record: Host, Client, Vendor, Model, versions |
| `<label>-frames.jsonl` | every JSON-RPC frame, both directions, in order |
| `<label>-raw.log` | the agent's raw stdio |
| `<label>-stderr.log` | the agent's stderr — empty in every run |
| `<label>-summary.json` | stop reason, exit code, timings |
| `gates.json` | the three gates, counted by `scripts/opencode-gates.py` |
| `harness-resolution.json` | every spawn attempted, and which ones a supervisor can use |
| `config-discovery.txt` | `opencode models` run from the Session's working directory |
| `session-opencode.json` | the per-Session config written beside the working directory |
| `workdir-after.txt` | what the Session left on the Host's disk |

Labels are `read`, `edit` and `execute` — one run per tool class, so the counts
cannot be confused with each other.

## Provenance

Recorded so nothing downstream reads more into these files than they hold.

- `ollama/` and `llamaswap/` are complete, exactly as the Client landed them.
- **`lmstudio/` is missing `manifest.txt` and `harness-resolution.json`.** Those
  two are written on the Client, and this capture was recovered from the Host's
  own `out/` directory instead. Its `config-discovery.txt` is a re-probe of the
  same working directory after the run, not the file the run wrote. The frames,
  logs and summaries are the run's own bytes.
- Each run overwrote the last, because every run landed in this one directory.
  `ollama/` was recovered from `7db708f`; `llamaswap/` is what `f25cb06` held.
  The script now lands under `<vendor>/`, so a re-run cannot do this again.

## Caveats

- **One Model per Vendor**, and **one run per class**, not the 12/12 that
  produced the Hermes findings.
- **Every permission request was answered with allow.** No refusal was captured,
  so these bytes say nothing about what `reject` does. Issue #46 settles that.
  `bash scripts/capture-opencode-host.sh --reject` answers one edit with
  `reject_once` and lands the result in `../opencode-reject/<vendor>/`, on its own
  so a refused Tool Call cannot disturb the counts above.
- **`webfetch` was never exercised**, though OpenCode's permission block gates it.

## The run that failed first

Two earlier attempts reported that nothing named `opencode` could be spawned on
the Host. That was wrong, and it was not OpenCode. Both `scp` calls were copying
to the absolute path Git Bash's `pwd` returns on the Host, `/c/Users/Victor/...`,
which the Windows SFTP server does not understand. Nothing arrived, neither copy
said so, and the resolver then ran in a directory that held no resolver.

Under ADR 0003 that message reads as gate 1 failing, which is fatal. A failed
copy was one step from condemning the Harness. Fixed in `1b79117`, which also
makes a copy failure loud and separates "the resolver did not run" from "the
Harness cannot spawn". Every capture here is from after that fix.
