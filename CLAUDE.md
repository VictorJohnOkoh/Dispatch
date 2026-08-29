# Remote Harness Orchestrator — Agent Guide
I am Victor, a second year Computer Science and AI student and I am tryign to improve my architecture design and software engineering skills 
I like simple solutions and understandable code
Make sure that when deciding with how you write out feature that you always choose the more efficient and imple to understand and maintain option
When choosing a data structure I prtefer using only what is necessary. 
Example:
- Using a hash map when the data needed is a known certain amount and the data structure is expected to be fully populated. <br>
Comments are very nice but they should be used sparingly and only when they add value. <br>
Good Example:
- For a function that adds 3 numbers together should be commented with "# adds 2 numbers together and returns the sum" <br>
Bad Example:
- For a function that adds 3 numbers together "Takes in 3 integers, performs the addition operation and returns the result as an integer" 
## Speech Pattern
When speaking always talk in ASD-STE100 Simplified Technical English, read CONTEXT.md and use the ubiquitous language.
Dumb down complex topics/options afterwards by describing them using more colloquial language
## Agent skills

### Issue tracker

Issues live as GitHub issues on `VictorJohnOkoh/Capstone`, managed via the `gh` CLI. See `docs/agents/issue-tracker.md`.

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
| 0005 | Fourteen Event kinds, a per-Daemon sequence number, and text that streams as Deltas the log never keeps | the Event model, Deltas, the log | 28 KB |
| 0006 | The Daemon owns the Harness process and the Adapter owns the conversation | Harness Adapters, process supervision | 28 KB |
| 0007 | The Vendor Adapter has no Health method, and every capability it reports is three-valued | Vendor Adapters, capability reporting | 33 KB |
| 0008 | Five Session states, one process each, and a gate that claims only what the Daemon allowed | Session lifecycle, admission, Approval Policy | 44 KB |
| 0009 | One protocol that differs by a Host id, a merged stream to the Client, and an Event that is written before it is sent | the wire protocol, the SQLite log, replay, retention | 51 KB |

Everything after 0004 is long. The nine together are about 54,000 tokens, which is why
they are indexed here and not read by default.
