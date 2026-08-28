# The Vendor interface has no Health method, and every capability it reports is three-valued

A Vendor could have answered `Health()` with a struct describing whether it is idle, loading or
busy, and how much memory it holds. Instead there is no health method at all. A Vendor is reachable
exactly when a call to it succeeds, which is the same shape ADR 0004 already gave Host State:
derived from the last thing that happened on the wire, never stored. Everything a `Health()` struct
would have carried is either unobservable on two of the three Vendors or already the Daemon's, and a
field populated by one Vendor is a field the Client learns to ignore.

The second decision is what a capability answer looks like. `Support` has three values — `Yes`, `No`
and `Unknown` — with `Unknown` as the zero value. That one choice absorbs llama-swap, which knows
nothing about a Model it has not loaded, and it absorbs every version difference in the other two, so
the interface has no version gate anywhere.

## What the sources decide

Four facts fix the shape, and one of them corrects the ticket.

**llama.cpp has a per-Model catalogue after all.** The ticket says llama.cpp "exposes only
per-server capability via `/props.chat_template_caps` — there is no per-model catalogue at all",
which is true of bare `llama-server -m`. ADR 0002 already decided that is not the Vendor. llama-swap
is, and it answers `GET /v1/models` with every Model in its config, loaded or not. This repo's own
capture confirms it on the Host: `probe llamacpp http://127.0.0.1:8080/v1/models -> HTTP 200`, in
`captures/pi-vendors/llamacpp/manifest.txt`, and the same manifest records the other half of the
same fact, `GET http://127.0.0.1:8080/props -> HTTP 404`, which is the 404 ADR 0002 predicted. So the
granularity mismatch is not catalogue against no catalogue. All three Vendors list Models. Two of
them attach capability metadata to the listing and one does not.

**Reading llama.cpp's capability answer costs a Model load.** `chat_template_caps` lives at
`/upstream/<model>/props`, and ADR 0002 records that fetching it is what starts a cold Model. Cold
loads on the development Host ran 4.4s for a 1.5B Q8_0 to 22.5s for a 20B. So the capability data
exists, is arguably the most accurate of the three because it is computed from the template that will
actually run, and cannot be gathered for a picker without loading every Model in turn.

**Health decomposes differently on every Vendor, and agrees on two things.** All three can be asked
whether they are answering, and all three report what is resident: Ollama at `/api/ps`, LM Studio in
`loaded_instances[]`, llama-swap at `/running`. Nothing else is common. Busy is not observable on
Ollama at all and reaches LM Studio only through `lms ps --json`, which is a CLI and therefore does
not exist for a Daemon. A load in progress is invisible on Ollama, streamed only on LM Studio's
native chat endpoint outside the passthrough, and visible on llama-swap. Memory is reported by
Ollama alone.

**Ollama's mid-stream error is not an SSE frame.** `BaseWriter.writeError` in `middleware/openai.go`
writes the error JSON with no `data: ` prefix, into a body already flushed as `text/event-stream`,
and never sends `[DONE]`. Any reader has to parse every line to survive it, so byte-transparent
passthrough was never available and the only open question is where the parser lives.

## The interface

