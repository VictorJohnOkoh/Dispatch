# Put a Daemon on a Host that has never had one

`SPEC.md` behaviour 13. Follow only the written instructions and type nothing from memory.

**This one gets skipped unless it is called out, so it is called out.** It fails if the install
instructions do not exist, which is the point of writing it as a behaviour rather than as a task. The
person who wrote the code cannot run it from memory and get a real answer.

## What you need

- A machine that has never run a Daemon. A virtual machine you made this morning counts, and it is the
  easier way to get one twice.
- [docs/install.md](../install.md), open, and nothing else.

## Half of this behaviour holds now, and the sheet says which half

`SPEC.md` behaviour 13 is `dispatch host add`: one command that shows you the Host fingerprint, takes
the account password once, and writes `hub.json` itself. It ends "the user does not edit an
authorized-key file, `known_hosts` or `hub.json`."

**The command is in the build for a standard local Windows account** (#80). An administrator account
uses a shared key file with an ACL of its own, and that half is #81 to #83. Until those land,
behaviour 13 does not hold for an administrator account however well this run goes.

So there are two runs on this sheet. A standard account runs the command, and that run can hold. An
administrator account runs the manual fallback, steps 6 to 8 of `docs/install.md`, and that run is
recorded as **not held** whatever else you saw. Both measure the same thing: whether a person who has
never done it can put a Daemon on a new machine from the page alone.

## The rule

Type nothing that is not on the page. When you reach a step you cannot do from the page alone, **stop
and write it down** before you look anything up. That note is the result of this check. A run where
you helped yourself past a gap has measured nothing.

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

A standard account does 6, 7 and 8 as the one `dispatch host add` command the page names between
step 5 and step 6. An administrator account does the three by hand.

## What has to be true

- Both `curl.exe` commands in step 9 answer. The first from the Hub, the second from the Daemon down
  the tunnel.
- On an administrator account, every line you put in `authorized_keys`, `known_hosts` and `hub.json`
  came off the page, including which of the two `authorized_keys` files that account uses. You edited
  all three by hand, which is the fallback working and behaviour 13 still not holding.
- On a standard account you edited none of the three, and the command refused to write `hub.json`
  until key-only SSH and the Handshake both answered.
- Nothing in the run needed a file the page did not name.
- Start a Session on the new Host at the end. Reaching `/hosts` is not the same as the Host working.
- Every place you had to stop is written down. Then file each one as its own issue against
  `docs/install.md`, rather than fixing the page while you are inside the run.

On a standard account the run is the command: one fingerprint to confirm, one password, and none of
the three files touched by hand. That is the run that can hold, and `authorized_keys`, `known_hosts`
and `hub.json` are read afterwards rather than written.

## Runs

| date | commit | OS | where it stopped | held |
| --- | --- | --- | --- | --- |
