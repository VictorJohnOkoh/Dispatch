# Register a first Host from the Client

SPEC.md behaviour 13, ADR 0013 and issue #83. Follow [the installation instructions](../install.md)
without using steps from memory. Use a Windows Host with OpenSSH, SFTP and password login turned on.
Do the run twice: once with a standard local account and once with an administrator account.

## Run

1. Build the same binary for the Host and the Hub. Install the Daemon as documented.
2. Start the Daemon with `-host-reg host:22`. Confirm that a code appears. For the administrator
   account, confirm that the Daemon logs the administrator warning. Record the Windows and OpenSSH
   versions.
3. Start the Hub with no `hub.json`. Open `http://127.0.0.1:7700/hosts`, choose a Host id, paste the
   code and type the account password.
4. Confirm that the Host appears without a Hub restart. Start a Session through the Client.
5. Find the file that holds the Hub key: `~\.ssh\authorized_keys` for the standard account, and
   `C:\ProgramData\ssh\administrators_authorized_keys` for the administrator. The last line must be
   the `dispatch-hub` line with `restrict,port-forwarding,permitopen="127.0.0.1:7717",command="exit 1"`.
   Unrelated keys and comments must still be there.
6. For the administrator, run `icacls C:\ProgramData\ssh\administrators_authorized_keys`. Only
   SYSTEM and Administrators may have access, and inheritance must be off.
7. From the Client machine, try the Hub key directly:
   `ssh -i $env:LOCALAPPDATA\Dispatch\ssh\id_ed25519 USER@host`. It must not give a shell. Then
   `ssh -i ... -N -L 7800:127.0.0.1:7717 USER@host` must work, and a tunnel to
   `127.0.0.1:22` through the same key must be refused when you connect through it.

## Failure runs

- Change a character in the code. The Hub must refuse it before any network connection.
- Use a code from another Host, or put a different key for the address in `known_hosts`. The Hub
  must refuse before it sends the password. Check the Host's sshd log: no password attempt from
  the Client machine. Correcting the address must not get around this check.
- Type a wrong password. Nothing may change on the Host.
- Stop the Daemon after the code is printed, then register. The Handshake fails, and the key file
  must be back the way it was.
- Make `hub.json` read-only, then register. The key file and the Hub's `known_hosts` must be back
  the way they were.
- Register the same Host again after deleting it from `hub.json`. The key file must hold one
  `dispatch-hub` line, not two.
- Use IPv6 and a non-default SSH port, then try an unreachable address. The error must be useful.
- Open the Hub from a different browser origin, or send non-JSON or oversized input. The Hub must
  refuse the registration. The password must not appear in a URL, browser storage, the Hub log or
  `hub.json`.

## Runs

| Date | Commit | Windows / OpenSSH | Result |
| --- | --- | --- | --- |
| 2026-09-07 | working branch for #123 | workspace OpenSSH 9.5p2, sshd stopped | Real standard-account Host run pending; automated Go SSH and recovery checks are separate evidence. |
| 2026-09-08 | working branch for #123 | Windows ACL fixtures | Permission checks passed for delete, change-permissions, take-ownership and delete-child grants. This does not complete the SSH Host run. |

The two runs above tested the code-only flow that ADR 0013 replaced on 2026-09-24. Do not mark this
real-machine check as passed from an in-process SSH rig. Record each missing or unclear installation
step before changing the instructions.
