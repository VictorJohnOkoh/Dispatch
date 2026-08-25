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

---

## What this run establishes, and what it does not

Establishes: a second machine reached over SSH key auth, a Vendor reached over a
real network interface rather than a WSL2 virtual adapter, and one of the two
Harnesses driven end to end remotely.

Does **not** establish: the Host is Windows, not bare-metal Linux. The Hermes
stdin deadlock (C7) and phantom denial (C9) were disproved only under WSL2, and
this run does not lift that caveat. A native Linux kernel and its pipe
implementation remain untested.
