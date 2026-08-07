# Vendor discovery, capability and health APIs

Research for issue `02-vendor-discovery-apis`. Establishes, from primary sources, what
Ollama, LM Studio and `llama-server` expose for **model discovery, capability metadata,
lifecycle, health and resource visibility** — i.e. everything the Vendor abstraction covers
that is *not* inference — plus where each departs from the OpenAI wire format in ways that
leak into a passthrough Harness.

## Versions this was researched against

APIs in this space move fast. Everything below was checked on **2026-08-07** against:

| Vendor | Version researched | Source of truth |
| --- | --- | --- |
| Ollama | **v0.32.6** (released 2026-08-04); docs and Go source read from `main` | [github.com/ollama/ollama](https://github.com/ollama/ollama), [docs/openapi.yaml](https://github.com/ollama/ollama/blob/main/docs/openapi.yaml) |
| LM Studio | **0.4.1** (latest entry in the API changelog). Native `v1` REST API since **0.4.0**; `v0` REST API since **0.3.6** | [lmstudio.ai/docs/developer](https://lmstudio.ai/docs/developer), [github.com/lmstudio-ai/docs](https://github.com/lmstudio-ai/docs) |
| llama.cpp `llama-server` | **b10306** (released 2026-08-07) | [tools/server/README.md](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md) and the server C++ sources |

Version-dependent facts are flagged inline with **[version]**.

A note on primacy: for Ollama I read the Go source as well as the docs, because the docs
lag the code in two places that matter (capabilities on `/api/tags`, and the error shape
emitted mid-stream). For LM Studio the `lmstudio-ai/docs` repo is the doc source of truth;
the app itself is closed source, so a few behaviours are only knowable from the docs, the
changelog or the bug tracker.

---

## Comparison table — questions 1 to 5

| | **Ollama** | **LM Studio** | **llama.cpp `llama-server`** |
| --- | --- | --- | --- |
| **1. List installed models** | `GET /api/tags` | `GET /api/v1/models` (also legacy `GET /api/v0/models`) | `GET /models` — **router mode only** (no `-m` flag) |
| **1. List loaded models** | `GET /api/ps` | same call — `loaded_instances[]` per model | `GET /models` `status.value` |
| **1. Installed vs loaded distinguishable?** | Yes, two endpoints | Yes, one endpoint, nested array | Yes (router mode); single-model mode has exactly one model, always loaded |
| **2. Context length** | `details.context_length` on `/api/tags` **[version ≥ v0.30.x]**; `model_info["<arch>.context_length"]` from `/api/show`; *loaded* ctx from `/api/ps` `context_length` | `max_context_length` (trained max) and `loaded_instances[].config.context_length` (actual) | `meta.n_ctx_train` from `/v1/models`; actual `n_ctx` from `/props` and `/slots` |
| **2. Tool-calling support** | `capabilities` contains `"tools"` | `capabilities.trained_for_tool_use` (boolean) | `chat_template_caps.supports_tools` / `supports_tool_calls` on `/props` |
| **2. Multimodal** | `capabilities` contains `"vision"` / `"audio"` / `"image"` | `capabilities.vision`; `type` is `"llm"`/`"embedding"` (v0 also `"vlm"`) | `modalities.vision` on `/props`; `architecture.input_modalities` on router `/models` |
| **2. Reasoning / thinking** | `capabilities` contains `"thinking"` | `capabilities.reasoning.allowed_options` + `.default` | inferred from `chat_template_caps` / `--reasoning-format`; no explicit flag |
| **2. Quantisation** | `details.quantization_level` (e.g. `Q4_K_M`) | `quantization.name` + `quantization.bits_per_weight` | not reported; only `meta.size` in bytes |
| **2. Size / params** | `size` (bytes), `details.parameter_size` (`"8.0B"`) | `size_bytes`, `params_string` (`"26B-A4B"`) | `meta.n_params`, `meta.size` |
| **3. Explicit load** | Implicit only — empty `/api/generate` or `/api/chat` preloads | `POST /api/v1/models/load` (first-class, with config) | `POST /models/load` (router mode) |
| **3. Explicit unload** | `keep_alive: 0` on an empty request | `POST /api/v1/models/unload` `{instance_id}` | `POST /models/unload` (router mode) |
| **3. Keep-alive / TTL** | `keep_alive` per request, default `5m`; `OLLAMA_KEEP_ALIVE`; `-1` = forever | `ttl` (seconds) per request, JIT default 60 min; Auto-Evict keeps ≤ 1 JIT model | `--sleep-idle-seconds` puts the model to sleep (unloads weights + KV cache) |
| **3. Load implicit on first request?** | Yes, always | Yes, when JIT loading is on (default) | Yes in router mode unless `--no-models-autoload` |
| **3. Load progress observable?** | No. `load_duration` reported *after the fact* | **Yes** — `model_load.start` / `.progress` (0–1 float) / `.end` SSE events on `POST /api/v1/chat` | Partially — `/models` `status.value == "loading"`; `/health` returns 503 while loading |
| **4. Health endpoint** | None dedicated. `GET /` → `"Ollama is running"`; `GET /api/version` | None dedicated; `GET /api/v1/models` serves as liveness | `GET /health` (and `/v1/health`), public, no API key |
| **4. Idle vs loading vs busy** | idle/loaded via `/api/ps`; **busy is not exposed**; loading is not exposed (request just blocks) | loaded via `loaded_instances`; busy only via `lms ps --json` (CLI, not HTTP); loading via native chat stream events | all three: `/health` 503 = loading; `/props` `is_sleeping`; `/slots` `is_processing`; `/slots?fail_on_no_slot=1` → 503 when saturated; `llamacpp:requests_processing` gauge |
| **5. VRAM / memory** | **`size_vram` (bytes) on `/api/ps`** — compare against `size` to detect partial GPU offload | Nothing over HTTP. `lms load --estimate-only` (CLI) gives pre-load estimates **[0.3.27+]** | No memory figures. `/metrics` (Prometheus, needs `--metrics`) reports throughput/queue only |
| **Default port** | `11434` | `1234` | `8080` |
| **Default bind** | `127.0.0.1` | `127.0.0.1` | `127.0.0.1` |

---

## 1. Model enumeration

### Ollama

Two endpoints, cleanly split. Source: [docs/api.md](https://github.com/ollama/ollama/blob/main/docs/api.md), [docs/openapi.yaml](https://github.com/ollama/ollama/blob/main/docs/openapi.yaml), and `api/types.go`.

`GET /api/tags` — everything pulled locally:

```json
{
  "models": [
    {
      "name": "gemma4",
      "model": "gemma4",
      "modified_at": "2025-10-03T23:34:03.409490317-07:00",
      "size": 9608350245,
      "digest": "c6eb396dbd5992bbe3f5cdb947e8bbc0ee413d7c17e2beaae69f5d569cf982eb",
      "details": {
        "format": "gguf",
        "family": "gemma4",
        "families": ["gemma4"],
        "parameter_size": "8.0B",
        "quantization_level": "Q4_K_M"
      }
    }
  ]
}
```

**[version]** The published example above understates what current Ollama returns. The Go
type backing it —
[`api.ListModelResponse`](https://github.com/ollama/ollama/blob/main/api/types.go) — also
carries `capabilities []model.Capability`, and `ModelDetails` carries `context_length` and
`embedding_length`. Verified by diffing the struct across tags: **absent in v0.24.0,
present from v0.30.2 onward** (the `modelListCache` landed 2026-05-19 in
[PR #16215](https://github.com/ollama/ollama/pull/16215), backing
[`server/model_list_cache.go`](https://github.com/ollama/ollama/blob/main/server/model_list_cache.go),
which populates capabilities and context length by reading GGUF metadata at list time).
Practical consequence: **on Ollama ≥ v0.30.2 a single `/api/tags` call is enough to build
an honest model picker**; below that you must fan out to `/api/show` per model.

`GET /api/ps` — currently resident in memory:

```json
{
  "models": [
    {
      "name": "gemma4",
      "model": "gemma4",
      "size": 6591830464,
      "digest": "c6eb396d…",
      "details": { "format": "gguf", "family": "gemma4", "parameter_size": "8.0B", "quantization_level": "Q4_K_M" },
      "expires_at": "2025-10-17T16:47:07.93355-07:00",
      "size_vram": 5333539264,
      "context_length": 4096
    }
  ]
}
```

`context_length` here is the *actual* context the runner was loaded with, not the model's
trained maximum — a different number from `details.context_length` on `/api/tags`.

### LM Studio

One endpoint covers both, because loaded instances are nested under the model.
Source: [rest/list.md](https://github.com/lmstudio-ai/docs/blob/main/1_developer/2_rest/list.md).

`GET /api/v1/models`:

```json
{
  "models": [
    {
      "type": "llm",
      "publisher": "google",
      "key": "google/gemma-4-26b-a4b",
      "display_name": "Gemma 4 26B A4B",
      "architecture": "gemma4",
      "quantization": { "name": "Q4_K_M", "bits_per_weight": 4 },
      "size_bytes": 17990911801,
      "params_string": "26B-A4B",
      "loaded_instances": [
        {
          "id": "google/gemma-4-26b-a4b",
          "config": {
            "context_length": 4096,
            "eval_batch_size": 512,
            "parallel": 4,
            "flash_attention": true,
            "num_experts": 8,
            "offload_kv_cache_to_gpu": true
          }
        }
      ],
      "max_context_length": 262144,
      "format": "gguf",
      "capabilities": {
        "vision": true,
        "trained_for_tool_use": true,
        "reasoning": { "allowed_options": ["off", "on"], "default": "on" }
      },
      "description": null,
      "variants": ["google/gemma-4-26b-a4b@q4_k_m"],
      "selected_variant": "google/gemma-4-26b-a4b@q4_k_m"
    }
  ]
}
```

`loaded_instances` empty ⇒ downloaded but not loaded. Note the **instance** concept: the
same model can be loaded more than once, each with its own `id`, which is what
`/api/v1/models/unload` takes.

**[version]** The legacy `GET /api/v0/models`
([endpoints.mdx](https://github.com/lmstudio-ai/docs/blob/main/1_developer/2_rest/endpoints.mdx),
LM Studio ≥ 0.3.6) is a flatter, OpenAI-list-shaped variant with `state: "not-loaded"`,
`arch`, `compatibility_type`, `quantization` as a bare string, and `max_context_length` —
but **no `capabilities` object**. It is still served but the docs recommend v1. If we need
to support LM Studio < 0.4.0 we lose the capability flags entirely.

### llama.cpp

Single-model mode (the classic `llama-server -m model.gguf`) has no enumeration worth the
name: `GET /v1/models` "returns information about the loaded model… The returned list
always has one single element."

```json
{
  "object": "list",
  "data": [{
    "id": "../models/Meta-Llama-3.1-8B-Instruct-Q4_K_M.gguf",
    "object": "model",
    "created": 1735142223,
    "owned_by": "llamacpp",
    "meta": { "vocab_type": 2, "n_vocab": 128256, "n_ctx_train": 131072,
              "n_embd": 4096, "n_params": 8030261312, "size": 4912898304 }
  }]
}
```

`meta` can be `null` while the model is still loading. `id` defaults to the model *file
path* unless `--alias` is given — a nasty identity wart for a cross-host client.

**[version]** *Router mode* (omit `-m`, use `--models-dir` or `--models-preset`) is
recent and changes the picture entirely, giving `GET /models` with real installed-vs-loaded
status:

```json
{ "data": [{
    "id": "ggml-org/gemma-3-4b-it-GGUF:Q4_K_M",
    "path": "/…/gemma-3-4b-it-Q4_K_M.gguf",
    "status": { "value": "loaded", "args": ["llama-server", "-ctx", "4096"] },
    "architecture": { "input_modalities": ["text", "image"], "output_modalities": ["text"] }
}] }
```

`status.value` ∈ `unloaded | loading | loaded | sleeping | downloading | failed`, and a
failed status carries `failed: true` and `exit_code`. There is also `GET /models/sse`, a
Server-Sent-Events stream of model status changes, download progress and load events —
the only push-based discovery channel across all three Vendors.

---

## 2. Capability metadata

This is the question that decides whether the Client's model picker can be honest. Short
answer: **Ollama and LM Studio can be honest; llama.cpp can be honest only about the one
model it has loaded.**

### Ollama

The capability vocabulary is a closed set, defined in
[`types/model/capability.go`](https://github.com/ollama/ollama/blob/main/types/model/capability.go):

```go
CapabilityCompletion = Capability("completion")
CapabilityTools      = Capability("tools")
CapabilityInsert     = Capability("insert")
CapabilityVision     = Capability("vision")
CapabilityEmbedding  = Capability("embedding")
CapabilityThinking   = Capability("thinking")
CapabilityImage      = Capability("image")
CapabilityAudio      = Capability("audio")
```

This is a genuinely useful enum to map a Vendor-neutral capability type onto. It is derived
from GGUF metadata and the model's chat template, not hand-declared.

`POST /api/show` is the deep source:

```bash
curl http://localhost:11434/api/show -d '{"model": "gemma4", "verbose": true}'
```

returns `capabilities`, `details`, `template`, `parameters`, `license`, and a `model_info`
map of raw GGUF keys — `general.architecture`, `general.parameter_count`,
`<arch>.context_length`, `<arch>.block_count`, `tokenizer.ggml.*`, and for multimodal
models `<arch>.vision.*` / `<arch>.audio.*`. Without `verbose: true` the large token arrays
are elided. This is the richest per-model metadata of the three, but it is one call per
model and Ollama maintains a `modelShowCache` precisely because it is expensive.

### LM Studio

Capabilities are a typed object rather than a string set, which is easier to consume but
narrower:

```json
"capabilities": {
  "vision": true,
  "trained_for_tool_use": true,
  "reasoning": { "allowed_options": ["off", "on", "low", "medium", "high"], "default": "on" }
}
```

`capabilities` is absent for embedding models. Note `trained_for_tool_use` is an honest
name: LM Studio will still *accept* `tools` for a model that was not trained for it and
fall back to a text-encoded protocol (see question 6), so the flag describes model quality,
not endpoint acceptance. The `reasoning` object is strictly more expressive than Ollama's
boolean-ish `"thinking"` capability, because it enumerates the allowed effort levels.

Context is reported twice and the distinction matters: `max_context_length` is what the
model supports, `loaded_instances[].config.context_length` is what it was actually loaded
with. A picker that shows the former while the server is running the latter will lie.

### llama.cpp

There is no per-model capability catalogue. What you get is per-*server*:

- `GET /props` → `modalities: { "vision": false }`, `chat_template`, and
  `chat_template_caps`.
- `chat_template_caps` is derived by parsing the Jinja template; the struct is
  [`common/jinja/caps.h`](https://github.com/ggml-org/llama.cpp/blob/master/common/jinja/caps.h):

  ```cpp
  struct caps {
      bool supports_tools = true;
      bool supports_tool_calls = true;
      bool supports_system_role = true;
      bool supports_parallel_tool_calls = true;
      bool supports_preserve_reasoning = false;
      bool supports_string_content = true;
      bool supports_typed_content = false;
      bool supports_object_arguments = false;
  };
  ```

  This is the closest llama.cpp gets to a capability manifest, and it is arguably the most
  *accurate* of the three, because it is computed from the template that will actually be
  used rather than from a metadata label. `supports_system_role` and `supports_typed_content`
  have no equivalent in the other two Vendors and are exactly the sort of thing that breaks
  a passthrough silently.
- No quantisation is reported anywhere over HTTP — only `meta.size` in bytes. If the Client
  wants to show "Q4_K_M" for a llama.cpp Vendor it must parse the filename or accept a gap.
- Router mode adds `architecture.input_modalities` / `output_modalities` per model, which is
  the only multimodal signal available for *unloaded* models.

---

## 3. Lifecycle

### Ollama — implicit only, controlled by `keep_alive`

There is **no load/unload endpoint**. Loading is a side effect of an inference call.
From [docs/api.md](https://github.com/ollama/ollama/blob/main/docs/api.md):

```bash
# preload
curl http://localhost:11434/api/chat -d '{"model": "llama3.2", "messages": []}'
# → {"model":"llama3.2","message":{"role":"assistant","content":""},
#    "done_reason":"load","done":true}

# unload
curl http://localhost:11434/api/chat -d '{"model": "llama3.2", "messages": [], "keep_alive": 0}'
# → {…,"done_reason":"unload","done":true}
```

The `done_reason` values `"load"` and `"unload"` are the observable signal. `keep_alive`
defaults to `5m`, accepts a duration string or seconds, and `-1` means keep loaded
indefinitely ([FAQ](https://github.com/ollama/ollama/blob/main/docs/faq.mdx)). The server
default is `OLLAMA_KEEP_ALIVE`; the per-request field overrides it.

Admission-control-relevant knobs, all from the FAQ:

- `OLLAMA_MAX_LOADED_MODELS` — max concurrently loaded models, default `3 × num GPUs` (or 3 on CPU).
- `OLLAMA_NUM_PARALLEL` — max parallel requests per model, default 1. "Required RAM will
  scale by `OLLAMA_NUM_PARALLEL * OLLAMA_CONTEXT_LENGTH`."
- `OLLAMA_CONTEXT_LENGTH` — server-wide default context.

**None of these are readable over the API** — they are process environment. A Vendor
adapter cannot discover the admission limits of the Ollama it is talking to; it can only
observe `/api/ps` and infer.

Load *duration* is reported only retrospectively, in `load_duration` (nanoseconds) on the
generate/chat response ([`api.Metrics`](https://github.com/ollama/ollama/blob/main/api/types.go)).
While a load is in progress there is no way to see it: the request simply blocks.

### LM Studio — first-class, and the only one with load progress

`POST /api/v1/models/load`
([load.md](https://github.com/lmstudio-ai/docs/blob/main/1_developer/2_rest/load.md)):

```json
{ "model": "openai/gpt-oss-20b", "context_length": 16384,
  "flash_attention": true, "echo_load_config": true }
```

```json
{ "type": "llm", "instance_id": "openai/gpt-oss-20b", "load_time_seconds": 9.099,
  "status": "loaded",
  "load_config": { "context_length": 16384, "eval_batch_size": 512,
                   "flash_attention": true, "offload_kv_cache_to_gpu": true, "num_experts": 4 } }
```

Optional load parameters: `context_length`, `eval_batch_size`, `flash_attention`,
`num_experts`, `offload_kv_cache_to_gpu`, `echo_load_config`. This is materially more
control than Ollama offers over HTTP — Ollama requires a Modelfile rebuild to change
context size for OpenAI-compat clients.

`POST /api/v1/models/unload` takes `{"instance_id": "..."}` and echoes it back.

Idle behaviour
([ttl-and-auto-evict.md](https://github.com/lmstudio-ai/docs/blob/main/1_developer/0_core/ttl-and-auto-evict.md)):

- **JIT loading** (default on): first request to a model loads it.
- **Idle TTL** default **60 minutes** for JIT-loaded models; set per request with a `ttl`
  field (seconds) that works on *both* the OpenAI-compat and native endpoints. The idle
  timer resets on each request.
- Models loaded with `lms load` have **no** TTL by default.
- **Auto-Evict** (default on): at most **1** JIT-loaded model stays resident; loading a new
  one unloads the previous. Non-JIT loads are unaffected.

Auto-Evict is a big deal for admission control: on a default LM Studio, a Client that
switches models is *silently* unloading the previous one.

Load progress is streamed on the native chat endpoint
([streaming-events.md](https://github.com/lmstudio-ai/docs/blob/main/1_developer/2_rest/streaming-events.md)) —
`model_load.start`, `model_load.progress` (`progress` float 0–1), `model_load.end`
(`load_time_seconds`), plus `prompt_processing.progress`:

```json
{ "type": "model_load.progress", "instance_id": "openai/gpt-oss-20b", "progress": 0.65 }
```

Note this is only on `POST /api/v1/chat`, i.e. **outside the OpenAI passthrough**. If the
Harness passes inference through to `/v1/chat/completions`, this telemetry is invisible.

### llama.cpp

Single-model mode: the model is loaded at process start; lifecycle is the process
lifecycle. The one lever is `--sleep-idle-seconds`
([PR #18228](https://github.com/ggml-org/llama.cpp/pull/18228)) which unloads the model and
KV cache from RAM after inactivity and reloads on the next task. `GET /health`, `GET /props`
and `GET /models` are explicitly exempt from resetting the idle timer — so a Vendor
adapter can poll health without pinning a model in memory. That exemption is a
thoughtfully-designed affordance and worth relying on.

Router mode: `POST /models/load` and `POST /models/unload`, both `{"model": "<id>"}` →
`{"success": true}`; `POST /models` starts a non-blocking download; `DELETE /models?model=`
evicts from cache. Autoload on request is default, disabled with `--no-models-autoload` or
per-request `?autoload=true|false`. Preset files support `load-on-startup` and
`stop-timeout`.

---

## 4. Health and readiness

**llama.cpp is the only one with a real health story.**

`GET /health` (and `/v1/health`), public, no API key:

```
200 → {"status": "ok"}
503 → {"error": {"code": 503, "message": "Loading model", "type": "unavailable_error"}}
```

Then:

- `GET /props` → `is_sleeping` (bool), `total_slots`, `build_info` (`b<number>-<commit>` —
  useful for version pinning), `model_path`.
- `GET /slots` (on by default, `--no-slots` to disable) → per-slot `is_processing`,
  `id_task`, `n_ctx`, and the full sampling params of whatever is running.
  **`GET /slots?fail_on_no_slot=1` returns 503 when nothing is free** — a ready-made
  admission-control probe.
- `GET /metrics` (requires `--metrics`) → Prometheus, including
  `llamacpp:requests_processing` and `llamacpp:requests_deferred` gauges. In router mode
  `?model=` is mandatory or you get a 400.

So all three of "running but idle", "loading a model", "busy" are distinguishable.

**Ollama has no health endpoint.** The routes registered in
[`server/routes.go`](https://github.com/ollama/ollama/blob/main/server/routes.go) include:

```go
r.GET("/",            func(c *gin.Context) { c.String(http.StatusOK, "Ollama is running") })
r.HEAD("/",           …)
r.GET("/api/version", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"version": version.Version}) })
r.GET("/api/status",  s.StatusHandler)
```

`HEAD /` is the cheapest liveness probe and `GET /api/version` doubles as a version
handshake. Note **`/api/status` is not a health endpoint** — reading `StatusHandler` shows
it returns only `{"cloud": {"disabled": …, "source": …}}`, i.e. Ollama-cloud availability.
Do not wire readiness to it.

Readiness decomposition on Ollama:

- *running but idle* — `/api/ps` returns `{"models": []}` or entries with future `expires_at`.
- *loading* — **not observable.** The request blocks; no state is exposed.
- *busy* — **not observable.** `ProcessModelResponse` has no processing/queue field.

This is the single biggest gap for an admission-control policy: on Ollama the Harness must
track in-flight requests itself, because the Vendor will not tell it.

**LM Studio has no HTTP health endpoint** either. `GET /api/v1/models` (or v0) is the de
facto liveness probe. Per-model busy state exists but only through the CLI —
`lms ps --json` "now reports each model's generation status and the number of queued
prediction requests" **[0.3.27+]**, per the
[API changelog](https://github.com/lmstudio-ai/docs/blob/main/1_developer/api-changelog.md).
That is not reachable over the network, so for a remote Vendor adapter it does not exist.

---

## 5. Resource visibility

- **Ollama** is the only one that reports memory over HTTP: `/api/ps` gives `size_vram`
  alongside `size` (both bytes). `size_vram < size` means the model is partially offloaded
  to CPU — a directly actionable signal for placement decisions. Nothing is reported about
  *free* VRAM or total device capacity, so you can only account for what Ollama itself has
  loaded.
- **LM Studio** reports nothing over HTTP. The load response gives `load_time_seconds` but
  no footprint. `lms load --estimate-only <model>` prints estimated GPU and total memory
  before loading, honouring `--context-length` and `--gpu` **[0.3.27+]** — CLI only.
- **llama.cpp** reports no memory at all. `/metrics` is throughput and queue depth;
  `/v1/models` `meta.size` is the file size on disk, not resident memory. `/props`
  `total_slots` and `/slots` `n_ctx` let you compute a KV-cache estimate yourself.

Conclusion for the abstraction: **a Vendor-neutral `MemoryUsage` field would be populated
by exactly one of the three Vendors.** Model it as optional and expect it to be absent.

---

## 6. Divergence from OpenAI compatibility

This is the question that decides whether the passthrough Harness is trivial. It is not
trivial. All three accept `POST /v1/chat/completions` and all three will satisfy a naive
`openai` SDK call, but they diverge in ways that surface exactly when things go wrong or
when tools are involved — i.e. in the cases that matter.

### 6a. What each actually implements

| Endpoint | Ollama v0.32.6 | LM Studio 0.4.x | llama.cpp b10306 |
| --- | --- | --- | --- |
| `/v1/chat/completions` | yes | yes | yes |
| `/v1/completions` | yes | yes | yes |
| `/v1/models`, `/v1/models/{model}` | yes | yes | yes (single element) |
| `/v1/embeddings` | yes | yes | yes |
| `/v1/responses` | yes, **non-stateful only** [v0.13.3+] | yes [0.3.29+], *with* `previous_response_id` | yes |
| `/v1/messages` (Anthropic) | yes | yes [0.4.1+] | yes (`--jinja` required for tools) |
| `/v1/audio/transcriptions` | yes | — | — |

Route list from `server/routes.go`; LM Studio from
[openai-compat](https://lmstudio.ai/docs/developer/openai-compat) and the changelog;
llama.cpp from the server README.

### 6b. Streaming format

All three use SSE `data: {json}\n\n` framing and all three terminate with
`data: [DONE]\n\n` (verified in
[`middleware/openai.go`](https://github.com/ollama/ollama/blob/main/middleware/openai.go)
line ~154, and
[`tools/server/server-context.cpp`](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/server-context.cpp)
line ~4308). So far so uniform. The divergences:

**Reasoning/thinking field name is not the same, and none of them match each other.**

| Vendor | Non-streaming | Streaming | Notes |
| --- | --- | --- | --- |
| Ollama | `choices[].message.reasoning` | `choices[].delta.reasoning` | `Message.Reasoning string \`json:"reasoning,omitempty"\`` in `openai/openai.go` |
| LM Studio | `choices.message.reasoning` | `choices.delta.reasoning` | moved out of `content` in **0.3.23**; before that it was inline in `content` |
| llama.cpp | `message.reasoning_content` | `delta.reasoning_content` | "similar to Deepseek API"; controlled by `--reasoning-format none\|deepseek\|deepseek-legacy`, default `auto` |

A passthrough that forwards bytes verbatim will hand the Client three different field
names for the same concept. **This alone justifies a per-Vendor response-shaping hook**, or
an explicit decision that the Client tolerates all three.

**Ollama splits one logical emission into two chunks.** `ToStreamChunks` in
`openai/openai.go`: when a single Ollama response carries *both* thinking and
content/tool-calls, it emits two `chat.completion.chunk` objects sharing one `created`
timestamp — a reasoning-only chunk and a content-or-tool-calls chunk. Clients that assume
one upstream event per chunk, or that dedupe on `created`, will misbehave.

**Ollama emits a dedicated finish chunk with an empty delta** (`FinishChunk`), then
optionally a *further* chunk carrying `usage` with `choices: []` when
`stream_options.include_usage` is set. `system_fingerprint` is the literal string
`"fp_ollama"`.

**llama.cpp sends SSE comment pings.** When no token has been produced for
`sse_ping_interval` seconds it writes `":\n\n"` to keep intermediaries alive
(`server-context.cpp`). This is valid SSE but a hand-rolled line-oriented proxy that
assumes every non-empty line starts with `data: ` will choke on it. Also, llama.cpp adds a
non-OpenAI `timings` object to responses (`cache_n`, `prompt_n`, `prompt_ms`,
`predicted_n`, `predicted_per_second`, …) alongside the standard `usage`, and its
`usage.prompt_tokens_details.cached_tokens` is populated.

**LM Studio adds `stats`, `model_info` and `runtime` objects** on the `/api/v0/*` variants
(`tokens_per_second`, `time_to_first_token`, `stop_reason: "eosFound"`, and the runtime
name/version such as `llama.cpp-mac-arm64-apple-metal-advsimd` `1.3.0`). On `/v1/*` these
are absent — and issue
[#601](https://github.com/lmstudio-ai/lmstudio-bug-tracker/issues/601) reports an empty
`"stats": {}` leaking into `/v1/chat/completions`.

### 6c. Tool-call encoding

**Ollama.** `ToToolCalls` in `openai/openai.go` marshals arguments to a JSON *string*
(OpenAI-correct) and sets `type: "function"`, but note:

```go
toolCalls[i].ID    = tc.ID
toolCalls[i].Index = tc.Function.Index
```

The `ID` is whatever the model produced; there is no `call_…` prefix guarantee. `Index` is
taken from the function index. `finish_reason` is remapped: `FinishChunk` upgrades `"stop"`
to `"tool_calls"` only if a tool call was actually emitted, and passes any other
`done_reason` through untouched — so a Client may see `done_reason` values that are not in
the OpenAI `finish_reason` enum (e.g. `"load"`, `"unload"`, `"length"`).

**`tool_choice` is not supported by Ollama at all.** From
[docs/api/openai-compatibility.mdx](https://github.com/ollama/ollama/blob/main/docs/api/openai-compatibility.mdx),
the unsupported list for `/v1/chat/completions` is verbatim:

```
- [ ] `tool_choice`
- [ ] `logit_bias`
- [ ] `user`
- [ ] `n`
```

Also unsupported: `logprobs` (listed as unsupported under *features* although the request
struct carries the fields), and **image URLs** — only base64 image content is accepted, not
`image_url` pointing at a remote URL. Ollama additionally accepts non-OpenAI fields
`reasoning`, `reasoning_effort` (`high|medium|low|max|none`) and a debug-only
`_debug_render_only`.

**LM Studio** returns OpenAI-shaped `tool_calls` with `arguments` as a JSON string and
streams them incrementally through `delta.tool_calls[].function.arguments` fragments — the
docs show the exact sequence, including `id` present only on the first fragment:

```
ChoiceDeltaToolCall(index=0, id='814890118', function=…(arguments='', name='get_current_weather'))
ChoiceDeltaToolCall(index=0, id=None,        function=…(arguments='{"',       name=None))
ChoiceDeltaToolCall(index=0, id=None,        function=…(arguments='location', name=None))
```

Two live caveats:

1. **Tool-call IDs are bare numeric strings** (`'814890118'`, `'377278620'` in the docs'
   own examples), not `call_…`. A Client that validates ID format will reject them.
2. **For models without native tool-use support, LM Studio synthesises tool calling from a
   text protocol** and parses it back into OpenAI `tool_calls`
   ([tools.mdx](https://github.com/lmstudio-ai/docs/blob/main/1_developer/3_openai-compat/tools.mdx)).
   The default wire format is:

   ```
   [TOOL_REQUEST]{"name": "get_weather", "arguments": {"location": "Paris"}}[END_TOOL_REQUEST]
   ```

   and the docs say plainly: *"The default format is subject to change."* When parsing
   fails, the raw markers leak into `content`. This is exactly the sort of thing that makes
   `capabilities.trained_for_tool_use` worth surfacing in the model picker.

   LM Studio also **normalises tool names** (e.g. to snake_case) before showing them to the
   model **[0.3.23+]** — so the name a Client sends is not necessarily the name it gets
   back.
3. `tool_choice` — reported by downstream projects to accept only *string* values
   (`"auto"`, `"none"`), rejecting OpenAI's `{"type": "function", "function": {...}}` object
   form with HTTP 400. Treat object-form `tool_choice` as unsupported across all three.

**llama.cpp** requires the Jinja engine for tool calling. **[version]** `--jinja` is now
**enabled by default** (README arg table: "whether to use jinja template engine for chat
(default: enabled)"); on older builds it was opt-in and tool calls silently did not parse
without it. It may still need `--chat-template-file` to get a tool-capable template, with
`--chat-template chatml` as the fallback. llama.cpp adds non-OpenAI request fields
`parse_tool_calls` (whether to parse at all) and `parallel_tool_calls` (validated against
the Jinja template), plus `chat_template_kwargs` (e.g. `{"enable_thinking": false}`) and a
`reasoning_effort: "none"` that disables reasoning — where *other* values ("low", "max")
are explicitly documented as having **no effect**, unlike OpenAI where they change
behaviour. There is also a genuinely non-OpenAI endpoint,
`POST /v1/chat/completions/control`, for ending a reasoning block mid-stream.

### 6d. Error shapes — the sharpest divergence

**Ollama's native API and its OpenAI-compat layer use different error shapes.**

Native (`/api/*`), per
[docs/api/errors.mdx](https://github.com/ollama/ollama/blob/main/docs/api/errors.mdx) — a
bare string:

```json
{ "error": "the model failed to generate a response" }
```

OpenAI-compat (`/v1/*`), per `openai.NewError` in
[`openai/openai.go`](https://github.com/ollama/ollama/blob/main/openai/openai.go):

```go
type Error struct {
    Message string  `json:"message"`
    Type    string  `json:"type"`
    Param   any     `json:"param"`
    Code    *string `json:"code"`
}
func NewError(code int, message string) ErrorResponse {
    switch code {
    case http.StatusBadRequest: etype = "invalid_request_error"
    case http.StatusNotFound:   etype = "not_found_error"
    default:                    etype = "api_error"
    }
}
```

Only three `type` values exist, `param` is always null and `code` is always null. So the
envelope is OpenAI-shaped but the discriminants a Client would branch on are effectively
absent.

**The mid-stream error case is where a naive passthrough breaks.** `BaseWriter.writeError`
in [`middleware/openai.go`](https://github.com/ollama/ollama/blob/main/middleware/openai.go):

```go
func (w *BaseWriter) writeError(data []byte) (int, error) {
    var serr api.StatusError
    …
    w.ResponseWriter.Header().Set("Content-Type", "application/json")
    if err := json.NewEncoder(w.ResponseWriter).Encode(openai.NewError(w.ResponseWriter.Status(), serr.Error())); err != nil {
```

and `ChatWriter.Write` dispatches to it whenever the upstream status is not 200. The
consequences, in order of nastiness:

1. The error JSON is written **without the `data: ` SSE prefix** — it is a raw JSON object
   in the middle of an `text/event-stream` body.
2. It is **not followed by `data: [DONE]`**.
3. It tries to reset `Content-Type` to `application/json` after the header block has
   already been flushed as `text/event-stream`, which is a no-op on the wire.

A byte-forwarding proxy will hand its Client a stream that ends with an unparseable frame.
**Any Harness that terminates SSE and re-frames it for its own protocol must special-case
this.** Compare llama.cpp, which does the right thing (`server-context.cpp`): the error is
wrapped as `format_oai_sse(json{{"error", res_json}})`, i.e. a properly framed
`data: {"error": {...}}\n\n`, and the stream is then terminated.

Also note Ollama's native ndjson streams carry mid-stream errors as an extra ndjson line
`{"error": "an error was encountered while running the model"}` with the HTTP status
unchanged (still 200) — documented behaviour, but it means **HTTP status is not a reliable
success signal for streaming on Ollama in either mode.**

**llama.cpp** is the most faithful. The README states plainly: *"`llama-server` returns
errors in the same format as OAI."* `format_error_response` in
[`server-common.cpp`](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/server-common.cpp):

| `type` | HTTP |
| --- | --- |
| `invalid_request_error` | 400 |
| `authentication_error` | 401 |
| `permission_error` | 403 |
| `not_found_error` | 404 |
| `server_error` | 500 |
| `not_supported_error` | 501 |
| `unavailable_error` | 503 |
| `exceed_context_size_error` | 400 |

Two of these — `not_supported_error` (501) and `exceed_context_size_error` — are **not in
the OpenAI type vocabulary**. `exceed_context_size_error` in particular is genuinely useful
(a Client can offer to truncate) and worth mapping into a Vendor-neutral error type rather
than flattening. Note also the error object carries a numeric `code` (`503`), whereas
OpenAI's `code` is a string or null, and Ollama's is always null.

**LM Studio.** The native v1 stream has a well-specified `error` event with a closed type
set:

```json
{ "type": "error",
  "error": { "type": "invalid_request", "message": "\"model\" is required",
             "code": "missing_required_parameter", "param": "model" } }
```

with `type` ∈ `invalid_request | unknown | mcp_connection_error | plugin_connection_error |
not_implemented | model_not_found | job_not_found | internal_error`. This is the richest
error vocabulary of the three — and again, **it lives outside the OpenAI passthrough.**

On the OpenAI-compat side, streaming errors were only aligned to "the correct format
expected by OpenAI clients" in **0.3.18** — so pre-0.3.18 LM Studio streams malformed
errors. And the server still returns a **bare-string** error for router-level misses, with
the wrong status code:

```
POST /chat/completions  (missing the /v1 prefix)
→ HTTP 200
  {"error":"Unexpected endpoint or method. (POST /chat/completions)"}
```

per [lmstudio-bug-tracker#618](https://github.com/lmstudio-ai/lmstudio-bug-tracker/issues/618),
**still open** as of this research (filed 2025-04-25). A Harness that checks
`status == 200` and then parses a completion will get a type error, not an error message.

### 6e. Verdict for the Harness

Passthrough is *nearly* trivial for the happy path of plain streaming chat with no tools.
It is **not** trivial for:

- **error handling** — three different envelopes, one of which (Ollama) violates SSE
  framing mid-stream, and one of which (LM Studio) can return a bare string with HTTP 200;
- **reasoning content** — three different field names (`reasoning` / `reasoning` /
  `reasoning_content`), version-dependent in two of them;
- **tool calls** — Ollama has no `tool_choice`; LM Studio may synthesise them from a text
  protocol whose format is explicitly unstable and rewrites tool names; llama.cpp needs the
  right Jinja template and exposes template-derived capability flags nobody else has;
- **chunk cardinality** — Ollama can emit two chunks per upstream event and a separate
  usage-only chunk;
- **keep-alive frames** — llama.cpp emits SSE comment lines.

Recommendation: keep inference passthrough byte-transparent **only** while the response
status is 200 and the stream is well-formed, and put a thin per-Vendor *error and
terminator* normaliser in the Harness. Treat reasoning-field naming as a Vendor property
declared by the adapter (one of three known names) rather than something the Client sniffs.

---

## 7. Discovery on the network

| | Ollama | LM Studio | llama.cpp |
| --- | --- | --- | --- |
| Default port | `11434` | `1234` | `8080` |
| Default bind | `127.0.0.1` | `127.0.0.1` | `127.0.0.1` |
| How to expose | `OLLAMA_HOST=0.0.0.0:11434` | Server Settings → "Serve on Local Network", or `lms server start --bind 0.0.0.0` | `--host 0.0.0.0 --port N` |
| Auth | Bearer (`bearerAuth` in the OpenAPI spec); `api_key` ignored on `/v1/*` in local use | "Require Authentication" toggle; `Authorization: Bearer $LM_API_TOKEN` | `--api-key`; `/health` is exempt |
| CORS | — | "Enable CORS" toggle | built-in CORS proxy handling |
| Version handshake | `GET /api/version` → `{"version":"0.32.6"}` | none over HTTP; changelog-only | `/props` → `build_info: "b<number>-<commit>"` |
| **mDNS / DNS-SD** | **No** | **No** | **No** |

Sources: Ollama [FAQ](https://github.com/ollama/ollama/blob/main/docs/faq.mdx) — "Ollama
binds 127.0.0.1 port 11434 by default. Change the bind address with the `OLLAMA_HOST`
environment variable"; LM Studio
[serve-on-network](https://github.com/lmstudio-ai/docs/blob/main/1_developer/0_core/0_server/serve-on-network.mdx)
and [server settings](https://github.com/lmstudio-ai/docs/blob/main/1_developer/0_core/0_server/settings.md);
llama.cpp server README argument table.

**None of the three advertises itself via mDNS/Bonjour/DNS-SD.** Evidence: Ollama's
`go.mod` contains no mDNS/zeroconf/bonjour dependency; llama-server's documented CLI has no
such flag and the server sources contain no discovery code; LM Studio's docs describe no
local-network advertisement.

LM Studio does have a remote-access feature, **LM Link**, but it is *not* LAN discovery:
per [lmlink.md](https://github.com/lmstudio-ai/docs/blob/main/1_developer/0_core/lmlink.md)
and Tailscale's own write-up
([tailscale.com/blog/lm-link-remote-llm-access](https://tailscale.com/blog/lm-link-remote-llm-access)),
device discovery goes through the LM Studio Hub (account-scoped) and transport runs over a
Tailscale mesh VPN. Requests to `localhost:1234` are transparently served by a remote
device. Two consequences for us: (a) LM Link is an *alternative* to our own multi-host
transport, not a discovery mechanism we can consume; (b) an LM Studio Vendor at
`localhost:1234` may not actually be local, which makes any latency- or VRAM-based
placement inference unreliable.

llama.cpp router mode's `GET /models/sse` is the only push channel for *model* state
changes, but it is per-server and gives no host discovery.

**Implication:** host discovery has to be ours — configuration, or an active sweep of the
three default ports (`11434`, `1234`, `8080`) with a cheap fingerprint per Vendor:

- Ollama: `GET /` → body `Ollama is running`; then `GET /api/version`.
- LM Studio: `GET /api/v1/models` → object with a `models` array (note: may require a
  bearer token); fall back to `GET /api/v0/models` for < 0.4.0.
- llama.cpp: `GET /health` → `{"status":"ok"}`; then `GET /props` for `build_info`.

All three fingerprints are cheap, unauthenticated (llama.cpp's `/health` is explicitly
public), and — importantly for llama.cpp — exempt from resetting the sleep-idle timer.

---

## Consolidated source list

Ollama:
- <https://github.com/ollama/ollama/blob/main/docs/api.md>
- <https://github.com/ollama/ollama/blob/main/docs/openapi.yaml>
- <https://github.com/ollama/ollama/blob/main/docs/api/openai-compatibility.mdx>
- <https://github.com/ollama/ollama/blob/main/docs/api/errors.mdx>
- <https://github.com/ollama/ollama/blob/main/docs/api/streaming.mdx>
- <https://github.com/ollama/ollama/blob/main/docs/faq.mdx>
- <https://github.com/ollama/ollama/blob/main/api/types.go>
- <https://github.com/ollama/ollama/blob/main/types/model/capability.go>
- <https://github.com/ollama/ollama/blob/main/server/routes.go>
- <https://github.com/ollama/ollama/blob/main/server/model_list_cache.go>
- <https://github.com/ollama/ollama/blob/main/openai/openai.go>
- <https://github.com/ollama/ollama/blob/main/middleware/openai.go>

LM Studio:
- <https://lmstudio.ai/docs/developer/rest> / <https://github.com/lmstudio-ai/docs/blob/main/1_developer/2_rest/index.mdx>
- <https://github.com/lmstudio-ai/docs/blob/main/1_developer/2_rest/list.md>
- <https://github.com/lmstudio-ai/docs/blob/main/1_developer/2_rest/load.md>
- <https://github.com/lmstudio-ai/docs/blob/main/1_developer/2_rest/unload.md>
- <https://github.com/lmstudio-ai/docs/blob/main/1_developer/2_rest/endpoints.mdx> (v0)
- <https://github.com/lmstudio-ai/docs/blob/main/1_developer/2_rest/streaming-events.md>
- <https://github.com/lmstudio-ai/docs/blob/main/1_developer/3_openai-compat/tools.mdx>
- <https://github.com/lmstudio-ai/docs/blob/main/1_developer/0_core/ttl-and-auto-evict.md>
- <https://github.com/lmstudio-ai/docs/blob/main/1_developer/0_core/0_server/serve-on-network.mdx>
- <https://github.com/lmstudio-ai/docs/blob/main/1_developer/0_core/0_server/settings.md>
- <https://github.com/lmstudio-ai/docs/blob/main/1_developer/api-changelog.md>
- <https://github.com/lmstudio-ai/lmstudio-bug-tracker/issues/618>
- <https://tailscale.com/blog/lm-link-remote-llm-access>

llama.cpp:
- <https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md>
- <https://github.com/ggml-org/llama.cpp/blob/master/tools/server/server-common.cpp>
- <https://github.com/ggml-org/llama.cpp/blob/master/tools/server/server-context.cpp>
- <https://github.com/ggml-org/llama.cpp/blob/master/common/jinja/caps.h>
- <https://github.com/ggml-org/llama.cpp/pull/18228> (sleep on idle)
