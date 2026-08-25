# llama-swap is how llama.cpp becomes a Vendor, and the Daemon still owns admission control

The Daemon could have driven llama.cpp directly, spawning one `llama-server` per Model on a port it picked and killing it to reclaim VRAM. Instead llama-swap runs on the Host at `127.0.0.1:8080` and llama.cpp sits behind it, while the Daemon keeps every decision about what may load and when. We chose this because llama-swap is what makes llama.cpp a Vendor at all, and because the admission control the Daemon needs has to sit above every Vendor anyway. Ollama and LM Studio load on demand and evict on their own timers with no knowledge of a Session, so removing llama-swap would not remove the problem it appears to solve.

llama-swap has no memory accounting of any kind. It reports GPU numbers on `/metrics` and it can hold several Models resident through its `matrix` config, but nothing in it asks whether the next load will fit. Treating it as the Host's memory manager was never possible.

## Considered options

- **Bare `llama-server`, supervised by the Daemon.** The Daemon spawns one process on a fixed port and restarts it to switch Model. Total control, no dependency, roughly 150 lines. Rejected because it only works when a Host runs one Model at a time. Two Sessions on two Models need two `llama-server` processes and something to route between them, and a Harness takes one fixed `baseUrl` per provider, so that something is a reverse proxy. Building it means rebuilding llama-swap.
- **llama-swap as the memory manager.** Let `ttl` evict and let swap-on-request handle contention. Rejected because it inverts the requirement. When a request arrives for a Model that is not loaded, llama-swap replaces the running one. It cannot block, and it does not know a Session is using what it is about to unload.
- **llama-swap as mechanism, Daemon as decision-maker.** Chosen.

## Consequences

- `ttl` must be `0` on every Model. A second evictor working to its own schedule will surprise the Daemon: a Session idle between prompts loses its Model and pays a reload the Daemon never planned. Measured cold loads on the development Host run from 4.4s for a 1.5B Q8_0 to 22.5s for a 20B.
- The Daemon must never let two Sessions on different Models reach one llama-swap on its own. Ordering requests is not enough, because the eviction happens on arrival rather than on contention. Gating belongs at Session launch.
- Per-pair VRAM arithmetic lives in the Daemon. Whether two Models coexist depends entirely on which two. On the development Host, a 16 GB card reports 15.1 GiB free. The weights alone for `qwen2.5-coder-1.5b` are 1.5 GiB and leave room for nearly anything, while `gpt-oss-20b` is 12.8 GiB and leaves room for nothing. KV cache is on top of that, and it scales with the context the server started with.
- There is no explicit load endpoint. The Daemon preloads by sending a cheap request, and `GET /upstream/<model>/props` is confirmed to start a cold Model. It reads state from `GET /running` and reclaims with `POST /api/models/unload/:model_id`.
- All three Vendors are discovered, health-checked and driven the same way, so llama.cpp needs no bespoke process supervision that Ollama and LM Studio do not.
- llama-swap answers `/props` with HTTP 404 and the message `no model id could be identified`, because it fronts several Models and cannot tell which one is meant. The per-Model view is at `/upstream/<model>/props`. Anything reading llama.cpp's own metadata has to handle both shapes, since a Host may run either.
- A third-party program now sits in the Data Plane on every Host that serves llama.cpp.
- Untested: whether llama-swap waits for in-flight requests to drain before it swaps. If it does not, an evicted Session loses its turn mid-generation rather than merely stalling. This does not change the decision, because the Daemon has to gate either way, but it changes how bad the failure looks when the gate has a bug.

This is consistent with ADR-0001, which already puts admission control among the things the Daemon exists to do.
