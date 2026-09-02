# llama-swap Vendor captures — 2026-09-02

Nine bodies from a real **llama-swap v251** fronting **llama.cpp b10622** on
Windows, answering on loopback at `http://127.0.0.1:8080`. Fetched with `curl`
from the Host itself, which is the only place the Daemon ever reaches a Vendor.

These extend the close of finding R8 in
[`../../remote-host-findings.md`](../../remote-host-findings.md) to the third
Vendor, and they are the ones R8 named: the `pi-vendors` pass recorded
`GET http://127.0.0.1:8080/v1/models -> HTTP 200` and wrote no body, so what that
call answers with was still unknown.

| file | endpoint | what it holds |
| --- | --- | --- |
| `v1-models.json` | `GET /v1/models` | the four Models this llama-swap fronts, every one `status.value: "unloaded"` |
| `v1-models-one-loaded.json` | `GET /v1/models` | the same four with `qwen2.5-coder-1.5b` loaded |
| `running.json` | `GET /running` | one llama-server process, `state: "ready"`, with the command line it was started with |
| `running-empty.json` | `GET /running` | nothing running. Empty and unreachable are different answers, and this is the empty one |
| `upstream-props.json` | `GET /upstream/qwen2.5-coder-1.5b/props` | the resident Model's `chat_template_caps`, `modalities`, `model_ftype` and `n_ctx`. Fetching this is also what started it, in 4.3s |
| `upstream-props-missing.txt` | `GET /upstream/no-such-model-v9/props` | `model not found`, HTTP 404 |
| `props-404.txt` | `GET /props` | `no model id could be identified`, HTTP 404. ADR 0002 predicted this and no adapter code path calls it |
| `api-models-unload-ok.txt` | `POST /api/models/unload/qwen2.5-coder-1.5b` | the two bytes `OK`, HTTP 200. Not JSON |
| `api-models-unload-missing.txt` | `POST /api/models/unload/no-such-model-v9` | `model not found`, HTTP 404 |

The `.txt` files carry the status line under the body. The `.json` files are
bodies only.

The bodies the tests replay are copied to
[`../../../../internal/vendors/testdata/llamaswap/`](../../../../internal/vendors/testdata/llamaswap/),
so `internal/vendors` reads its own `testdata` and no test in that package opens a
socket.

`llamaswap_live_test.go` writes no files. It drives the same calls against a real
llama-swap and skips unless `DISPATCH_LLAMASWAP_LIVE` is set.

## What `/v1/models` settles, which is the question ADR 0007 left open

ADR 0007 said "which call returns that set is not established, and this ADR does
not guess". It is `GET /v1/models`, and the body is an OpenAI listing:

```json
{"data":[{"id":"qwen2.5-coder-1.5b","object":"model","owned_by":"llama-swap",
          "status":{"value":"unloaded"}}],"object":"list"}
```

An id, and a `status`. **No size, no quantisation, no context length and no
capability of any kind.** So every Capability llama-swap reports for a Model it has
not loaded is `Unknown`, which is the value this Vendor is in v1 to fill, and the
adapter never asks a Model that is not loaded, because asking is what loads it.

The adapter reads what is loaded from `/running` rather than from this `status`
field, which is the endpoint ADR 0002 named and the one that reports the process
rather than the routing table.

## What `/upstream/<id>/props` settles

For a Model that is already resident the answer sharpens, and it is the most
accurate of the three Vendors while it lasts, because it is computed from the
Jinja template that will actually run:

```json
"model_ftype": "Q8_0",
"modalities": { "vision": false, "video": false, "audio": false },
"chat_template_caps": { "supports_tools": true, "supports_tool_calls": true, ... }
```

Two corrections to what was written before this body existed.

- **llama-swap does report a quantisation**, in `model_ftype`, for a Model it has
  loaded. `vendor-discovery-apis.md` said "no quantisation is reported anywhere
  over HTTP" from the bare `llama-server` sources, and `Model.Quant` carried a
  comment saying llama-swap never reports one. Both were true of the listing and
  wrong about `/props`. The comment now says loaded only.
- **`n_ctx` is a loaded context and not a trained one.** It is the `-c` the
  llama-server was started with, straight out of llama-swap's config, so it can
  never fill `Model.TrainedContext`. llama-swap reports no trained maximum at all.

`chat_template_caps` has no reasoning flag, so `Reasoning` stays `Unknown` on this
Vendor even for a Model it has loaded. Inferring one from `reasoning_format` would
be a guess about the Model dressed as an answer.

## Two shapes that would break a careless reader

An unload answers with the two bytes `OK` rather than with JSON, so nothing may
decode that body. And a bare `GET /props` is HTTP 404 with `no model id could be
identified`, because llama-swap fronts several Models and cannot tell which one is
meant — the per-Model view is the only one that exists here.

## What this capture does not settle

llama-swap's config on this Host carries `ttl: 300` on every Model, and ADR 0002
requires `ttl: 0` so that a second evictor cannot surprise a Session idle between
prompts. That is Host setup rather than something the Daemon calls, and the
`Load` this adapter makes does not change it.
