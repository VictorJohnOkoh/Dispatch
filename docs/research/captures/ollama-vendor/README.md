# Ollama Vendor captures — 2026-08-31

Eight bodies from a real Ollama **v0.33.2** on Windows, answering on loopback at
`http://127.0.0.1:11434`. Fetched with `curl` from the Host itself, which is the
only place the Daemon ever reaches a Vendor.

**These close finding R8** in
[`../../remote-host-findings.md`](../../remote-host-findings.md). The three earlier
passes recorded `HTTP 200` for every Vendor model listing and wrote none of them,
so the tier-two tests for `vendors` had no bodies to replay. They have them now.

| file | endpoint | what it holds |
| --- | --- | --- |
| `api-version.json` | `GET /api/version` | `0.33.2`, the version every other row was recorded against |
| `api-tags.json` | `GET /api/tags` | the two Models this Ollama serves, with `capabilities`, `details.context_length`, `details.quantization_level` and `size` |
| `v1-models.json` | `GET /v1/models` | the same two Models on the OpenAI-compatible surface: an `id` and nothing else. Kept as the evidence for why discovery does not use it |
| `api-ps.json` | `GET /api/ps` | `qwen3:latest` resident, with `size_vram` and the `context_length` it was started with |
| `api-ps-empty.json` | `GET /api/ps` | nothing resident. Empty and unreachable are different answers, and this is the empty one |
| `api-chat-load-ok.txt` | `POST /api/chat` | an empty chat with `keep_alive: -1`. `done_reason: "load"`, HTTP 200 |
| `api-chat-unload-ok.txt` | `POST /api/chat` | the same call with `keep_alive: 0`. `done_reason: "unload"`, HTTP 200 |
| `api-chat-load-missing.txt` | `POST /api/chat` | the same call naming a Model this Ollama does not have. `{"error":"model 'no-such-model:v9' not found"}`, HTTP 404 |

The `.txt` files carry the status line under the body, because for the load path
the status is half the answer. The `.json` files are bodies only.

The bodies the tests replay are copied to
[`../../../../internal/vendors/testdata/ollama/`](../../../../internal/vendors/testdata/ollama/),
so `internal/vendors` reads its own `testdata` and no test in that package opens a
socket.

`ollama_live_test.go` writes no files. It drives the same four calls against a real
Ollama and skips unless `DISPATCH_OLLAMA_LIVE` is set, so a human can check that a
newer Ollama still answers the way these bodies say before re-running the `curl`
commands above.

## What `/api/tags` settles

`SPEC.md` says Ollama answers `Unknown` for every Capability because `/v1/models`
carries no capability field. `v1-models.json` confirms that about `/v1/models`.
`api-tags.json` shows the native endpoint carries the answer:

```json
"details": { "quantization_level": "Q4_K_M", "context_length": 262144 },
"capabilities": ["vision", "completion", "tools", "thinking"]
```

This is ADR 0007's own matrix row — *Ollama ≥ v0.30.2, `capabilities` and
`details.context_length` on `/api/tags`, every field answered* — and
`vendor-discovery-apis.md` reached it from the Go source before this capture
reached it from a running server.

So the adapter reads `/api/tags` and reports a listed capability as `Yes`. What it
never reports is `No`: a capability Ollama did not list stays `Unknown`, because an
Ollama below v0.30.2 lists none at all and reading that absence as `No` would hide
every Model on the Host. That is the mistake the three values exist to prevent, and
it is intact.

**Two lines in `SPEC.md` were wrong and this capture corrected them**: the three
Vendors section, which said Ollama answers `Unknown` for everything, and behaviour
11, which repeated it. Both now say what `api-tags.json` shows. The behaviours
themselves did not change, only the claim about which Vendor supplies which value.
