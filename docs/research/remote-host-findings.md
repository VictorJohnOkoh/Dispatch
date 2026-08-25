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

This matters for a remote Host because LM Studio is GUI-first — its server is
started by hand from a desktop session and cannot be brought up over SSH.
Ollama runs headless. A Host reached only over SSH is therefore an Ollama Host
in practice, whatever the local captures used.

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
