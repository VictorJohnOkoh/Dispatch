# Point a Session at a directory outside the Workspace Root

`SPEC.md` behaviour 9. It is refused before the Session exists. Then have the Harness delegate a write
outside it, and see that refused too.

Two halves, and they are refused by two different pieces of code. Run both.

## What you need

A Host whose `workspaceRoot` you know, and a directory outside it that really exists. A directory that
is not there would be refused for the wrong reason and prove nothing.

On Windows, `C:/Users/victor/work` as the Root and `C:/Users/victor/Desktop` as the outside directory.

## The first half: the Session's own directory

1. Open `/new`, pick a Host, and type the outside directory as the working directory.
2. Start it.

What has to be true:

- The start is refused, and the message says `outside the Workspace Root` and names the path.
- No Session exists. Nothing appears in the rail, and no Event was written. The containment check runs
  before the Session, like admission does.
- Try `..` as well: a path inside the Root with enough `..` in it to climb out. It is refused the
  same way, because the path is resolved before it is compared.
- Try a symlink or a junction inside the Root that points outside it. Refused too; a link is resolved,
  not followed on trust.

## The second half: a write the Harness delegates

This half needs a Harness that asks the Daemon to write for it. **OpenCode does, over ACP. Pi does
not:** it runs its own tools and delegates no write, so its own file access is bounded by the working
directory alone and this half cannot be run on it.

1. Start a Session with OpenCode, in a directory inside the Root.
2. Prompt it to write a file outside the Root. Name the absolute path in the prompt.

What has to be true:

- The write is refused, and the Session survives the refusal.
- Nothing was written. Check the outside directory from a shell on the Host, not from the Client.
- The refusal reaches the transcript as the Harness being told no, and the Session takes another
  prompt afterwards.

## Runs

One row per half.

| date | commit | half | what was seen | held |
| --- | --- | --- | --- | --- |
