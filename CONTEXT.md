# Remote Harness Orchestrator

A single-user system for driving AI agent harnesses that run on separate, more powerful machines the user owns, with the model backing each harness chosen at launch time.

## Language

### Machines and roles

**Host**:
A machine the user owns that runs a Vendor and can run Harnesses. Has an identity — a named profile with its own connection details — because the user may have several.
_Avoid_: server, remote, box, device, node

**Daemon**:
The long-lived program on a Host that owns that Host's Session registry, spawns and supervises Harnesses, and serves the Control Plane API. A Daemon knows only its own Host and never learns about its peers.
_Avoid_: server, agent, service, broker

**Hub**:
The program that holds live connections to every configured Host's Daemon, merges what they report, and serves the Client. The only component that knows more than one Host exists. Runs from the same binary as the Daemon, in a different role.
_Avoid_: gateway, proxy, coordinator, controller

**Client**:
The browser UI the user interacts with. Talks only to the Hub, never to a Daemon or a Vendor directly.
_Avoid_: app, frontend, UI

### The two planes

**Control Plane**:
The path carrying Session lifecycle commands and Events — Client to Hub to Daemon. Crosses the network; never carries prompts for a Harness-backed Session.
_Avoid_: management channel, SSH layer

**Data Plane**:
The path a prompt and its response travel. For a Harness-backed Session this is entirely internal to one Host, because a Harness always reaches a Vendor on the same Host over localhost.
_Avoid_: inference channel, API layer

### Reachability

**Host State**:
The Hub's view of one Host, and the only place that view exists. One of `Connecting`, `Ready`, `Down` (carrying a cause of `unreachable` or `no-daemon`), or `Incompatible`. Derived from the liveness of the Hub's Event stream to that Host, never stored.
_Avoid_: status, availability, online, offline, health

**Handshake**:
The protocol version check the Hub and a Daemon run when the connection opens. Passing it makes a Host `Ready`; failing it makes the Host `Incompatible`, which the Hub never retries.
_Avoid_: negotiation, version check, hello

**Stale**:
Last-known Host content the Client keeps showing while the Host is not `Ready`, stamped with the time it was true. A Host is never hidden for being unreachable.
_Avoid_: cached, outdated, offline data

### Inference and agency

**Vendor**:
A program on a Host that serves model inference over a local API. Ollama, LM Studio and llama-swap are Vendors. The system abstracts a Vendor for discovery, capability and health — not for inference. A Vendor is reachable exactly when a call to it succeeds; reachability is never a value it returns and never stored.
_Avoid_: provider, backend, engine, runtime

**Vendor Adapter**:
The code that speaks one Vendor's API and answers five questions about it: where it answers, what Models it can serve, what is resident now, and load and unload. It promises nothing about idle, loading or busy, because no two Vendors expose the same ones. The Daemon holds the cache, the admission policy and everything else the Vendor will not say.
_Avoid_: driver, client, connector, provider

**Model**:
A specific set of weights a Vendor can serve, selected before a Session begins. Its id is the Vendor's own spelling, carried verbatim and unique inside one Vendor on one Host.
_Avoid_: LLM, checkpoint

**Capability**:
One thing a Model can do — hold a conversation, call tools, reason, or see. A Vendor Adapter answers each one `yes`, `no` or `unknown`, and `unknown` is an answer rather than a missing value. llama-swap reports `unknown` for every Model it has not loaded, and so does any Vendor version that does not carry the field.
_Avoid_: feature, flag, support level

**Harness**:
A program that turns a Model into an agent — running the tool-calling loop, managing context, and deciding what to do next. OpenCode and Pi are Harnesses. Sending a prompt straight to a Vendor with no agency is modelled as a passthrough Harness, not as a separate path.
_Avoid_: agent, framework, scaffold, runner

**Harness Adapter**:
The code that speaks one Harness's protocol and turns what it says into Events. It writes the five Kinds a Harness is the authority on, answers the Harness when it blocks on a question, and chooses the arguments the Harness is launched with. It does not own the process: the Daemon spawns it, holds its stdin, drains its stderr and kills it. An Adapter may decide what belongs with what, and may never report something that did not happen.
_Avoid_: driver, plugin, connector, backend, translator

