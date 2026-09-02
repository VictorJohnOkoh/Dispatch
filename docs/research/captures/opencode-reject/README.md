# OpenCode refuses a Tool Call — 2026-09-02

The answer to issue #46's open question: what `reject_once` does.

Every capture in [`../opencode/`](../opencode/README.md) answered allow, so the
Adapter's refusal path was an assumption. These bytes settle it.

Produced by `bash scripts/capture-opencode-host.sh --reject`, driving
`opencode acp` 1.18.25 on the Host `ZenitoBurrito` over SSH, against Ollama
serving `qwen3.5:9b`. One run, one class: an edit, refused.

The gates are not counted here. A refused Tool Call changes the counts, and
[`../opencode/`](../opencode/README.md) is where the gates are answered.

## The finding

**`reject_once` is offered, and its `optionId` is `reject`.** The gate carried
three options — `allow_once` as `once`, `allow_always` as `always`, and
`reject_once` as `reject`. The Adapter matches on `kind` and sends the option's
own id, so it does not depend on that spelling.

**A refusal is an ordinary end to a Prompt.** OpenCode moved the call to
`failed`, ended the turn with `stopReason: "end_turn"`, and exited 0. The Prompt
did not error, did not retry, and did not ask a second time. The Adapter needs no
special path for a refusal, and `internal/harness/acp_test.go` replays these
frames to say so.

**The failed call carries a reason.** Its `content` and its `rawOutput.error` both
say `The user rejected permission to use this specific tool call.` The Adapter
already passes that text to `ToolCallEnded`, so the Client is told why the call
ended and does not have to guess from the outcome.

**The refusal held.** `refused.txt` is absent from `workdir-after.txt`. The
delegated write never arrived, so nothing reached the disk.

**The call was `in_progress` before the question.** The order on the wire is
`tool_call` (pending), `tool_call_update` (`in_progress`), then
`session/request_permission`. This is the capture that shows why the rule matters:
an Adapter that reported `in_progress` would have drawn a write that the human
refused and the disk never received.

**This refused turn carried no message.** The run produced 19
`agent_thought_chunk`s and no `agent_message_chunk` at all. One run says nothing
about every run, but it is enough to show that a Client which waits for text
before it ends a turn can wait forever. `Completed` is what ends a turn.

## The frames

| file | what it is |
| --- | --- |
| `manifest.txt` | the run's own record: Host, Client, Vendor, Model, versions |
| `reject-frames.jsonl` | every JSON-RPC frame, both directions, in order |
| `reject-raw.log` | the agent's raw stdio |
| `reject-stderr.log` | the agent's stderr — empty |
| `reject-summary.json` | `permission_answers` says the gate was answered `reject_once` |
| `harness-resolution.json` | every spawn attempted, and which ones a supervisor can use |
| `config-discovery.txt` | `opencode models` run from the Session's working directory |
| `session-opencode.json` | the per-Session config written beside the working directory |
| `workdir-after.txt` | what the Session left on the Host's disk — `refused.txt` is absent |

## Caveats

- **One Vendor, one Model, one run.** Ollama and `qwen3.5:9b`. A second Vendor was
  not asked, because the gate is OpenCode's and not the Vendor's.
- **`reject_always` was never sent.** The Adapter only ever sends `reject_once`,
  so the capture sends only that. What a blanket refusal does is still unknown.
- **One class.** The edit was refused. An execute refusal is not captured, and
  `read` has no gate to refuse.
- **The empty message is one run.** No `agent_message_chunk` arrived here. Whether
  a refused turn always ends without text needs more than one Model to say.
- These bytes prove what OpenCode 1.18.25 said in September 2026 and nothing about
  a later version. Re-capturing is a recurring task.
