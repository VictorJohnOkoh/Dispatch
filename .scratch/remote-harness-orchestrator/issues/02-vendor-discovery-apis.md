# Vendor discovery, capability and health APIs

Type: research
Status: resolved
Blocked by: —

## Question

The Vendor abstraction covers **discovery, capability and health — not inference** (inference is OpenAI-compatible and passes through). Establish from primary sources what each Vendor actually exposes, so the abstraction is drawn from evidence rather than guesswork.

For **Ollama** and **LM Studio**, and briefly for **llama.cpp's server** as a third data point:

1. **Model enumeration.** What endpoint lists available models, and what does each entry contain? Distinguish *installed/downloadable* from *currently loaded*.
2. **Capability metadata.** Can you learn a model's context length, whether it supports tool calling, whether it is multimodal, its quantisation or size — and if so, from where? This decides whether the Client's model picker can be honest about what a model can do.
3. **Lifecycle.** Can a model be explicitly loaded, unloaded, or kept alive? Is loading implicit on first request? What is observable about load state and how long loading takes? This is what a future admission-control policy would need.
4. **Health and readiness.** Is there a health endpoint, and can you distinguish "running but idle", "loading a model", and "busy"?
5. **Resource visibility.** Is anything reported about VRAM or memory use?
6. **Divergence from OpenAI compatibility.** Where does each depart from the OpenAI schema in ways that would leak into the passthrough Harness — streaming format, tool-call encoding, error shapes?
7. **Discovery on the network.** Default ports and bind addresses, and whether either advertises itself (mDNS or similar).

Point 6 matters as much as the rest: it decides whether the passthrough Harness is genuinely trivial or quietly needs per-Vendor handling.

Capture findings as a Markdown file in the repo and link it from the Answer. Note explicitly which facts are version-dependent.

## Answer

Full findings: [`docs/research/vendor-discovery-apis.md`](../../../docs/research/vendor-discovery-apis.md).

All three enumerate models and distinguish installed from loaded, but only via **native**
endpoints — the OpenAI `/v1/models` view is capability-free everywhere. Ollama and LM Studio
give real capability metadata (tools, vision, reasoning, quantisation, context); llama.cpp
gives per-server, not per-model, metadata via `/props` `chat_template_caps`.

Lifecycle diverges most: LM Studio has first-class load/unload plus load-progress events;
llama.cpp has them only in router mode; Ollama has neither — loading is implicit and
governed by `keep_alive`. Only llama.cpp has a real health endpoint and a busy signal;
only Ollama reports VRAM (`/api/ps` `size_vram`).

**Q6: passthrough is not trivial.** Reasoning fields differ three ways
(`reasoning`/`reasoning`/`reasoning_content`); error envelopes differ, and Ollama writes
mid-stream errors as raw JSON with no `data:` prefix and no `[DONE]`, while LM Studio can
return a bare-string error with HTTP 200. Tool calls need per-Vendor care. No Vendor
advertises via mDNS.

**Version-dependent:** Ollama `/api/tags` capabilities + `details.context_length` (≥ v0.30.2);
LM Studio v1 REST API (≥ 0.4.0), `reasoning` split out (0.3.23), stream errors fixed (0.3.18);
llama.cpp router mode, sleep-on-idle, and `--jinja` default-on are all recent (b10306).
