# The Vendor adapter interface

Type: grilling
Status: open
Blocked by: 02

## Question

The Vendor abstraction covers **discovery, capability and health — not inference**. Define its Go interface, informed by [Vendor discovery, capability and health APIs](./02-vendor-discovery-apis.md).

- **What is the interface?** The actual method set. Something in the region of: list Models, describe a Model's capabilities, report health, report what is currently loaded. Attack that list against what Ollama and LM Studio genuinely provide.
- **What is the common denominator, honestly?** The research settled this: capability metadata is rich on Ollama (a closed capability enum, plus `details.context_length` on `/api/tags`) and LM Studio (typed capabilities, quantisation, both max and loaded context length), but llama.cpp exposes only **per-server** capability via `/props.chat_template_caps` — there is no per-model catalogue at all. So the third Vendor doesn't just have less data, it has data at a *different granularity*. Does the interface model that, or does it exclude llama.cpp? Optionality the Client must constantly check is a leak wearing a disguise.
- **How is missing capability information represented?** "Unknown" and "not supported" are different, and the Client's model picker needs to tell the user which it is facing. Note that llama.cpp's per-server caps are arguably the *most* accurate of the three, since they're computed from the template that will actually run — so "less structured" is not the same as "less trustworthy".
- **Lifecycle and health diverge on every axis, and this is the hard part.** LM Studio has real load/unload plus load-progress events; llama.cpp has them only in router mode; Ollama has neither — loading is implicit, governed by `keep_alive`, and **a load in progress is entirely unobservable**. Only llama.cpp distinguishes idle from loading from busy. Only Ollama reports VRAM. A single `Health()` method that returns a uniform answer would be a fiction; decide what the interface can honestly promise, and what the Daemon must track itself because no Vendor will tell it.
- **Does the passthrough Harness use this interface or bypass it?** The divergences are real and specific: reasoning content arrives as `reasoning`, `reasoning` or `reasoning_content` depending on Vendor; **Ollama emits mid-stream errors as raw JSON with no `data:` prefix and no `[DONE]`**, breaking SSE framing outright; LM Studio can return a bare-string error with HTTP 200. So a byte-transparent passthrough is not viable. Decide where the per-Vendor error and terminator normalisation lives — this interface, the passthrough adapter, or a shared normaliser both use.
- **An assumption worth testing.** LM Studio's LM Link can make a `localhost:1234` endpoint point at *another machine*. That silently violates the same-Host invariant for Harness and Vendor, and nothing in the protocol reveals it. Is that detectable, does it matter, or is it a documented caveat?
- **Does the interface know about loading and unloading?** v1 ships one Session at a time, but [Session lifecycle, admission and containment](./08-session-lifecycle-and-containment.md) draws an admission seam that a real policy will later need to drive. Does load control belong in this interface now, or is it added later without breaking it?
- **Who calls it, and from where?** The Daemon on the Host, over localhost, with the Hub only seeing the results — or something else?
- **Caching and freshness.** Model lists change rarely, health changes constantly. Are these one interface or two, with different freshness contracts?
- **Testing.** Can adapters be tested against recorded HTTP fixtures?

Use `/codebase-design` alongside `/grilling`.