**Gate**:
A Harness Adapter's ability to hold one Tool Call of a given `toolKind` until the Daemon has decided. Declared per Adapter, not discovered from the Harness, because no Harness reports it. OpenCode has no Gate for `read`, and a passthrough Harness has no Gates at all.
_Avoid_: hook, interceptor, permission, guard

**Session**:
One run of one Harness against one Model on one Host. The unit whose lifecycle a Daemon manages, and the unit an Event belongs to.
_Avoid_: conversation, chat, instance, job

**Event**:
One normalised, typed thing that happened inside a Session — an assistant message, a tool call, a tool result, an error, a termination. Every Harness's native output is translated into Events; the Client renders only Events, never raw Harness output. Native output that no Event Kind covers is dropped, and the Harness's raw bytes are kept in a per-Session transcript file beside the log. The ordered Event log of a Session is simultaneously its transport, its replay buffer and its history, and a Session's whole state is derivable by folding it.
_Avoid_: message, chunk, token, log line

**Event Kind**:
The type of one Event, from a closed set of fourteen. Written by the Harness adapter: `Reasoning`, `AssistantMessage`, `ToolCallRequested`, `ToolCallEnded`, `PromptCompleted`. Written by the Daemon: `SessionStarted`, `ApprovalPolicySet`, `PromptSubmitted`, `ApprovalRequested`, `ApprovalDecided`, `Error`, `SessionEnded`, `HubDetached`, `HubAttached`. A Kind exists when at least two of Pi, OpenCode and passthrough produce the fact and the Client draws it, or when it records a Daemon decision that changes how a Session behaves.
_Avoid_: event type, message type, tag

**Envelope**:
The five fields every Event carries whatever its Kind: Sequence Number, Session id, the Daemon's timestamp, Kind, and the Kind's payload. There is no Harness field, no Host field and no version field.
_Avoid_: header, metadata, wrapper

**Sequence Number**:
The Daemon-wide counter that orders every Event on one Host. Starts at 1, never skips, and is both the Event log's primary key and the `Last-Event-ID` offset. Unique inside one Daemon and nowhere else, so the Hub tracks one per Host.
_Avoid_: offset, index, event id, cursor

**Delta**:
A frame on the Event stream that adds text to an `AssistantMessage` or `Reasoning` Event the log already holds. Never stored, never given a Sequence Number, and never carrying information its Event will not eventually hold. The final Delta of an Event carries the whole text and replaces rather than appends, so a Client that dropped one repairs itself.
_Avoid_: chunk, token, partial, streaming event

**Prompt**:
One submission from the user and all the work a Session does because of it. Bounded by a `PromptSubmitted` Event and a `PromptCompleted` Event, which carries the stop reason and the token usage.
_Avoid_: turn, request, query, exchange

**Tool Call**:
One attempt by a Harness to run one tool, identified by a tool call id that correlates a `ToolCallRequested` Event with its `ToolCallEnded`. Every Tool Call ends: when a Harness reports no result, the Daemon writes `ToolCallEnded` with outcome `unknown` as the Prompt completes. `toolKind` is one of `read`, `edit`, `execute`, `fetch`, `other`.
_Avoid_: tool use, function call, action, invocation

### Containment

**Workspace Root**:
The directory on a Host outside which no Session may operate. Configured per Host; enforced by the Daemon when a Session's working directory is chosen.
_Avoid_: sandbox, jail, base path

**Approval Policy**:
The per-Session rule governing whether a Harness's tool call executes immediately, waits for the user's decision, or is refused. One decision per `toolKind`, so five slots that are always all set. Chosen when the Session starts and changeable while it runs, so answering an approval with "always allow" flips one slot. Every value it ever holds is an `ApprovalPolicySet` Event. A slot with no Gate may only be set to `auto`; setting it to `wait` or `refuse` fails, when the Session starts and on every change while it runs. A passthrough Session has no tools and so has no Approval Policy.
_Avoid_: permissions, confirmation mode, safety setting
