# OpenCode ACP capture — 2026-08-27

Frozen bytes behind [`../../opencode-acp-host.md`](../../opencode-acp-host.md)
and the answer on issue #16.

Produced by `scripts/capture-opencode-host.sh`, driving `opencode acp` on the
Host `ZenitoBurrito` over SSH from a separate Client. `scripts/acp-capture.py`
was reused unchanged, so these transcripts stay comparable with the Hermes ones.

| file | what it is |
| --- | --- |
| `manifest.txt` | the run's own record: Host, Client, Vendor, Model, versions |
| `<label>-frames.jsonl` | every JSON-RPC frame, both directions, in order |
| `<label>-raw.log` | the agent's raw stdio |
| `<label>-stderr.log` | the agent's stderr — empty in all three runs |
| `<label>-summary.json` | stop reason, exit code, timings |
| `gates.json` | the three gates, counted by `scripts/opencode-gates.py` |
| `harness-resolution.json` | every spawn attempted, and which ones a supervisor can use |
| `config-discovery.txt` | `opencode models` run from the Session's working directory |
| `session-opencode.json` | the per-Session config written beside the working directory |
| `workdir-after.txt` | what the Session left on the Host's disk |

Labels are `read`, `edit` and `execute` — one run per tool class, so the counts
cannot be confused with each other.

## Caveats

Recorded here so nothing downstream reads more into these files than they hold.

- **One Vendor.** Ollama only. LM Studio was serving and was not driven;
  llama-swap was not serving at capture time. Vendor coverage is recorded, not
  gated (ADR 0003).
- **One Model**, `qwen3.5:9b`, and **one run per class**. Counts are 1, 1 and 2,
  not the 12/12 that produced the Hermes findings.
- **Every permission request was answered with allow.** No refusal was captured,
  so these bytes say nothing about what `reject` does.
- **`webfetch` was never exercised**, though OpenCode's permission block gates it.
- The frames were taken from the Host's own `out/` directory. The Client's landed
  copy is the same bytes, scp'd from there.

## The run that failed first

Two earlier attempts reported that nothing named `opencode` could be spawned on
the Host. That was wrong, and it was not OpenCode. Both `scp` calls were copying
to the absolute path Git Bash's `pwd` returns on the Host, `/c/Users/Victor/...`,
which the Windows SFTP server does not understand. Nothing arrived, neither copy
said so, and the resolver then ran in a directory that held no resolver.

Under ADR 0003 that message reads as gate 1 failing, which is fatal. A failed
copy was one step from condemning the Harness. Fixed in `1b79117`, which also
makes a copy failure loud and separates "the resolver did not run" from "the
Harness cannot spawn". The capture in this directory is from after that fix.
