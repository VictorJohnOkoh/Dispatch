# LM Studio Vendor captures — 2026-09-02

Seven bodies from a real **LM Studio 0.4.21** on Windows, answering on loopback at
`http://127.0.0.1:1234`. Fetched with `curl` from the Host itself, which is the
only place the Daemon ever reaches a Vendor.

These extend the close of finding R8 in
[`../../remote-host-findings.md`](../../remote-host-findings.md) to the second
Vendor. [`../ollama-vendor/`](../ollama-vendor/) closed it for the first.

| file | endpoint | what it holds |
| --- | --- | --- |
| `api-v1-models.json` | `GET /api/v1/models` | seven Models, one of them with a `loaded_instances` entry. The whole of discovery on this Vendor |
| `api-v1-models-none-loaded.json` | `GET /api/v1/models` | the same Models with every `loaded_instances` empty. Nothing resident and nobody answering are different answers, and this is the empty one |
| `api-v1-models-two-instances.json` | `GET /api/v1/models` | one Model loaded twice, as `qwen2.5-coder-1.5b-instruct` and `qwen2.5-coder-1.5b-instruct:2`. The Daemon never causes this and must still read it |
| `api-v1-models-load-ok.txt` | `POST /api/v1/models/load` | `{"model": "..."}` and nothing else. `status: "loaded"`, HTTP 200 |
| `api-v1-models-load-missing.txt` | `POST /api/v1/models/load` | the same call naming a Model this LM Studio has not downloaded. `model_not_found`, HTTP 404 |
| `api-v1-models-unload-ok.txt` | `POST /api/v1/models/unload` | `{"instance_id": "..."}`, echoed back. HTTP 200 |
| `api-v1-models-unload-missing.txt` | `POST /api/v1/models/unload` | the same call naming an instance that is not loaded. `model_not_found`, HTTP 404 |

The `.txt` files carry the status line under the body, because for the load and
unload paths the status is half the answer. The `.json` files are bodies only.

The bodies the tests replay are copied to
[`../../../../internal/vendors/testdata/lmstudio/`](../../../../internal/vendors/testdata/lmstudio/),
so `internal/vendors` reads its own `testdata` and no test in that package opens a
socket.

`lmstudio_live_test.go` writes no files. It drives the same calls against a real
LM Studio and skips unless `DISPATCH_LMSTUDIO_LIVE` is set, so a human can check
that a newer LM Studio still answers the way these bodies say before re-running
the `curl` commands above.

## What `/api/v1/models` settles

This is the Vendor that fills `Yes`, and it is also the only one that fills `No`:

```json
"type": "llm",
"capabilities": { "vision": true, "trained_for_tool_use": true,
                  "reasoning": { "allowed_options": ["off", "on"], "default": "on" } }
```

Three readings, and the third is the one that would be easy to get wrong.

- A boolean LM Studio sent is `Yes` or `No`. `qwen2.5-coder-1.5b-instruct` reports
  `trained_for_tool_use: false`, and `No` is the right answer: it hides the Model
  from an agent Session's picker rather than showing it with a gap.
- `type` answers `Chat`. `text-embedding-nomic-embed-text-v1.5` is an `embedding`
  and cannot back a Session at all.
- **A key LM Studio did not send is `Unknown` and never `No`.** `gpt-oss-20b`
  reasons and carries no `reasoning` key, and an LM Studio below 0.4.0 serves
  `/api/v0/models` with no `capabilities` object at all. This is the same rule the
  Ollama capture is the other half of.

## What the two-instance body settles

LM Studio counts instances, not Models. A second load of the same Model answers
with `instance_id: "qwen2.5-coder-1.5b-instruct:2"` and the listing then carries
two entries under one `key`. `Resident` repeats the Model id, and one `Unload`
unloads every instance of it, because the Daemon's intent is that the VRAM comes
back rather than that one instance ends.

## Two things this Vendor does not report

No memory figure of any kind reaches HTTP, so `Resident.VRAM` is zero here and
Ollama stays the only Vendor that fills it. There is no version endpoint either:
`GET /api/v1/models` is the liveness check, which is what "a Vendor is reachable
exactly when a call to it succeeds" means on this Vendor. The 0.4.21 above is read
from the application, not from the wire.
