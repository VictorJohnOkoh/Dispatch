# Pi across three Vendors

Produced 2026-08-25 by `scripts/capture-pi.sh`, one full pass per Vendor, all on
the Host (`ZenitoBurrito`, Windows 11, Git Bash). Same wizard, same prompts, same
model family — only the Vendor changes.

| Vendor | Endpoint | Model | Stages |
| --- | --- | --- | --- |
| LM Studio | `http://127.0.0.1:1234` | `qwen/qwen3.5-9b` | all 0 |
| Ollama | `http://127.0.0.1:11434` | `qwen3.5:9b` | all 0 |
| llama.cpp | `http://127.0.0.1:8080` | `qwen3.5-9b` | all 0 |

Every Vendor is reached the same way: a provider in `~/.pi/agent/models.json`.
No extension, no `--base-url`. Every run used `pi -ne`, so no auto-loaded
extension was in the process.

Each directory holds the raw streams, not summaries:

- `plain-events.jsonl` — `--mode json`, no tools
- `tool-events.jsonl` — `--mode json`, forced tool call
- `rpc-events.jsonl` — `--mode rpc`, bidirectional
- `sessions/` — Pi's own session files
- `manifest.txt` — every exit code and HTTP status of the run

## A known gap

None of the three directories holds a `vendor-models.json`, and the llama.cpp
one has no `llamacpp-props.json`, although every manifest records `HTTP 200`
for those fetches. curl returned the body and failed to write it — a native
curl handed an MSYS path — and the helper trusted the status line. See R8.

The gap is in the Vendor model listings only. Every event stream, session file
and exit code in these captures is intact.

Read the comparison in [`../../remote-host-findings.md`](../../remote-host-findings.md),
R7 and R8.
