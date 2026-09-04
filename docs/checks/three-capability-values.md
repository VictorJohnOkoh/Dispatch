# See all three Capability values in one Model list

`SPEC.md` behaviour 11. The Client draws `Unknown` as an answer rather than as a blank, and every
Session runs anyway.

Three values, and `Unknown` is the one that matters. It is an answer: nobody has said. Drawn as a
blank it becomes `No`, and a Model nobody asked about disappears from the picker.

## What you need

One Host with all three Vendors running and listed in its `daemon.json`:

```json
"vendors": [
  {"kind": "lmstudio", "base": "http://127.0.0.1:1234"},
  {"kind": "ollama", "base": "http://127.0.0.1:11434"},
  {"kind": "llamaswap", "base": "http://127.0.0.1:8080"}
]
```

Each has to hold at least one Model. This is the only check that needs all three at once, which is
why it is worth setting up once and running the other twelve on the same Host.

## Where each value comes from

| Vendor | What it answers | Why |
| --- | --- | --- |
| LM Studio | `Yes` or `No` | `trained_for_tool_use` is a boolean it really sends |
| LM Studio, older than 0.4.0 | `Unknown` | the listing carries no capabilities at all |
| Ollama | `Yes` for what `/api/tags` lists, `Unknown` for the rest | the list names what a Model can do and never what it cannot |
| llama-swap | `Unknown` until the Model is resident | the properties come from the running process, and nothing is running yet |

llama-swap is the one to watch. The same Model answers `Unknown` before a Session and answers
something else after one, and both answers are honest.

## The run

1. Open `/new` and go to the Model step. Every Model from all three Vendors is in one list.
2. Read the whole list.
3. Start a Session on a Model whose Tools answer is `Unknown`.
4. Come back to `/new` and read the llama-swap Models again.

## What has to be true

- All three values appear in the one list. Find a `Yes`, find a `No`, and find an `Unknown`.
- `Unknown` is **drawn**. It is a word on the page, not a gap, not a dash and not a missing row.
- A Model with `Unknown` capabilities can be chosen and the Session runs. Nothing is hidden or
  disabled for an answer nobody gave.
- Two Vendors serving the same Model id are told apart. The list names the Vendor beside the Model,
  because the id alone does not name a Model.
- After step 3, the llama-swap Model that was resident answers with something better than `Unknown`.
  The answer improved because the process is up, not because anything was cached.

## Runs

| date | commit | Vendors | what was seen | held |
| --- | --- | --- | --- | --- |
