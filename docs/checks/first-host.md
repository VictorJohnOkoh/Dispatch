# Register a first Host from the Client

SPEC.md behaviour 13 and issue #123. Follow [the installation instructions](../install.md) without
using steps from memory. Use a Windows Host with OpenSSH and an enabled standard local account.
Run the Daemon and SSH as that account. Administrator and cross-account automatic registration
are outside this check.

## Run

1. Build the same binary for the Host and Hub. Install the Daemon as documented.
2. Start the Daemon with `-register-address host:22`. Confirm the local OpenSSH checks pass and a
   code appears. Record the Windows and OpenSSH versions, but never record the code or private keys.
3. Start the Hub with no hub.json. Open `http://127.0.0.1:7700/hosts`, choose a Host id and paste the code.
4. Confirm the Host appears without restarting the Hub. Start a Session through the Client.
5. Inspect authorized_keys: the completed Hub public key remains, and its temporary and pending
   lines are gone. Check that unrelated keys, comments, trust entries and config settings remain.

## Failure runs

- Change a character in a code: no SSH connection or persistent change may occur.
- Use another Host fingerprint or an existing conflicting known_hosts entry: no authentication
  may occur. Correcting the address must not bypass this check.
- Before entering a code, test its temporary SSH key in an isolated check fixture. Shell, arbitrary
  command, PTY, local and remote forwarding, and agent forwarding must be refused. The startup
  probe checks forwarding, PTY, agent forwarding and a harmless arbitrary-command attempt.
- Wait five minutes, cancel locally, or kill and restart the Daemon. The old temporary key must
  never install authorization. Repeat with an already-open temporary SSH connection after expiry.
- Submit two different permanent keys with one code. Exactly one can claim it. Retry its signed
  claim: authorization must not be duplicated.
- Stop after a claim but before durable Hub intent. Check pending-key expiry and Host-local cleanup.
- Make the config write fail after Host completion. Keep the public recovery record. Restore write
  access and restart the Hub: it must finish without revoking the completed key or losing trust.
- Kill the Daemon before completion. Once the pending authorization expires, generate a fresh code
  and recover with the same Host id. Completed keys from other registrations must stay.
- Close the browser during registration, then inspect the Host list and recovery record before retry.
- Use a different Daemon account or an administrator account. No usable code may be displayed.
- Use IPv6 and a non-default SSH port, then try an unreachable address. The error must be useful.
- Open the Hub from a different browser origin or send non-JSON/oversized input: registration must
  be refused. Codes must not appear in URLs, browser storage, Hub logs or recovery files.

## Runs

| Date | Commit | Windows / OpenSSH | Result |
| --- | --- | --- | --- |
| 2026-09-07 | working branch for #123 | workspace OpenSSH 9.5p2, sshd stopped | Real standard-account Host run pending; automated Go SSH and recovery checks are separate evidence. |
| 2026-09-08 | working branch for #123 | Windows ACL fixtures | Permission checks passed for delete, change-permissions, take-ownership and delete-child grants. This does not complete the SSH Host run. |

Do not mark this real-machine check as passed from an in-process SSH rig. Record each missing or
unclear installation step before changing the instructions.
