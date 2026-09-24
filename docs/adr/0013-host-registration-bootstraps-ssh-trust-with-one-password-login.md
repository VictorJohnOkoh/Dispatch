# Host Registration uses a code that names the Host and a password that installs a restricted Hub key

Status: accepted, revised 2026-09-24. This replaces the code-only flow from #123, which replaced the
password flow from PR #122. The filename stays the same so that existing links still work.

## Why it changed

The first version sent a password to whatever Host key answered. The user confirmed a fingerprint
in a terminal, and that check went away when registration moved into the Client. The second version
sent no password. The Daemon made a temporary SSH key, and a relay on the Host installed the Hub key.
That worked only for standard accounts, because the relay could not write the file that Windows
OpenSSH reads for administrators. It also needed a pending state, claims, locks and a recovery record.

A Host that serves models is usually a machine where the user is an administrator, so v1 must
support administrator accounts. This version keeps the part of the code that gave trust, and gives
the key installation back to a login that can already write the file.

## The code

The Daemon prints a code when it starts with `-host-reg <address>`. The code holds four public
facts: the SSH address, the account the Daemon runs as, the Daemon port, and the full SHA-256
fingerprint of the Host's ed25519 SSH key. It holds no secret and changes nothing on the Host, so
it has no expiry and the user can use it more than once. The Host key fingerprint is the only part
that proves anything.

The layout is `dispatch4.` followed by unpadded base64url of: 32 fingerprint bytes, the Daemon port
as two big-endian bytes, one address-length byte, the address, and the account. After a dot come
the first eight SHA-256 bytes of that payload in hex. The checksum finds copy errors and does not
authenticate. A code with an older prefix gets an error that says to take a fresh code. The limit
is 4096 characters.

Before it prints the code, the Daemon connects to the address it will advertise and checks that the
same Host key answers there. This finds a Daemon in WSL beside a Windows OpenSSH, and a wrong
address.

## Registration

1. The user enters the code and the account password in the Client. The user can also correct the
   address, and can name a different account when SSH and the Daemon run as two accounts.
2. The Hub connects with the password. Its host key callback compares the key with the code, and
   checks the Hub's managed trust file, the user's known_hosts and every configured trust file for
   a conflicting line. It runs before authentication, so a wrong Host never receives the password.
   The Hub offers password and keyboard-interactive authentication with the same password.
3. Over that login the Hub asks which file OpenSSH reads. Only Windows answers `whoami /groups`.
   Output that holds `S-1-5-32-544` is an administrator, and the file is
   `C:\ProgramData\ssh\administrators_authorized_keys`. Every other login uses the account's own
   `~/.ssh/authorized_keys`, and the Hub makes `.ssh` when it is not there.
4. The Hub writes its line over SFTP. For an administrator it then sets the ACL that OpenSSH
   requires, SYSTEM and Administrators only, by SID so that a translated group name does not matter.
5. The Hub opens a second connection with its own key, and runs the normal Handshake through the
   tunnel to the Daemon.
6. The Hub trusts the Host key in its known_hosts, saves `hub.json`, and attaches the Host to the
   running Hub. No restart is needed.

A failure after step 4 puts the keys file back the way the Hub found it, over the same password
login, and removes the trust line the attempt added. If the file changed during registration, the
Hub does not overwrite it and the error says to remove the `dispatch-hub` line by hand. The last
step to fail is the config save, and that step also rolls back. So there is no recovery record: a
Host is never left with a key that the Hub did not save, except when the Hub process dies in the
middle. That line is restricted, and the next registration replaces it.

The password stays in the Hub's memory for one request. The Hub does not write it to a file, a log
or a URL, and the Client clears the field when it sends the form.

## The Hub key line

```
restrict,port-forwarding,permitopen="127.0.0.1:<daemon port>",command="exit 1" ssh-ed25519 AAAA... dispatch-hub
```

After registration the Hub uses SSH for one thing, the tunnel to the Daemon on 127.0.0.1. The line
allows that and nothing else. `restrict` turns off forwarding, the PTY, the agent and X11.
`port-forwarding` with `permitopen` allows the one tunnel. `restrict` does not stop commands, so the
forced command answers any shell or command request and ends.

This matters most for administrators. A key in `administrators_authorized_keys` logs in as every
administrator on that Host. Without the options, a stolen Hub key is an administrator shell.

A separate file for Dispatch keys is not possible without a change to `sshd_config`, because
OpenSSH reads only the files that `sshd_config` names. Dispatch does not change `sshd_config`,
services or firewall rules. So the Hub line lives in the same file as the user's keys, and the
`dispatch-hub` comment marks it. When a line for the same Hub key is already there without these
options, registration replaces it. Unrelated lines stay.

A future shell into a Session's working directory goes through the Daemon, not through this key.
The Daemon knows the working directory and runs as the account the agent uses.

## Accounts

Windows accepts an enabled local account, standard or administrator. Linux accepts any account,
root included. When the Daemon has administrator rights, which on Windows means an elevated token,
or runs as root, it logs a warning: every Harness tool call then runs with those rights, and the
Workspace Root does not bound shell commands. A Daemon that an administrator starts without
elevation does not have those rights and gets no warning. A standard account is safer, and the
install guide says so. Domain and Entra ID accounts are not supported.

The file the Hub writes depends on group membership, not on elevation, because that is how
OpenSSH's default `Match Group administrators` rule decides. `whoami /groups` lists the group for a
filtered token too, marked as used for deny only.

## The existing-login way

The Client also offers an SSH login that the machine already has, from the SSH agent or the key
files in `~/.ssh`. The Hub trusts only a Host that a known_hosts file already names, then installs
the same restricted line the same way, with the same rollback.

## Client and Hub

A missing `hub.json` starts an empty Hub at 127.0.0.1:7700. The form is on `/hosts`. The POST
endpoint checks the numeric loopback Host header, the peer and the exact browser Origin, requires
JSON, bounds the input, and runs one registration at a time. Browser access to the Hub is loopback
only. Remote browser access needs a separate authenticated, encrypted design.

Config reads and writes stay in `cmd/dispatch` through a commit callback. The Host id and the
normalized SSH address plus Daemon port are checked before the Host changes and again at commit.
Normalization lowercases DNS names, removes a final dot and normalizes IP spelling. DNS aliases for
one machine are not resolved.

## Verification

The in-process SSH tests enforce the line's options the way sshd does, so the Handshake passes only
through the one permitted tunnel. They cover the fingerprint check before the password, known_hosts
conflicts, a wrong password, rollback after a failed Handshake and after a failed commit, the
administrators file and its ACL, replacement of an older line, and the existing-login way. They do
not prove Windows ACLs or the real OpenSSH options. #83 is that run on a real Windows Host, and it
is recorded in `docs/checks/first-host.md`.

References: [OpenSSH authorized_keys options](https://man.openbsd.org/sshd.8#AUTHORIZED_KEYS_FILE_FORMAT),
[Windows key management](https://learn.microsoft.com/en-us/windows-server/administration/openssh/openssh_keymanagement).
