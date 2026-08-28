# OpenCode ACP on a Host

Type: research
Status: open
Blocked by: —

## Question

[ADR 0003](../../../docs/adr/0003-opencode-replaces-hermes-as-the-second-harness.md) removes Hermes from v1 and names OpenCode as the second Harness, but only conditionally. This ticket supplies the evidence, or refuses it.

Capture `opencode acp` on the **remote Host, over SSH**, against a local Vendor. Not on the development machine. The development machine is where Hermes looked healthy; the Host is where it died.

The hypothesis is `opencode acp`, and the capture exists to falsify it. `opencode serve` was not chosen and is not a fallback, because it would add a second integration shape to a seam that currently has one. `opencode run` fails the Session definition outright.

What is known about OpenCode today is a version string (1.18.9), a `--help` listing, a `permission` block found inside the shipped binary, and a working `provider` config on the development machine that reaches LM Studio over loopback. Under this project's own rule, none of that is evidence. Treat every line of it as a claim to break.

### The three gates

A gate that fails changes v1. Record the result even when it is inconvenient.

1. **A tool call completes on the Host over SSH.** `opencode acp` starts under a supervisor that **owns stdin rather than inheriting it**, and a real tool call runs to completion. Both existing Harnesses trap on stdin in opposite directions, so this is the first thing to get wrong. Failure here is fatal: v1 ships Pi alone.
2. **`session/request_permission` fires for every tool class OpenCode can gate.** The Daemon owns the Approval Policy ladder and the Harness runs its own tools, so this method is the only lever the policy has. A gateable class that never asks is written `deny` in OpenCode's `permission` block, so it is refused rather than ungated. **`read` is exempt**: OpenCode's permission block takes `edit`, `bash` and `webfetch` and has no key for reads, so a silent read is a Harness with no gate rather than a Harness skipping one, and the `deny` recovery would leave a Harness that cannot read a file. The exemption is never silent — `read` is still counted and reported, and the cost is recorded: the Approval Policy cannot honour wait or refuse for reads, so Workspace Root is the only thing bounding what a Session reads.
3. **Terminal Events are known per tool class, with counts.** This asks for knowledge, not perfection. A quiet class does not block v1, because the adapter synthesises the terminal Event. An unknown class does block it. Match the rigour that produced Hermes' 12/12.

### Also record, but do not gate on

- **All three Vendors.** LM Studio, Ollama and llama-swap, each driven to a tool call. A Vendor that fails is a configuration bug, not grounds to reject the Harness.
- **Where OpenCode reads configuration from.** ADR 0003 assumes the Daemon can write a per-Session `provider` block beside the Session's working directory. Confirm the discovery order and precedence. Two config files already exist on the development machine and a project-level one is possible, so this is not obvious. If working-directory discovery does not work, per-Session Model choice dies and configuration falls back to a manual per-Host prerequisite.
- **The ACP method set.** Which client methods OpenCode calls, and whether it delegates any tool execution to the client. Hermes delegated nothing (C6). If OpenCode does delegate, the Daemon gains a lever it does not currently have, and that changes #7.
- **Whether the Event vocabulary is Vendor-independent**, the property Pi was proven to have. If OpenCode's event names or `stopReason` values shift with the Vendor, the Event model cannot put Vendor identity in metadata.
- **Anything that looks like a hang.** A Harness that appears hung may be waiting on its supervisor. Vary the supervision timeout and check whether the hang moves with it before calling it a defect in the Harness.

### Capture hygiene

Four earlier captures claimed success they had not achieved: a stale SSH multiplexing socket read as a version string, a pipeline returning `head`'s exit code, a launcher stub that satisfied `command -v` without running, and an HTTP 200 recorded while curl wrote no file. Each read something *adjacent* to what it was meant to verify. Check the thing itself.

Freeze the capture bytes under `docs/research/captures/opencode/` and write the findings to `docs/research/opencode-acp-host.md`.

## Acceptance criteria

- [ ] Gate 1 answered: a tool call completed on the Host over SSH, with the supervisor owning stdin, or a recorded failure with the cause investigated
- [ ] Gate 2 answered: `session/request_permission` counted per tool class for `read`, `edit` and `execute`, with `read` exempt from failing the gate and the exemption recorded
- [ ] Gate 3 answered: terminal Events counted per tool class, with the quiet classes named
- [ ] Vendor coverage recorded for LM Studio, Ollama and llama-swap
- [ ] OpenCode's configuration discovery order confirmed, and the per-Session config assumption in ADR 0003 upheld or reversed
- [ ] The ACP client-method set recorded, including whether any tool execution is delegated
- [ ] Capture bytes frozen under `docs/research/captures/opencode/`, findings in `docs/research/opencode-acp-host.md`
- [ ] ADR 0003's conditional half resolved: OpenCode enters v1, or v1 ships Pi alone
- [ ] If OpenCode enters, `CONTEXT.md` and `docs/architecture-sketch.html` are updated to stop naming Hermes as a shipped Harness

## Blocking

#7 — [The Harness adapter interface](./06-harness-adapter-interface.md)
