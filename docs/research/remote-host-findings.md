# Remote Host findings

Research for [issue #4 — Stand up a Host](https://github.com/VictorJohnOkoh/Capstone/issues/4).

Date: 2026-08-25. Produced by `scripts/capture-remote-host.sh` driving a second
machine over SSH.

Every capture before this one ran on one machine: `ZenitoBurrito`, with the
Vendor reached at `172.25.112.1` — a WSL2 virtual adapter on the same box. This
run is the first with a Client and a Host that are different machines on a real
network. The findings below are the ones a single machine cannot produce.

Evidence tags:

- `[remote]` — observed over SSH between two machines.
- `[desktop]` — observed at the Host's own keyboard, for contrast.
- `[open]` — the cause is not established. Recorded as observed, not explained.

| | |
| --- | --- |
| Client | `192.168.4.28`, Git Bash |
| Host | `192.168.4.49`, Windows 11, OpenSSH `sshd`, `DefaultShell` = `C:\Program Files\Git\bin\bash.exe` |
| Auth | ed25519 key, `administrators_authorized_keys` |

---

## R1. A Windows SSH session inherits the Machine PATH only

An installer that offers "Add to PATH" writes the **User** PATH. An SSH session
never reads it. So a tool works at the Host's keyboard and is invisible to the
orchestrator, and the symptom is indistinguishable from not installed. `[remote]`

Node and curl passed on the first attempt because `C:\Program Files\nodejs` and
`C:\Program Files\Git` are Machine PATH entries. Python and Hermes failed
because they sat under `C:\Users\Victor\AppData`, which only the desktop session
sees. `[remote]`

Moving the entries to the Machine PATH and restarting `sshd` fixes it. The
prerequisites doc now says PATH "for the SSH session, not just for the desktop
session" for this reason.

## R2. Hermes runs at the Host's keyboard and not over SSH

`hermes --version` at the Host prints `Hermes Agent v0.19.0 (2026.7.20)`,
Python 3.11.15. `[desktop]`

The same command over SSH fails:

```
error: uv trampoline failed to spawn Python child process
  Caused by: entity not found (os error 2)
```

`hermes acp --check` exits 1. So does `python --version`. `[remote]`

The install is a chain of launcher stubs. `hermes.exe` is a uv trampoline that
spawns `venv\Scripts\python.exe`, itself a trampoline that reads `pyvenv.cfg`
and spawns the real interpreter through a directory junction. Four links, and
one of them resolves at the desktop and not over SSH.

Ruled out, each by measurement rather than argument:

| Suspect | Evidence against |
| --- | --- |
| Broken install | Runs at the desktop, all four links present on disk |
| Missing interpreter | Present, 91,648 bytes, runs when called directly |
| Environment | Runs under `env -i` with nothing set at all |
| File ACLs | `Local` and `Roaming` grant identical rights to the session's own user |
| Junction policy | `fsutil`: local-to-local evaluation ENABLED |
| Code integrity | Every block event names an unrelated DLL |
| The AppControl filter driver | Stopping `appcservice` changed nothing |
| Profile access | Pi runs from `AppData\Roaming\npm` over SSH without complaint |

The cause is not established. `[open]`

**The finding does not depend on the cause.** A Host can pass every
installation check and still refuse to run a Harness. An orchestrator that asks
"is it installed" gets the wrong answer; it has to try to run the thing.

Issue #4 accepts "a recorded reason why one is not". This is that reason. Pi
carries the remote capture.

## R3. Pi runs over SSH unchanged

Pi answers over SSH from `C:\Users\Victor\AppData\Roaming\npm`, same version as
the local captures, no adaptation. `[remote]`

Two Harnesses installed the same day on the same machine, one of which survives
being driven remotely and one of which does not. The Daemon cannot assume that
what works locally works remotely, per Harness.

## R4. Probing a remote tool by reading its output is unsafe

Three defects, each of which put a false PASS into the research record before it
was caught. All three are transport artefacts, not Harness behaviour, and none
can occur on one machine. `[remote]`

**Multiplexing.** With `ControlMaster` set in the Client's `~/.ssh/config` and a
stale socket, ssh prints

```
mux_client_request_session: read from master failed: Connection reset by peer
```

*instead of* running the command. A check that reads stdout accepts that text as
a version string. Three tools reported as installed without being executed.
Fixed by forcing `ControlMaster=no` and `ControlPath=none` on every call: the
capture must not inherit the operator's ssh config.

**Shared stream.** Anything ssh writes on its own behalf — multiplexing errors,
host-key warnings, banners — arrives on the same stream as the command's output.
Scanning that text for `not found` reads ssh's error as proof the tool exists.
Presence is now decided by `command -v`'s exit code.

**Masked exit code.** `CMD --version 2>&1 | head -3` returns *head's* status. The
tool's exit code is discarded, and the merged stderr supplies non-empty output.
A Python that could not start passed as installed, with its crash message
printed as the version. Fixed by not piping, and by reading stdout alone — every
tool here prints its version there, so a stub that dies leaves stdout empty.

The general rule: **presence, runnability and transport health are three
questions.** Answering one and reporting it as another is how a capture run
fabricates a result.

## R5. A missing Harness and a broken one are different records

The manifest recorded `Hermes: NOT INSTALLED` when Hermes was installed and
would not start. That is a fabricated finding with the wrong cause attached.
The manifest now distinguishes the two states.

## R6. Pi reaches Ollama through the same extension as LM Studio

Pi has no `--base-url` flag and no built-in entry for either local Vendor. Both
are reached the same way: an extension loaded with `-e` that calls
`registerProvider` with an `openai-completions` api and a `baseUrl`. Only the
port and the provider name differ. `[desktop]`

```
provider  model            context  max-out
ollama    qwen3.5:9b       32.8K    4.1K
```

A one-shot `--mode json` run exits 0 and emits 41 frames naming `ollama` as the
provider. The `apiKey` is required by the schema and ignored by both Vendors.

Two differences matter for the Daemon:

- Ollama's OpenAI-compatible `/v1/models` carries **no capability field**, so
  tool support cannot be read from it. LM Studio reports
  `trained_for_tool_use`. Absent is not false, and a discovery routine that
  treats it as false will hide every usable model on an Ollama Host.
- Ollama's `/v1/models` also drops the context length, which LM Studio reports.
  The native `/api/tags` carries parameter size and quantisation instead.

**Corrected 2026-08-30. The paragraph that stood here was wrong, and it was an
inference from `[desktop]` rather than an observation.** It said LM Studio is
GUI-first, cannot be brought up over SSH, and that a Host reached only over SSH
is an Ollama Host in practice. Two later captures on this Host disprove it.
`[remote]`

- The 2026-08-25 `pi-vendors` pass drove LM Studio at `127.0.0.1:1234` on the
  Host, all stages exit 0, the same as Ollama and llama.cpp.
- The 2026-08-27 OpenCode run records
  `gate1_tool_call_on_host: pass`, noting
  `Host Victor@ZenitoBurrito:22 over SSH, no TTY, supervisor owned stdin;
  Vendor lmstudio`. See `captures/opencode/lmstudio/gates.json`.

What is true is narrower and is not about reach. LM Studio's server is started
from its own application rather than from a CLI, which is a one-time setup step
on the Host. Once it is serving it keeps serving, and it is then reachable over
SSH like any other Vendor on loopback. It survives across Sessions and across
reconnects.

Nothing in the design depended on the wrong version, because the Daemon never
starts a Vendor: a Vendor is reachable exactly when a call to it succeeds. The
lesson is the one this file keeps making. A `[desktop]` observation plus a
plausible inference is not a `[remote]` finding, and this one sat unchallenged
for five days next to captures that already contradicted it.

## R7. Pi's event vocabulary is the same across all three Vendors

Three full passes on the Host, same wizard, same prompts, same model family,
only the Vendor changing. Artefacts in
[`captures/pi-vendors/`](./captures/pi-vendors/). `[desktop]`

| Vendor | Endpoint | Model | Stages |
| --- | --- | --- | --- |
| LM Studio | `:1234` | `qwen/qwen3.5-9b` | all exit 0 |
| Ollama | `:11434` | `qwen3.5:9b` | all exit 0 |
| llama.cpp via llama-swap | `:8080` | `qwen3.5-9b` | all exit 0 |

**Identical where it matters.** All three emit the same 23 event types, and
nothing appears under one Vendor that does not appear under the others:

```
agent_start agent_end agent_settled session turn_start turn_end
message_start message_update message_end
text text_start text_delta text_end
thinking thinking_start thinking_delta thinking_end
toolcall_start toolcall_delta toolcall_end
tool_execution_start tool_execution_update tool_execution_end
```

`stopReason` takes the same three values everywhere — `pending`, `stop`,
`toolUse` — over the same raw values `stop` and `tool_calls`. All three called
the `bash` tool on the tool-forcing prompt, and all three reported
`agent_settled` in `--mode json` and `--mode rpc` alike.

So the Event model can be written once. Vendor identity belongs in metadata, not
in the event schema.

**Different in the accounting, and it cannot be compared across Vendors.**

| | LM Studio | Ollama | llama-swap |
| --- | --- | --- | --- |
| `thinkingSignature` | `reasoning_content` | `reasoning` | `reasoning_content` |
| `usage.input` | 3012 | 3007 | 24 |
| `usage.cacheRead` | 0 | 0 | 2986 |
| `usage.reasoning` | 51 | 0 | 0 |
| extra key | — | — | `responseModel` |

Three traps for anything that reads these numbers:

- **`usage.reasoning` is not trustworthy.** All three produced thinking
  content; only LM Studio counted it. Zero means "not reported", not "none".
- **`input` and `cacheRead` are not comparable.** llama-swap counted 24 input
  tokens against a 2986-token cached prefix for the same prompt the others
  charged ~3010 input for. Totals agree, the split does not. Summing `input`
  across Vendors measures nothing.
- **`thinkingSignature` is Vendor-specific.** Keying anything off the string
  breaks the moment the Host changes Vendor.

**Reaching them is uniform.** All three are OpenAI-compatible endpoints declared
in `~/.pi/agent/models.json` — a `baseUrl`, an `api`, an `apiKey` that no local
Vendor checks. No extension, no `--base-url`, no per-Vendor code. Every run used
`pi -ne`, so nothing else was loaded into the process.

Only discovery differs, and llama-swap is the awkward one: it fronts several
Models, so a bare `/props` answers HTTP 404 with `no model id could be
identified`, and the per-Model view is at `/upstream/<model>/props`. See
[ADR-0002](../adr/0002-llama-swap-is-the-llama-cpp-vendor.md).

## R8. A 200 is not proof the artefact arrived

The three passes recorded `HTTP 200` for every Vendor model listing and wrote
none of them. The capture directories have no `vendor-models.json` at all.
`curl-errors.log` holds the reason:

```
curl: (23) Failure writing output to destination, passed 637 returned 4294967295
```

A native curl was handed an MSYS path (`/c/Users/...`) it could not open, so it
fetched the body and threw it away. The status line was true and the artefact
was absent. The fetch helper checked the status only.

Two fixes, and both were needed: pass `winpath` so curl gets a path it can
write, and check the file after the fetch rather than the status line. A run
now reports `HTTP 200 but NO FILE WRITTEN` instead of a silent gap.

**Closed 2026-08-31.** The bodies exist and are checked in, at
[`captures/ollama-vendor/`](captures/ollama-vendor/) with a table saying which
endpoint each one came from. Six of them, from a real Ollama v0.33.2 on loopback:
`/api/version`, `/api/tags`, `/v1/models`, `/api/ps` loaded and empty, and three
`/api/chat` answers for load, unload and a Model that does not exist. They are
copied into `internal/vendors/testdata/ollama/` and replayed through a
caller-supplied `http.RoundTripper`, so the tier-two tests for `vendors` open no
socket. Recording them cost a `curl` and a save, exactly as `SPEC.md` predicted
once M3 had Ollama running anyway.

This is the same fault as R4 in a new place. Across this exercise the capture
has claimed success from a multiplexing error, from a pipeline's exit code,
from a launcher stub that never ran, and now from a status line. **Every one of
them was a check that read something adjacent to the thing it was meant to
verify.**

---

## What this run establishes, and what it does not

Establishes: a second machine reached over SSH key auth, a Vendor reached
without the WSL2 virtual-adapter hop that flattered the earlier runs, and one of
the two Harnesses driven end to end remotely.

On the Vendor, note what the improvement actually is. The earlier captures ran
the Harness inside WSL2 and crossed a virtual adapter at `172.25.112.1` to reach
the Vendor on the Windows side. Here the Harness and the Vendor sit on the same
Host and the hop is gone — the Vendor answers on loopback and is not exposed to
the network at all. That is what the design wants, since the Daemon is
co-located with the Vendor. Exposing the port would have tested a path the
architecture never uses.

The capture records reachability from three vantage points — Host loopback, Host
NIC, and the Client — because they answer different questions. A check run only
on the Host tests the socket binding and never touches the firewall, so it
cannot say whether anything off-box could connect.

Does **not** establish: the Host is Windows, not bare-metal Linux. The Hermes
stdin deadlock (C7) and phantom denial (C9) were disproved only under WSL2, and
this run does not lift that caveat. A native Linux kernel and its pipe
implementation remain untested.
