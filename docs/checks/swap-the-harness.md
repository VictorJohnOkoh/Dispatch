# Swap the Harness and change nothing else

`SPEC.md` behaviour 10. The same Model, the same prompt, the same Host, and the transcript renders the
same way for both OpenCode and Pi.

Half of this is already a test. `TestPiAndOpenCodeRenderTheSameWay` in `internal/harness` replays a
recorded run of each and compares what the Daemon was told. It compares which Kinds appear. It cannot
compare how a page looks, and this check is the half that can.

## What you need

One Host with both `opencode` and `pi` in its `daemon.json`, one Model both can reach, and a prompt
that makes a Model use a tool. "Read the README in this directory and tell me what this project is"
does it: a read, then a message about it.

## The run

1. Start a Session with OpenCode, on the Model, in a directory you pick.
2. Submit the prompt. Let it finish. Leave the page open.
3. Start a Session with Pi. **The same Model, the same directory, the same policy.**
4. Submit the same prompt, word for word.
5. Put the two pages side by side.

## What has to be true

- Both transcripts hold the same kinds of thing: reasoning where the Model reasons, an assistant
  message, a Tool Call that is requested and then ends, and one completion.
- Every Tool Call is requested before it ends, in both. A call that ends without being requested
  leaves the page with nothing to attach a result to.
- Each prompt ends once, on a completion, in both.
- Neither page carries anything that names the Harness. Nothing above this seam should be able to tell
  you which one ran, and if you can tell, write down what gave it away.
- Approvals hold in both. Set `edit` to `wait` and run the pair again with a prompt that edits: the
  question arrives the same way and the answer releases it the same way.

The answers will differ. That is the Model, not the Harness, and it is not what this check is about.
What has to match is the shape.

## Runs

| date | commit | Model | what was seen | held |
| --- | --- | --- | --- | --- |
