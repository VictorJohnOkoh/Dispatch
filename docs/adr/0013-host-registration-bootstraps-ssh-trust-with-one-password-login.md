# Host Registration uses a single-use code entered in the Client

Status: accepted. Issue #123 replaces the password flow from PR #122. The filename stays stable for existing links.

## Trust and account scope

The Daemon generates a long code only when started with `-register-address`. This is explicit local
authorization to register one Hub. Normal startup creates no code. The first version requires the
Daemon and SSH to use the same enabled standard local Windows account. It refuses administrator
membership, including a filtered administrator token. A different Daemon account, administrator,
domain account or Entra account requires a separate design; the earlier cross-account assumption
does not apply to this automatic path. Manual configuration still supports existing SSH profiles.

The code contains version 1, a random 128-bit registration id, a random 256-bit ed25519 seed, the
suggested SSH address and account, the Daemon port, the OpenSSH ed25519 Host-key fingerprint, and
a five-minute expiry. JSON is encoded with unpadded base64url, prefixed `dispatch1.`, with the first
eight SHA-256 bytes as a hexadecimal checksum. The limit is 4096 characters. The checksum detects
copy errors; it does not authenticate the Host. Trust comes from copying the code from the intended
Host through a trusted path. A short numeric code is not supported. A hash cannot recover a key.

The Hub checks the fingerprint before SSH authentication. Conflicting entries in its managed trust
file, the user's default known_hosts, or configured trust files stop registration. Address correction
never changes the fingerprint. This version uses an ed25519 Host key and fails if OpenSSH does not
offer it. The Host private key stays on the Host; the permanent Hub private key stays on the Hub.

## Restricted SSH operation

The temporary authorized_keys line uses `restrict`, `expiry-time` and a fixed `command`. The command
starts this binary's `registration-relay` with the Daemon port. It accepts only the literal original
command `dispatch-register`, reads one bounded signed request from stdin, and sends it to the existing
Daemon listener on 127.0.0.1. It accepts no user-selected URL, file, account or shell command.

Before displaying a code, the Daemon checks local SSH login, fingerprint, denial of local and remote
forwarding, denial of a PTY and agent forwarding, and enforcement of the fixed command. Failure
removes temporary authorization and displays no code. This tests the installed OpenSSH configuration;
Dispatch does not change sshd_config, services or firewall rules. OpenSSH must accept loopback SSH
at the selected port. The Daemon must listen on 127.0.0.1.

The local endpoint verifies ed25519 signatures and the in-memory pending registration. Claim and
abort use the temporary key. Completion uses the claimed permanent key. A first claim binds its
public key under one mutex, even if the authorization write fails. The same key can retry; another
key cannot take the claim. A claimed operation gets two minutes to finish. Pending permanent-key
authorization has an OpenSSH expiry too. No secret is written in a recovery record or authorization
line. A temporary key left after a crash cannot install a key because a restarted Daemon has no
pending registration. It cannot open a shell or tunnel because its OpenSSH restrictions remain.

One operating-system file lock prevents two local Daemons from registering the same account at
once. A similar lock protects the managed Hub identity. The locks release after process death.

## Commit and recovery

These are separate machines, so there is no atomic distributed transaction. The durable Hub intent
is the decision to finish registration; it is not an abandoned attempt after that point.

1. Verify temporary SSH trust, then install a time-limited authorization for the Hub public key.
2. Open a separate permanent-key SSH connection and verify forwarding and the normal Handshake.
3. Write the verified trust entry, then sync `hub.json.registration`, containing the registration id
   and public connection profile. This is committed intent. It contains no seed or private key.
4. Sign completion with the permanent key. The Host atomically replaces this attempt's pending
   line with completed authorization and removes its temporary line.
5. Atomically save hub.json, attach the Host to the running Hub, and remove the recovery record.

Before step 3, a failure removes only this attempt's authorization and trust addition. A lost cleanup
reply reports that local cancellation may be needed. The OpenSSH expiry bounds new login with the
pending key. After step 3, failure keeps the intent and trust. It never automatically removes a
completed authorization. Startup retries completion and the config save. A completed Host line also
acts as a public receipt, so a lost completion reply is recoverable after a Daemon restart.

If completion never reached the Host and its lease expired, the Hub starts with a recovery notice.
The user starts a fresh code on the Host and submits it with the same Host id and connection profile.
The new attempt replaces the old recovery record only after its own checks pass. Do not delete a
recovery record simply because an acknowledgement was lost. A completed but unreachable Host requires
restoring reachability, not issuing another public key. A Host key change requires explicit local
trust repair; Dispatch never silently overwrites conflicting trust.

Before committed intent, local cancellation or expiry removes pending authorization. Ctrl+C cancels
registration and stops the Daemon; do not use it while Sessions must remain running. A normal restart
invalidates the pending operation. Starting another explicit registration sweeps stale pending lines.
Completed lines and unrelated keys remain. Existing authorization for the same Hub key is refused
without changing it; use its existing profile or resolve it locally.

## Client and Hub

A missing hub.json starts an empty Hub at 127.0.0.1:7700. An existing empty Host list is also valid.
The Client form is on `/hosts`. It clears the code on submission and page exit and uses no URL,
browser storage or log for it. The POST endpoint checks the numeric loopback Host header, peer and
exact browser Origin, requires JSON, bounds input, and serializes registrations. Hub browser access
is loopback only. Remote browser access needs a separate authenticated, encrypted design.

Config reads and writes stay in cmd/dispatch through prepare and commit callbacks. A Host id and a
normalized SSH address plus Daemon port are checked before mutation and again before persistence.
Normalization lowercases DNS names, removes a final dot and normalizes IP spelling. DNS aliases for
the same machine cannot be detected reliably and are not resolved into identity.

Live attachment updates the SSH dialer and Host table, then ends existing merged streams so their
normal reconnect includes the new Host. The submitting Client reloads its cards after success.
Manual hub.json edits still require a restart. `dispatch host add` now directs the user to the Client.

## Verification and remaining account work

Automated checks cover code corruption, signatures, competing claims, expiry, cancellation, restart,
failed claim writes, preserved authorization, locks, SSH fingerprint ordering, pre-intent rollback,
and post-intent recovery. The real Windows run is recorded in docs/checks/first-host.md. A passing
in-process SSH test does not prove Windows ACL or OpenSSH behavior.

Issues #81–#83 must be reviewed against this flow. Password prompting is superseded. Administrator
authorization and its real Windows check remain separate work and are not claimed as supported here.

References: [OpenSSH key options](https://man.openbsd.org/sshd.8),
[Windows key management](https://learn.microsoft.com/en-us/windows-server/administration/openssh/openssh_keymanagement).
