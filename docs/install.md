# Put a Daemon on a Host by hand

This is the v1 install. You copy two files to the Host and start the Daemon yourself. The Hub does
not install anything, and nothing here runs from the Client.

Follow this page from the top and type nothing from memory. `SPEC.md` behaviour 13 is a person doing
exactly that on a machine that has never had a Daemon.

Two names are used below. The **Host** is the machine that runs the Daemon and the Harness. The
**Client machine** is the one that runs the Hub and the browser. They can be the same machine, but
this page assumes they are not.

## Before you start

On the Host:

- An account you can reach with `ssh`.
- `sshd` running.
- A directory for the Workspace Root. Every Session works below it.

On the Client machine:

- Go 1.25 or later, to build the binary.
- `ssh`, `scp` and `ssh-keyscan`. All three come with OpenSSH.

You do not install Go on the Host. You build the binary here and copy it there.

## 1. Build the Daemon binary for the Host

Build for the Host's operating system and processor, not for yours. The SQLite driver is pure Go, so
`CGO_ENABLED=0` gives one static file with nothing to install beside it.

PowerShell, for a 64-bit Linux Host:

```powershell
$env:GOOS = "linux"; $env:GOARCH = "amd64"; $env:CGO_ENABLED = "0"
go build -o dispatch ./cmd/dispatch
Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
```

Use `$env:GOARCH = "arm64"` for a Raspberry Pi 4 or 5.

The same build with `bash`:

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o dispatch ./cmd/dispatch
```

Write down the checksum. Step 3 compares it.

```powershell
Get-FileHash dispatch -Algorithm SHA256
```

## 2. Write the Host's `daemon.json`

Copy `daemon.example.json` and edit it. Every path in it is a path **on the Host**.

```json
{
  "listen": "127.0.0.1:7717",
  "workspaceRoot": "/home/victor/work",
  "logPath": "/home/victor/.local/state/dispatch/events.db",
  "vendors": [
    {"kind": "ollama", "base": "http://127.0.0.1:11434"}
  ],
  "harnesses": [
    {"name": "passthrough"}
  ],
  "policyDefault": {
    "read": "auto",
    "edit": "wait",
    "execute": "wait",
    "fetch": "auto",
    "other": "wait"
  }
}
```

Four rules for this file:

- `listen` stays on `127.0.0.1`. The Daemon has no authentication of its own. The SSH tunnel is the
  only way in, and a Daemon on `0.0.0.0` gives that up.
- `workspaceRoot` must exist. The Daemon resolves it at start and stops if it is not there.
- The directory that holds `logPath` must exist. The Daemon creates the file, not the directory.
- An unknown key stops the Daemon. There is no key that is quietly ignored.

## 3. Copy both files to the Host

The binary and `daemon.json` are the whole install. There is no third file.

```powershell
ssh victor@192.168.1.20 "mkdir -p ~/dispatch ~/.local/state/dispatch ~/work"
scp dispatch daemon.json victor@192.168.1.20:~/dispatch/
ssh victor@192.168.1.20 "chmod +x ~/dispatch/dispatch && sha256sum ~/dispatch/dispatch"
```

**Compare that checksum with the one from step 1.** `scp` can report success and leave you with an
old file or an empty one. This repo has lost two runs to exactly that, so check the number rather
than the exit code.

## 4. Start the Daemon by hand

Open a shell on the Host and start it in the foreground. Read the first lines it prints.

```bash
ssh victor@192.168.1.20
cd ~/dispatch
./dispatch daemon -config daemon.json
```

A Daemon that started prints two lines to stderr, `dispatch starting` and `daemon listening`. A
Harness with no Adapter in this build prints a warning and the Daemon keeps running.

Leave that shell open. Nothing here installs a service. To keep the Daemon alive after you log out,
run it under `tmux`, or write your own `systemd` unit.

Check it from a second shell on the Host:

```bash
curl http://127.0.0.1:7717/v1/sessions
```

You get `{"sessions":[...]}`, empty on a Host with no Session yet. If this fails, the problem is on
the Host and not in the tunnel. Fix it here before you go back to the Client machine.

## 5. Give the Hub a key and the Host's key

The Hub is an SSH client in its own process. It does not read `~/.ssh/config`, so every value it
needs is in `hub.json`.

Make an ed25519 key with no passphrase, on the Client machine. The Hub cannot answer a passphrase
prompt.

```powershell
ssh-keygen -t ed25519 -f $HOME\.ssh\dispatch_hub -N '""'
ssh-copy-id -i $HOME\.ssh\dispatch_hub.pub victor@192.168.1.20
```

Without `ssh-copy-id`, append the public key to `~/.ssh/authorized_keys` on the Host yourself.

Now record the Host's own key. The Hub checks it on every connection and refuses a Host that answers
with a key it has not seen.

```powershell
ssh-keyscan -H 192.168.1.20 >> $HOME\.ssh\known_hosts
```

`ssh-keyscan` trusts whoever answers, so run it on a network you trust, or copy the line from the
Host's `/etc/ssh/ssh_host_ed25519_key.pub` instead.

Prove the key works before the Hub uses it:

```powershell
ssh -i $HOME\.ssh\dispatch_hub victor@192.168.1.20 "echo ok"
```

## 6. Write `hub.json` on the Client machine

Copy `hub.example.json` and edit it. Every path in it is a path on the **Client machine**.

```json
{
  "listen": "127.0.0.1:7700",
  "hosts": [
    {
      "id": "workstation",
      "address": "192.168.1.20:22",
      "user": "victor",
      "keyPath": "C:/Users/victor/.ssh/dispatch_hub",
      "knownHosts": "C:/Users/victor/.ssh/known_hosts",
      "daemonPort": 7717
    }
  ]
}
```

- `id` is the Host's name in every URL and every Cursor. Keep it short and use letters, digits and
  hyphens.
- `daemonPort` is the port from the Host's `listen`. The Hub opens a channel onto `127.0.0.1` at that
  port, on the far side of the tunnel.
- `knownHosts` is optional. Leave it out and the Hub reads `~/.ssh/known_hosts`.

The Hub reads both files at start. A path that is wrong stops the Hub with the path in the message,
rather than leaving a Host that never connects.

## 7. Start the Hub and reach the Host

On the Client machine:

```powershell
go run ./cmd/dispatch hub -config hub.json
```

Then, from another shell:

```powershell
curl http://127.0.0.1:7700/v1/hosts
curl http://127.0.0.1:7700/v1/hosts/workstation/sessions
```

The first answers from the Hub alone and lists what `hub.json` names. The second travels down the
tunnel and is answered by the Daemon on the Host. When both answer, the install is done.

## When it does not work

The Hub names four failures. Each one has one place to look.

| What the Hub says | What it means | What to do |
| --- | --- | --- |
| `the Host does not answer` | The SSH connection failed | Check `address`, the network, and that `sshd` is running |
| `the Host refused this key` | `sshd` rejected the key | Check `keyPath` and `authorized_keys` on the Host. Step 5's `ssh` command proves both |
| `the Host's key is not the one in known_hosts` | The Host answered with an unexpected key | Run `ssh-keyscan` again. If the Host was rebuilt, delete its old line first |
| `the Host answers but no Daemon is listening` | SSH works and the port is closed | The Daemon is not running, or `daemonPort` and the Host's `listen` port differ |

The last one is the useful one. It tells you the machine is up and the tunnel is open, so the
problem is the Daemon and not the network.
