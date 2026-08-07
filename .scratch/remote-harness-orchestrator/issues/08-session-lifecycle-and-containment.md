# Session lifecycle, admission and containment

Type: grilling
Status: open
Blocked by: 05

## Question

Define the Session state machine the Daemon supervises, the admission seam in front of it, and the containment rules around it. Three concerns, one ticket, because they share a boundary: they are all things the Daemon does to a Session that the Harness knows nothing about.

**Lifecycle**

- What are the states and transitions? Provisional set to attack: requested, admitted, starting, running, awaiting approval, stopping, stopped, crashed.
- **A Session is not always one process.** [Hermes and Pi control surfaces](./01-hermes-and-pi-control-surfaces.md) found that Hermes is best driven as a long-lived local server where Sessions are runs *inside* it, while Pi is one subprocess per Session. So "supervise the Harness" and "supervise the Session" are not the same lifecycle. Does the Daemon need a second, longer-lived supervision concept sitting underneath Sessions — and if so, who starts and stops it, and what happens to sibling Sessions when it dies?
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
- **The research answered the "what if a Harness has no hook" question with a yes.** Pi has no built-in approval mechanism, so requesting `prompt` on a Pi Session must either fail loudly at start or silently degrade — decide which, and note that silent degradation on a safety control is the worst available option.
- **Hermes' hooks appear to fail open** — the docs say hook failures never crash the agent. Fail-open is the wrong default for an approval gate. Decide whether the Daemon can detect and compensate, or whether this is a documented limitation of running Hermes under `prompt`. Confirm empirically first; this was flagged as needing a real install.
- **Hermes writes `trajectory_samples.jsonl` into the working directory.** A Harness that drops files into the user's workspace is a containment concern even though it isn't an escape. Decide whether Workspace Root policy has anything to say about Harness-generated artefacts.

Use `/grilling` and `/domain-modeling`.
