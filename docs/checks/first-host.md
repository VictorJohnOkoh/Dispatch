# Put a Daemon on a Host that has never had one

`SPEC.md` behaviour 13. Follow only the written instructions and type nothing from memory.

**This one gets skipped unless it is called out, so it is called out.** It fails if the install
instructions do not exist, which is the point of writing it as a behaviour rather than as a task. The
person who wrote the code cannot run it from memory and get a real answer.

## What you need

- A machine that has never run a Daemon. A virtual machine you made this morning counts, and it is the
  easier way to get one twice.
- [docs/install.md](../install.md), open, and nothing else.

## The rule

Type nothing that is not on the page. When you reach a step you cannot do from the page alone, **stop
and write it down** before you look anything up. That note is the result of this check. A run where
you helped yourself past a gap has measured nothing.

`dispatch host add` is what ADR 0013 specifies and it is not in the build yet, so steps 6 to 8 are the
manual fallback, and this check covers the fallback as written.

## The run

Work down `docs/install.md` from the top:

1. Turn on the SSH server on the Host.
2. Build the binary.
3. Write `daemon.json`.
4. Copy both files, and compare the checksum.
5. Start the Daemon and read its first two lines.
6. Give the Hub a key.
7. Record the Host's own key.
8. Write `hub.json`.
9. Start the Hub and reach the Host.
10. Check the Event stream.

## What has to be true

- Both `curl.exe` commands in step 9 answer. The first from the Hub, the second from the Daemon down
  the tunnel.
- You did not edit `authorized_keys`, `known_hosts` or `hub.json` from memory. The page told you
  where each line goes, including which of the two `authorized_keys` files an administrator account
  uses.
- Nothing in the run needed a file the page did not name.
- Start a Session on the new Host at the end. Reaching `/hosts` is not the same as the Host working.
- Every place you had to stop is written down. Then file each one as its own issue against
  `docs/install.md`, rather than fixing the page while you are inside the run.

## Runs

| date | commit | OS | where it stopped | held |
| --- | --- | --- | --- | --- |
