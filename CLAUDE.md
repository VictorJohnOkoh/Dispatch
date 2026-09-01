# Dispatch — Agent Guide
I am Victor, a second year Computer Science and AI student and I am tryign to improve my architecture design and software engineering skills 
I like simple solutions and understandable code
Make sure that when deciding with how you write out feature that you always choose the more efficient and imple to understand and maintain option
When choosing a data structure I prefer using only what is necessary. 
Example:
- Using a hash map when the data needed is a known certain amount and the data structure is expected to be fully populated. <br>
Comments are very nice but they should be used quite sparingly and only when they add value. They should only bring value to the codebase, they should only be usedexplanining something that isn't obvious or self-explanatory. 
 <br>
Good Example:
- For a function that adds 3 numbers together should be commented with "# adds 2 numbers together and returns the sum" <br>
Bad Example:
- For a function that adds 3 numbers together "Takes in 3 integers, performs the addition operation and returns the result as an integer" 
Bad Example for a variable/constant: type Support uint8
- // Support is a Vendor's answer about one Capability. Unknown is an answer and not a
- // missing value, so nothing here is a pointer and no caller writes a nil check.
- // Unknown is the zero value, which makes an unfilled Capabilities honest rather
- // than wrong.
When adding a new feature to an existing codebase publish your changes to a branch and open a Pull Request instead of just comminting to main
## Speech Pattern
When speaking always talk in ASD-STE100 Simplified Technical English, read CONTEXT.md and use the ubiquitous language.
Dumb down complex topics/options afterwards by describing them using more colloquial language
## Agent skills

### Issue tracker

Issues live as GitHub issues on `VictorJohnOkoh/Dispatch`, managed via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

The five canonical triage roles, each label string equal to its name. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.

Read `CONTEXT.md` first. It is the ubiquitous language and it is small.

Each ADR's title is its decision, so the table below is the decision. Treat it as the
answer. Open the full file only when you change that area, and expect to pay the size
in the last column when you do.

| ADR | Decision | Open it to change | Size |
|---|---|---|---|
| 0001 | A resident Daemon on each Host owns Session lifecycle, not the SSH connection | Daemon startup, SSH, transport | 1 KB |
| 0002 | llama-swap is how llama.cpp becomes a Vendor, and the Daemon still owns admission control | llama.cpp, VRAM admission | 3 KB |
| 0003 | OpenCode replaces Hermes as the second Harness, and Hermes becomes a test fixture | the Harness list, test fixtures | 6 KB |
| 0004 | The Hub owns a four-state Host State, and presence is connection liveness | Hub, Host State, presence | 12 KB |
| 0005 | Fourteen Event kinds (0008 raised it to sixteen), a per-Daemon sequence number, and text that streams as Deltas the log never keeps (the transcript bound it asks for is in `SPEC.md`) | the Event model, Deltas, the log | 28 KB |
| 0006 | The Daemon owns the Harness process and the Adapter owns the conversation | Harness Adapters, process supervision | 28 KB |
| 0007 | The Vendor Adapter has no Health method, and every capability it reports is three-valued (`SPEC.md` says who calls `Load`, and that nothing calls `Unload`) | Vendor Adapters, capability reporting | 33 KB |
| 0008 | Five Session states, one process each, and a gate that claims only what the Daemon allowed (its ladder's "kill the process group" needs a Job Object on Windows, see `SPEC.md`) | Session lifecycle, admission, Approval Policy | 44 KB |
| 0009 | One protocol that differs by a Host id, a merged stream to the Client, and an Event that is written before it is sent (`SPEC.md` makes the read path a second exception to that, so an open message reads whole) | the wire protocol, the SQLite log, replay, retention | 51 KB |
| 0010 | Four leaf packages, two roles in one binary, and a Host id the Daemon cannot import (`SPEC.md` adds the two per-OS `supervise` files and settles the first paint it deferred) | the package tree, imports, concurrency ownership, testing tiers, where config enters | 41 KB |
| 0011 | One binary runs both roles, and the Hub is the only place a second Host can be named | the role split, one binary against two, deployment | 6 KB |
| 0012 | A Harness reaches only its own Host's Vendor, and no type on the wire can say otherwise | cross-Host Sessions, the Data Plane, Vendor addresses | 4 KB |
| 0013 | Host Registration bootstraps SSH trust with one password login | Host Registration, SSH trust bootstrap, managed Hub keys | 6 KB |

Everything after 0004 is long. The thirteen together are about 70,000 tokens, which is why
they are indexed here and not read by default. 0011, 0012 and 0013 are the exceptions and
are short enough to open on a hunch.

### The build spec

`SPEC.md` at the repo root is the frozen v1 scope, the nine-milestone build order, the
thirteen behaviours that define done, and where test-first applies. Read it before
writing code.

It mostly assembles the ADRs rather than repeating them, with two exceptions worth
knowing. It owns the decisions no ADR made: the SQLite driver, the config format, the
Harness and Vendor counts, observability and the error taxonomy. And it owns **five
corrections** to ADRs 0005, 0007, 0008, 0009 and 0010, flagged on their rows above. Where the
two disagree, `SPEC.md` is later and wins, which is the same rule ADR 0009 and ADR 0010
already set when they corrected earlier ADRs.