```go
package vendor

// Vendor is one program serving Models on this Host's loopback. One value per
// configured Vendor, made at Daemon start and shared by every Session.
//
// There is no Health method. A Vendor is reachable exactly when one of these calls
// returns without error, and the Daemon derives its view from that rather than
// asking a second time in a second way.
type Vendor interface {
	// Endpoint is fixed for the life of the Daemon. It is what a Harness config is
	// pointed at and what the passthrough Adapter dials.
	Endpoint() Endpoint

	// Catalogue lists every Model this Vendor can serve, loaded or not, from one
	// call. It changes when the user pulls or deletes a Model, so the Daemon caches
	// it and the Client may show it Stale.
	Catalogue(ctx context.Context) ([]Model, error)

	// Resident reports what is in memory now. It changes constantly, so it is never
	// cached and never shown Stale. An empty slice and an error are different
	// answers: nothing loaded, against nobody answering.
	Resident(ctx context.Context) ([]Resident, error)

	// Load makes a Model resident with this Vendor's own evictor disabled, and
	// returns once it is resident. It reports no progress, because only LM Studio
	// offers any and only outside the passthrough. It blocks for as long as the load
	// takes, which was 4.4s to 22.5s on the development Host.
	Load(ctx context.Context, modelID string) error

	// Unload makes a Model not resident, whatever that takes on this Vendor. On
	// LM Studio it unloads every instance of that Model, because the Daemon's intent
	// is that the VRAM comes back.
	Unload(ctx context.Context, modelID string) error
}

// Endpoint is where a Vendor answers, and it never leaves this Host.
type Endpoint struct {
	Kind Kind

	// Base is the Vendor's root, such as http://127.0.0.1:11434. All three Vendors
	// serve their OpenAI-compatible surface at Base + "/v1", so a caller that wants
	// that surface appends it. Native paths belong to the adapter and to nobody else.
	Base string

	// Token is the bearer token when the user turned authentication on, and empty
	// otherwise. Every Vendor here defaults to none.
	Token string
}

type Kind uint8

const (
	Ollama Kind = iota
	LMStudio
	LlamaSwap
)

// Model is one set of weights a Vendor can serve.
type Model struct {
	// ID is the Vendor's own spelling and is used verbatim, never parsed and never
	// normalised. It is unique inside one Vendor on one Host and nowhere else, the
	// same scoping a Sequence Number has inside one Daemon.
	ID string

	// Name is for a human. It is the ID when the Vendor offers nothing better.
	Name string

	Caps Capabilities

	// Context is the Model's trained maximum, and 0 when the Vendor does not say.
	// It is not what a resident Model was loaded with; that is on Resident, and
	// showing this one while the other is in force is the lie the picker must not
	// tell.
	Context int

	// Quant is a label such as "Q4_K_M", empty when the Vendor does not say.
	// llama-swap never says.
	Quant string

	// Bytes is the size on disk, 0 when the Vendor does not say. It is not resident
	// memory.
	Bytes int64
}

// Support is a Vendor's answer about one capability. Unknown is an answer and not a
// missing value, so nothing here is a pointer and no caller writes a nil check.
// Unknown is the zero value, which makes an unfilled Capabilities honest rather
// than wrong.
type Support uint8

const (
	Unknown Support = iota
	No
	Yes
)

// Capabilities is the four questions the Client's Model picker asks. Nothing here is
// version dependent: an endpoint that does not carry the answer produces Unknown.
type Capabilities struct {
	// Chat is whether this Model can back a Session at all. An embedding Model
	// cannot.
	Chat Support

	// Tools is whether the Model was trained for tool calling. It is not whether the
	// endpoint will accept a tools array: LM Studio accepts one either way and
	// synthesises the calls from a text protocol it says is subject to change.
	Tools Support

	// Reasoning is whether the Model produces reasoning content. A Session on a Model
	// that does writes Reasoning Events.
	Reasoning Support

	Vision Support
}

// Resident is one Model in memory now.
type Resident struct {
	// ModelID matches a Model.ID from Catalogue. It repeats when LM Studio holds two
	// instances of one Model, which the Daemon never causes and must still read.
	ModelID string

	// Context is what the runner was actually loaded with, 0 when the Vendor does not
	// say.
	Context int

	// VRAM is resident bytes on the GPU, 0 when the Vendor does not say. Ollama alone
	// says, and comparing it against Model.Bytes is how partial CPU offload is seen.
	VRAM int64
}
```

Five methods, and four of them are one HTTP call.

## Health is what happens to a call

The rule:

> A Vendor is reachable exactly when a call to it succeeds. Reachability is never a
> return value and never stored.

This is ADR 0004's rule about Host State moved down one level, and it is right for the same reason:
a stored answer about liveness is a claim that was true once. It is also what this repo's own
scripts already do. `capture-opencode-host.sh` fingerprints all three Vendors with one call each —
`ollama|/api/tags`, `lmstudio|/api/v1/models`, `llamaswap|/v1/models` — and the recent fix in 507f60c
made a Vendor that does not answer stop the run rather than fall back. The probe endpoint and the
catalogue endpoint are already the same call on every Vendor.

