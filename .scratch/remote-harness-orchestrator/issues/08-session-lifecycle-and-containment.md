# Session lifecycle, admission and containment

Type: grilling
Status: open
Blocked by: 05

## Question

Define the Session state machine the Daemon supervises, the admission seam in front of it, and the containment rules around it. Three concerns, one ticket, because they share a boundary: they are all things the Daemon does to a Session that the Harness knows nothing about.

**Lifecycle**

- What are the states and transitions? Provisional set to attack: requested, admitted, starting, running, awaiting approval, stopping, stopped, crashed.
- **A Session is one process on both Harnesses — but a Harness process can outlive a Session.** [Hermes and Pi control surfaces](./01-hermes-and-pi-control-surfaces.md) corrected the earlier assumption that Hermes needed a long-lived server: both are one subprocess. But ACP puts `session/new`, `fork`, `list` and `resume` *in-protocol*, so one `hermes acp` process can hold several Sessions. Decide whether the Daemon uses that or starts a process per Session, and say what it costs either way.
- **A Session that looks hung may be waiting on its supervisor.** Hermes' Windows deadlock always resolved within ~6s of the *client's own* timeout across eight runs — raising the timeout raised the hang by the same amount. So the supervision timeout is not a neutral observer of Session health. Decide what the Daemon concludes from a silent Harness, and how it avoids treating its own impatience as evidence.
- Which transitions produce Events, and which are internal?
- What does the Daemon do when a Harness crashes? Restart, or surface and stop? Does the answer differ by how far the Session had progressed?
- What survives a Daemon restart? The Event log persists, but the child process does not. Is a Session resumable, or does it become terminal with its history intact? Be explicit — this is the difference between a nice property and a lie in the UI.
- Who may stop a Session, and is stopping graceful, forced, or graceful-then-forced?

**Admission**

- Define the policy interface, then implement only "one Session at a time" behind it. What does the interface need to take so a later VRAM-aware policy fits without changing its shape?
- What does a rejected Session start look like to the user — an error, or a queue position?
- Is admission per-Host or global across the Hub? Given Daemons are ignorant of each other, presumably per-Host — confirm, and note what that means for a user driving three Hosts at once.

**Containment**

- Workspace Root is configured per Host, and no Session may operate outside it. Where is that enforced, and against what — the working directory only, or every path the Harness touches? Be honest about what is actually enforceable given the Harness runs as a normal process.
- Symlinks, `..` traversal, and relative paths. Path validation that is not resolution-aware is decoration.
- Approval Policy is per-Session: `auto`, `prompt`, `deny`. Who sets it, can it change mid-Session, and what is the default?
- **Both Harnesses can gate, and neither reports a refusal the Daemon can trust.** Hermes reports a human's refusal and an internal failure identically (`"denied by ACP client"`); Pi reports refusal only as a tool error with free text chosen by the extension author. So the Approval Policy must **record its own decisions** and treat Harness output as corroboration. Decide where that record lives, and whether it is itself an Event.
- **Hermes fails open and closed at the same time, in different subsystems.** Its `pre_tool_call` shell hook returns "no block" on every failure path — crash, missing, non-executable, timeout, non-zero exit — so a broken hook silently permits. Its ACP edit path does the opposite and can deny without ever asking. Same product, opposite defaults, so a guarantee proven for one does not transfer to the other. Decide which surface the Daemon gates on, and what it does when the Harness answers a question it was never asked.
- **The shell-hook path needs consent the Daemon cannot give interactively.** Hermes records first-use hook consent in an allowlist, and a non-TTY caller — which the Daemon is — must pass `--accept-hooks` or the hook never registers at all. A gate that silently fails to install is worse than no gate; decide how the Daemon verifies its own approval path is live before admitting a Session.
- **Pi's gate has two correlation traps.** Its approval request carries **no tool-call id** — the command is embedded in a display string, so correlation is by ordering — and `tool_execution_start` fires *before* the gate resolves. A start Event is not evidence that anything ran. Decide what the Session state machine may conclude from a start.
- **Hermes writes `trajectory_samples.jsonl` into the working directory.** A Harness that drops files into the user's workspace is a containment concern even though it isn't an escape. Decide whether Workspace Root policy has anything to say about Harness-generated artefacts.

Use `/grilling` and `/domain-modeling`.
