# Host Registration bootstraps SSH trust with one password login

Status: accepted

A new Windows Host already has OpenSSH and its Daemon running, but the Hub cannot use key authentication until the Host trusts its key. Host Registration uses one password-authenticated SSH connection to install the Hub's public key, then proves key-only access and a compatible Daemon before it adds the Host to `hub.json`. This keeps the Daemon on loopback and uses Windows OpenSSH as the security boundary instead of adding authentication to the Daemon.

## Registration flow

The user runs `dispatch host add` on the Client machine. Flags may supply the Host id, address and local Windows account; the command asks for missing values. An address without a port uses SSH port 22, and the Daemon port is 7717 unless `-daemon-port` changes it. The password is read from a hidden prompt, used for one attempt, held only in memory and never logged or stored.

The command reads the SSH Host key before it sends the password. It shows the fingerprint and continues only after the user confirms it on a trusted network. This is trust on first use: later connections refuse a changed Host key, but the first confirmation cannot prove identity by itself.

Registration then runs in this order:

1. Connect with the password and verify that SSH forwarding reaches a compatible Daemon through the normal Handshake.
2. Generate the Hub's passphrase-free ed25519 key if it does not exist. One Hub uses one key for all its Hosts.
3. Detect whether the local Windows account is a standard user or an administrator. Add the public key once to the account's `authorized_keys` file or to `C:\ProgramData\ssh\administrators_authorized_keys`, and apply the required administrator-file ACL.
4. Open a new connection with the key only and repeat the forwarding and Handshake checks.
5. Persist the confirmed Host key and atomically add the Host to `hub.json`.

All checks happen before the config commit. If a later step fails, registration removes only the Host authorization and trust entry that this attempt added. It keeps a newly generated Hub key because that key is the Hub's stable identity and can be used on retry. An error names the failed stage and says whether rollback succeeded.

The managed key and `known_hosts` file live under `%LOCALAPPDATA%\Dispatch\ssh`. Existing `hub.json` files may continue to name keys in other locations. If `hub.json` does not exist, registration creates it with `127.0.0.1:7700` as the Hub listen address. If it exists, registration preserves its settings and reloads it immediately before the atomic write.

## Scope

The first version supports a Windows Hub, a Windows Host and a local Windows account. OpenSSH and the Daemon are prerequisites. Registration does not install either one and does not edit `sshd_config`, firewall rules or Windows services. It does not support Microsoft Entra ID accounts, domain accounts, Linux Hosts, concurrent registrations, Host removal, config live reload or password retries. The Hub must restart after a Host is added. Manual `hub.json` configuration remains supported.

`host add` refuses an existing Host id and an exact duplicate of an SSH address and Daemon port. The SSH account does not have to run the Daemon because the SSH channel reaches the Daemon through the Host's loopback address.

## Module seam

Host Registration is one deep Hub module with one operation:

```go
hub.RegisterHost(ctx, request, interaction, commit)
```

The CLI supplies validated plain input, the hidden password and fingerprint confirmation through `interaction`, and an atomic config callback through `commit`. This keeps config file access in `cmd/dispatch`, as ADR 0010 requires. The module owns SSH trust, remote key installation, ordering, Handshake verification and rollback. It reuses the existing Go SSH implementation and does not start local `ssh`, `scp` or `ssh-keyscan` processes.

Connection and SSH stages have a 10-second limit. Remote key installation and the Handshake have a 30-second limit. Cancellation starts rollback if registration has changed the Host.

## Verification

The existing in-process SSH rig grows password authentication, fingerprint capture, remote command handling and key-only reconnection. Tests exercise the complete module interface, including every failure after mutation and the config commit. One manual Windows check covers a standard account and an administrator account because an in-process SSH server cannot prove Windows ACL behavior.

## Considered options

- **Keep all SSH setup manual.** Rejected because the Windows administrator key location, ACL and text encoding rules make the normal path error-prone.
- **Add a browser setup screen.** Rejected for the first version because the current Hub needs a configured Host before it starts; this would also add config mutation and secret input to the Client.
- **Run an unauthenticated pairing listener in the Daemon.** Rejected because it would add a second network security boundary and weaken the loopback-only Daemon rule.
- **Install OpenSSH from the Hub.** Rejected because the Hub has no remote channel until OpenSSH already runs.
- **Trust the first Host key without confirmation.** Rejected because the password could then be sent to the wrong machine without the user seeing its identity.

## Consequences

Daemon installation stays manual, while the Hub side of SSH trust becomes Client-driven. This reopens only the reach-and-deployment part of the frozen v1 scope. The config format and normal Hub connection path do not change. The shared Hub key is simpler to manage, but compromise of that key affects every Host that trusts it.