So a separate `Alive()` would be a second way to learn one fact, and two ways to learn one fact
disagree eventually. ADR 0005 rejected a second sequence counter for that reason.

What the interface then does **not** promise, and who holds each one instead:

| the fact | why it is not here | who holds it |
| --- | --- | --- |
| busy | Not observable on Ollama. CLI-only on LM Studio. | The Daemon. It launched every Session on this Host, so it knows what is running without asking. |
| a load in progress | Not observable on Ollama; on LM Studio only on the native chat endpoint, which the passthrough does not use. | The Daemon. `Load` blocks, so the Daemon knows a load started because it made the call and knows it finished because the call returned. |
| free or total VRAM | No Vendor reports it. Ollama reports only what it has loaded itself. | The Daemon, from the Host, which ADR 0002 already required for per-pair arithmetic. |
| the Vendor's version | Ollama has `/api/version`, LM Studio has nothing over HTTP, llama-swap answers `/props` with 404. | Nobody. Nothing in this design branches on a Vendor version. |
| admission limits | `OLLAMA_MAX_LOADED_MODELS` and friends are process environment, not API. | The Daemon, as ADR 0001 and ADR 0002 already say. |

Every row that leaves is a row the Daemon was always going to own, which is the test that the seam
is in the right place rather than merely convenient.

## Three values, and the version matrix they delete

`Unknown` was added for llama-swap, which reports no capability metadata for an unloaded Model. It
turns out to pay for more than that.

| Vendor and version | what the one catalogue call carries | what the interface reports |
| --- | --- | --- |
| Ollama ≥ v0.30.2 | `capabilities` and `details.context_length` on `/api/tags` | every field answered |
| Ollama < v0.30.2 | the same listing without `capabilities` or `context_length` | `Unknown` and `Context: 0` |
| LM Studio ≥ 0.4.0 | typed `capabilities`, `max_context_length`, `quantization` | every field answered |
| LM Studio < 0.4.0 | `/api/v0/models`, which has no `capabilities` object | `Unknown` |
| llama-swap | model ids and nothing else | `Unknown` for every Model it has not loaded |

Five rows, one code path, no version check. Without a third value the first four rows need a version
gate and a fan-out to `/api/show` per Model on old Ollama, and the fifth row needs llama.cpp excluded
or special-cased. `Unknown` is what lets one call per Vendor be the whole of discovery.

**Unknown and No are different, and the Client must draw them differently.** "This Model was not
trained for tool use" hides it from an agent Session's picker. "Nobody has said" shows it with the
gap visible. Getting that backwards on llama-swap hides every Model on the Vendor.

**A llama-swap answer sharpens when the Model is loaded, and goes back to Unknown when it is not.**
`Catalogue` fills `Caps` from `/upstream/<id>/props` for Models that are already resident and reports
`Unknown` for the rest, so it never causes a load of its own. That answer is the best of the three
while it lasts, because `chat_template_caps` is computed from the template that will actually run
rather than read off a metadata label. If the Daemon wants to remember the sharper answer past
eviction, that is its cache and not the interface's memory.

## Load and Unload are in the interface now

The ticket asks whether load control belongs here yet. It does, because ADR 0002 already put it on
the Daemon: "The Daemon preloads by sending a cheap request … It reads state from `GET /running` and
reclaims with `POST /api/models/unload/:model_id`." Leaving `Load` out would put three Vendors' load
mechanics in the Daemon, which is the seam not existing.

All three can do both, by three different mechanisms, which is exactly what an adapter is for:

| | preload | unload | evictor to disable |
| --- | --- | --- | --- |
| Ollama | empty `POST /api/chat`, which returns `done_reason: "load"` | the same call with `keep_alive: 0` | `keep_alive: -1` on every request the Daemon sends |
| LM Studio | `POST /api/v1/models/load` | `POST /api/v1/models/unload` per `instance_id` | load explicitly rather than letting a first request JIT-load, so the 60 minute TTL and Auto-Evict do not apply |
| llama-swap | `GET /upstream/<id>/props`, which ADR 0002 confirms starts a cold Model | `POST /api/models/unload/:model_id` | `ttl: 0` on every Model, which ADR 0002 already requires |

The rule the third column follows is:

> The Daemon turns off every Vendor's own evictor and does all the loading itself.

ADR 0002 wrote this for llama-swap, where a second evictor would surprise a Session idle between
prompts. It generalises, and LM Studio is the sharper case. Auto-Evict keeps at most one JIT-loaded
Model resident, so on a default LM Studio a Harness whose first prompt loads its Model silently
unloads whatever was there. The Daemon calling `/api/v1/models/load` before the Session starts is
what stops that, and it is the same call the Daemon makes anyway to know the Model is ready. This is
also the rule that makes `Load` blocking acceptable: it runs once, at Session launch, before there is
anything to be responsive to.

`Load` and `Unload` are mechanism. Whether a load may happen — the per-pair VRAM arithmetic, and the
gate that stops two Sessions on different Models reaching one llama-swap — is admission policy and
stays with [#9](https://github.com/VictorJohnOkoh/Capstone/issues/9), where ADR 0002 put it.

**Untested, and named as such.** That an LM Studio Model loaded over REST is exempt from Auto-Evict
and from the JIT TTL is read from the documentation, not measured on the Host. The documentation says
non-JIT loads are unaffected and that `lms load` sets no TTL, and the REST load endpoint is the same
kind of explicit load. If it turns out JIT rules apply to it, the fallback is a `ttl` the Daemon sets
long and refreshes, which changes the adapter and not the interface.

## A Catalogue goes Stale, a Resident list never does

These are two freshness contracts, and they are one interface because the difference belongs to the
caller rather than to the Vendor.

`Catalogue` changes when a human pulls or deletes a Model, so it is cached by the Daemon and it is
exactly the "last-known Host content" that `CONTEXT.md` already calls **Stale**: shown dimmed and
stamped with the time it was true while the Host is not `Ready`. ADR 0004 already decided a Host is
never hidden for being unreachable, and the Model list is the content that rule was written about.

`Resident` is worthless when old. A Model that was loaded a minute ago tells nobody anything about
what is loaded now, so it is fetched on demand, never cached, and never rendered Stale. When the call
fails the Client shows nothing rather than a memory.

**The cache is the Daemon's, not the adapter's.** Three adapters each holding a clock and a mutex is
the same code written three times and three chances to serve a stale answer as a fresh one. This is
ADR 0006's seam applied to a different pair: what is identical across Vendors goes on the Daemon,
what differs goes in the adapter. Nothing about caching a list differs between Ollama and LM Studio.

## The stream reader has no per-Vendor branches

The ticket asks whether error and terminator normalisation lives in this interface, in the
passthrough Adapter, or in a shared normaliser. It is none of the three as posed, because writing the
rules down shows they are not per-Vendor.

The reader lives in package `vendor` as a plain function, not on the `Vendor` interface. It is
Vendor knowledge — which field carries reasoning, which bodies are errors, which framing gets
violated — so it belongs in the package where Vendor knowledge lives. It is not part of the Vendor
abstraction, which stays discovery, capability and health.

```go
// ReadStream consumes one OpenAI-compatible SSE body and yields Frames until the
// stream ends. It takes no Kind, because none of its rules is Vendor specific and
// none can misfire on a well-formed stream.
func ReadStream(r io.Reader, out func(Frame))

type Frame struct {
	Kind  FrameKind
	Text  string // the text, or for FrameError the Vendor's own words
	Stop  string // FrameEnd only
	Usage Usage  // FrameEnd only
}

type FrameKind uint8

const (
	FrameText FrameKind = iota
	FrameReasoning
	FrameError     // the Vendor refused or failed
	FrameTruncated // the body ended with no terminator and no finish reason
	FrameEnd
)
```

Five rules, each safe on all three:

1. **A line beginning `:` is ignored.** llama.cpp writes `":\n\n"` as a keep-alive ping when no token
   has been produced for `sse_ping_interval`. It is valid SSE that a line-oriented proxy chokes on.
2. **A line that is bare JSON with no `data: ` prefix is an error frame.** This is Ollama's
   mid-stream error and nothing else produces it. The Vendor's own message goes into `Frame.Text`.
3. **A 200 whose body is an object with an `error` key rather than a completion is an error frame.**
   This is LM Studio's router miss, `lmstudio-bug-tracker#618`, open since 2025-04-25.
4. **Reasoning is read from `reasoning` or `reasoning_content`, whichever is present.** The names are
   disjoint and no Vendor sends both, so reading all of them deletes the version matrix that
   declaring one name per Vendor would need — LM Studio moved the field out of `content` in 0.3.23,
   and the declaration would have to track that. This departs from the research's recommendation to
   declare the name per Vendor, and the reason is that tolerance costs one map lookup and a
   declaration costs a version table.
5. **The body ending with no `[DONE]` and no finish reason is `FrameTruncated`.** Ollama's error path
   sends no terminator, and a dropped connection looks the same. Both are the same fact for a Client.

The four other divergences need no rule at all. Ollama splitting one emission into two chunks, its
separate usage-only chunk, and its `finish_reason` values outside the OpenAI enum (`"load"`,
`"unload"`) are all absorbed by accumulating text and passing `Stop` through as a string.

The passthrough Adapter is then a loop with a five-way switch, and the mapping onto ADR 0006's `Sink`
is one to one:

| Frame | Sink call |
| --- | --- |
| `FrameText` | `Message(text, false)` |
| `FrameReasoning` | `Reasoning(text, false)` |
| `FrameError` | `Failed(event.ErrVendor, text)` |
| `FrameTruncated` | `Failed(event.ErrStreamTruncated, "")` |
| `FrameEnd` | `Completed(stop, usage)` |

`vendor` yields its own `Frame` and its own `Usage` rather than Event types, so it imports no other
package of this project. That fixes the dependency order for
[#12](https://github.com/VictorJohnOkoh/Capstone/issues/12): `vendor` is a leaf, `harness` imports
`vendor` and `event`, and nothing imports `harness`.

## LM Link: documented, and it costs nothing yet

LM Studio's LM Link serves `localhost:1234` from another machine over a Tailscale mesh, discovered
through an account-scoped hub. Nothing in the HTTP protocol reveals it, and no probe from the Daemon
distinguishes it: the local LM Studio process really is running and really is answering, it is simply
answering with someone else's weights. So it is not detectable, and this design does not try.

What it would break is inference from latency and from memory figures. Neither exists here. LM Studio
reports no memory over HTTP at all, so the Daemon's LM Studio arithmetic is already an estimate made
from the Host rather than from the Vendor, and nothing in the interface times a call to conclude
anything. The exposure is bounded to something already weak.

Two things keep it a caveat rather than a hole. The system is single-user, and the person who turned
LM Link on is the person who configured the Host, so it is silent to the software and known to the
only human involved. And the rule that keeps it harmless is one this design follows for other
reasons:

> The Daemon never concludes anything from how long a Vendor took to answer.

That is the same rule ADR 0006 wrote for Harness supervision — a timer may end a Session and may
never diagnose one — reaching a second place. If a later version starts placing Sessions by measured
latency, this is what it has to reopen first.

## Who calls it, and from where

The Daemon, on the Host, over loopback. Nothing else ever holds a `Vendor` value. The Hub sees the
results and the Client sees what the Hub merged, which is `CONTEXT.md`'s rule that a Client talks
only to the Hub, never to a Daemon or a Vendor.

This adds a Control Plane path that is a request and a response rather than an Event. A Model
catalogue is not a Session fact — it exists before any Session does, and ADR 0005's envelope carries
a Session id on every Event — so it cannot travel as one. The shape is Client to Hub to Daemon and
back. The transport is [#10](https://github.com/VictorJohnOkoh/Capstone/issues/10)'s, and what this
ADR fixes for it is the two freshness contracts above: one answer the Hub may hold and replay Stale,
one it must fetch or omit.

## Testing against recorded bodies

Every adapter's whole contact with the world is one `http.Client`, supplied by the caller, exactly as
ADR 0006 made `Spawn`, `Files` and `Sink` caller-supplied. A test supplies a `RoundTripper` that
answers from recorded bodies, and no Vendor, no GPU and no network is involved.

```go
func TestLlamaSwapReportsUnknownForAnUnloadedModel(t *testing.T) {
	v := NewLlamaSwap(Endpoint{Base: "http://127.0.0.1:8080"}, replay(t, "captures/vendors/llamaswap/"))
	got, err := v.Catalogue(t.Context())
	// every Caps field Unknown, every ID present, no request to /upstream/ made
}
```

The reader is easier still: `ReadStream` takes an `io.Reader`, so its fixtures are captured response
bodies with no HTTP at all. The three that matter are the three malformed ones — Ollama's unframed
error mid-body, LM Studio's bare string under 200, and llama.cpp's comment ping — because those are
the cases a hand-written reader gets wrong.

**The fixtures do not exist in this repo yet, and this is the second time that gap has been found.**
`captures/pi-vendors/README.md` records it: none of the three directories holds a `vendor-models.json`
although every manifest reports `HTTP 200` for the fetch, because a native curl was handed an MSYS
path, returned the body, and wrote nothing. That is finding R8 — a 200 is not proof the artefact
arrived — and it is why the manifests read `vendor-reported ctx: none` on all three Vendors. So the
capability data this ADR reasons about is read from primary sources rather than from bytes taken off
the Host. Landing those bodies is the first task the interface needs, and R8's rule is the way to
land them: check the file, not the status line.

## Considered options

- **A `Health()` method returning idle, loading or busy.** The ticket's own starting list, and what
  any reader expects. Rejected: two of the three states are unobservable on Ollama and CLI-only on
  LM Studio, so the struct would be honest on one Vendor and fiction on two, and the Daemon already
  knows both facts because it started the Session and made the load call.
- **A separate `Alive()` or `Ping()`.** Cheap, and every Vendor has a fingerprint endpoint. Rejected:
  on all three Vendors the fingerprint endpoint is the catalogue endpoint, which the repo's own
  capture scripts already demonstrate, so it is a second way to learn one fact.
- **`*bool` for a capability nobody reported.** The obvious Go spelling of "missing". Rejected: it
  puts a nil check at every use in the Client, which is the optionality the ticket warned about, and
  the zero value of the containing struct is then a struct of nils rather than a struct of honest
  Unknowns.
- **`bool` plus a `Known` bitmask.** Compact. Rejected: two fields that must agree, and reading one
  without the other compiles.
- **Exclude llama.cpp from the interface.** The ticket offers it. Rejected on a corrected premise:
  llama-swap has a catalogue, so what llama.cpp lacks is capability metadata, which `Unknown` already
  covers for two versions of Ollama and LM Studio as well.
- **Model llama.cpp's per-server capability as a separate server-level type.** Honest about the
  granularity. Rejected: it is per-server only in bare `llama-server`, which ADR 0002 already ruled
  out as the Vendor. Behind llama-swap the caps are per-Model and reachable at
  `/upstream/<id>/props`, so a server-level type would model a Vendor this project does not run.
- **Have `Catalogue` load each Model to fill in llama-swap's capabilities.** The data is right there.
  Rejected: building a picker would load every Model in the config in turn, at 4.4s to 22.5s each,
  and evict whatever a Session was using.
- **Fan out to `/api/show` per Model on Ollama.** Gets a full answer on old Ollama. Rejected: N calls
  against a cache Ollama itself maintains because they are expensive, to buy one Vendor on one old
  version a sharper answer the interface can already express as `Unknown`.
- **Two interfaces, a `Catalogue` and a `Probe`, split by freshness.** The ticket asks. Rejected: the
  freshness difference is entirely in the caller. Two interfaces means two values per Vendor and two
  registries in the Daemon to describe one program.
- **Adapters cache their own answers.** Puts the cache next to the call. Rejected: nothing about
  caching a list differs across three Vendors, so it is ADR 0006's rule pointing the other way.
- **Leave `Load` and `Unload` out until admission control exists.** Smaller interface now. Rejected:
  ADR 0002 already committed the Daemon to preloading and unloading llama-swap, so the mechanics
  would sit in the Daemon with a three-way switch on Vendor kind.
- **Put load progress on the interface.** LM Studio streams it. Rejected: one Vendor, and only on the
  native chat endpoint the passthrough does not use. The Daemon knows a load started because it made
  the call.
- **Byte-transparent passthrough with no reader.** The ticket's own hypothesis to test. Rejected by
  Ollama's `writeError`, which puts unframed JSON in an SSE body and sends no terminator. Once a
  reader is needed for that, transparency is already gone.
- **A per-Vendor normaliser selected by `Kind`.** What the research recommends. Rejected after
  writing the rules out: all five are structural and none can misfire on a well-formed stream, so
  the `Kind` argument would never be read.
- **Declare each Vendor's reasoning field name in its adapter.** Also the research's recommendation.
  Rejected: the three names are disjoint, so reading all of them is a map lookup, while declaring one
  needs a version table that LM Studio 0.3.23 already breaks.
- **Put the reader in `harness`, beside its only caller.** Rejected: it would give the passthrough
  Adapter a three-way switch on Vendor kind, which is Vendor knowledge in the wrong package.

## Consequences

- [#12](https://github.com/VictorJohnOkoh/Capstone/issues/12) gets the last of its three packages and
  a settled order. `vendor` holds `Endpoint`, `Kind`, the interface, three adapters and `ReadStream`,
  and imports nothing else of this project. `harness` imports `vendor` and `event`. The Daemon
  imports all three.
- [#9](https://github.com/VictorJohnOkoh/Capstone/issues/9) inherits the loading contract: call `Load`
  before the Session starts and `Unload` when it ends, never let a Harness's first prompt load a
  Model, and hold the per-pair VRAM arithmetic ADR 0002 already gave it. It also inherits busy, which
  no Vendor reports and the Daemon knows for free.
- [#10](https://github.com/VictorJohnOkoh/Capstone/issues/10) inherits a Control Plane request and
  response beside the Event stream, carrying a Model catalogue that predates every Session, with one
  answer that may be replayed Stale and one that may not.
- [#11](https://github.com/VictorJohnOkoh/Capstone/issues/11) draws `Unknown` and `No` differently in
  the Model picker, or a llama-swap Host shows no usable Models at all. It shows `Model.Context`
  against `Resident.Context` when they differ, and it renders a catalogue Stale with the time it was
  true, which ADR 0004 already asked for.
- The passthrough Adapter in ADR 0006 becomes a five-way switch over `Frame` with a one-to-one
  mapping onto `Sink`. That is the confirmation ADR 0006 asked for that passthrough is an Adapter
  rather than a path.
- `docs/research/captures/vendors/` does not exist and is the first thing the `vendor` package needs:
  one catalogue body and one resident body per Vendor, plus three malformed SSE bodies. R8's rule
  applies to collecting them — the last attempt reported `HTTP 200` and wrote nothing.
- The Daemon disables three Vendors' evictors in three different ways, and getting any of them wrong
  is a Session that silently loses its Model between prompts. On LM Studio it is worse than a reload:
  Auto-Evict takes out a Model the Daemon did not ask about.
- Nothing in this system reads a Vendor's version, so a Vendor that stops carrying a field degrades
  to `Unknown` rather than failing. The Handshake in ADR 0004 stays the only version check anywhere.
- An LM Studio Vendor may be answering from another machine and nothing here detects it. Any later
  feature that places Sessions by latency reopens this first.
- `CONTEXT.md` gains **Vendor Adapter** and **Capability**, and **Vendor** gains the sentence that a
  Vendor is reachable exactly when a call to it succeeds.

This does not reopen ADR 0002 or ADR 0006. ADR 0002 put admission control on the Daemon and named
`GET /running`, `/upstream/<model>/props` and `POST /api/models/unload/:model_id` as the mechanics;
this ADR gives those mechanics a signature and generalises the `ttl: 0` rule to all three Vendors.
ADR 0006 said #8 owns `vendor.Endpoint`, the reasoning field names and the three error bodies, and
all three are here.
