# Put a Daemon on a Host by hand

This is the v1 install. You copy two files to the Host and start the Daemon yourself. The Hub does
not install anything, and nothing here runs from the Client.

ADR 0013 specifies `dispatch host add` for automatic Host Registration, but that command is not in
the current build yet. Until it lands, steps 6 to 8 are the supported manual fallback. The command
will automate those steps without changing the `hub.json` format.

Follow this page from the top and type nothing from memory. `SPEC.md` behaviour 13 is a person doing
exactly that on a machine that has never had a Daemon.

Two names are used below. The **Host** is the machine that runs the Daemon and the Harness. The
**Client machine** is the one that runs the Hub and the browser. This page assumes both are Windows
and that they are two different machines. The last section holds the commands that differ for a
Linux Host.

Every command on this page is PowerShell.

## Before you start

On the Host:

- Windows 10 or 11, and an account you can sign in to.
- A directory for the Workspace Root. Every Session works below it.

On the Client machine:

- The OpenSSH client. Windows 11 has it. Check with `ssh -V`.

Go 1.25 or later on one of the two machines, to build the binary. It does not matter which one. One
binary runs both roles, so you build it once and copy it to the other machine.

## 1. Turn on the SSH server on the Host

Windows ships the OpenSSH server and it is off by default. Open PowerShell **as Administrator** on
the Host and run:

```powershell
Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0
Start-Service sshd
Set-Service -Name sshd -StartupType Automatic
```

The first `Start-Service` makes the Host's own key and adds the firewall rule. Check both:

```powershell
Get-Service sshd
Get-NetFirewallRule -Name *OpenSSH-Server* | Select-Object Name, Enabled
```

Leave `sshd_config` alone. The Hub needs `AllowTcpForwarding`, which is on unless somebody turned it
off, and the stock file does not set it.

Now find the Host's address and account name, because every later step needs both:

```powershell
(Get-NetIPAddress -AddressFamily IPv4 | Where-Object { $_.IPAddress -ne "127.0.0.1" }).IPAddress
$env:USERNAME
```

## 2. Build the binary

One binary runs both roles, so this is the only build. Run it on whichever machine has Go and copy
the file to the other one. The SQLite driver is pure Go, so `CGO_ENABLED=0` gives one file with
nothing to install beside it.

```powershell
$env:GOOS = "windows"; $env:GOARCH = "amd64"; $env:CGO_ENABLED = "0"
go build -o dispatch.exe ./cmd/dispatch
Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
```

Write down the checksum. Step 4 compares it.

```powershell
(Get-FileHash dispatch.exe -Algorithm SHA256).Hash
```

## 3. Write the Host's `daemon.json`

Copy `daemon.example.json` and edit it. Every path in it is a path **on the Host**.

Write Windows paths with forward slashes. Go accepts them, and a backslash in JSON has to be doubled,
which is one more thing to get wrong.

```json
{
  "listen": "127.0.0.1:7717",
  "workspaceRoot": "C:/Users/victor/work",
  "logPath": "C:/Users/victor/AppData/Local/dispatch/events.db",
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
- The directory that holds `logPath` must exist. The Daemon makes the file, not the directory.
- An unknown key stops the Daemon. There is no key that is quietly ignored.

A Vendor `kind` is `ollama`, `lmstudio` or `llamaswap`, and `base` is where that Vendor already
answers on the Host. The Daemon does not start a Vendor. It only speaks to one that is running.

### A llama-swap Host needs `ttl: 0`

Only if you list a `llamaswap` Vendor. Skip this if you do not.

Set `ttl: 0` on every Model in llama-swap's own `config.yaml`, next to each `cmd`:

```yaml
models:
  "qwen2.5-coder-1.5b":
    cmd: |
      ...
    ttl: 0
```

Leaving the key out does the same thing. Writing the `0` shows the next reader a decision instead of
an absence.

The Daemon does all the loading and unloading, and ADR 0002 gives it that job alone. A `ttl` above
zero puts a second evictor on the Host that works to its own clock and cannot see a Session. A
Session idle between two prompts then loses its Model and pays a cold load nobody planned, which
runs from 4.4s for a 1.5B to 22.5s for a 20B on the development Host.

**Nothing checks this for you.** `ttl` is a config key, and llama-swap serves no endpoint that reads
or writes it, so the Daemon can neither set it nor warn you. A Host that misses this is not an error
you will see. It is a reload you will wait for.

## 4. Copy both files to the Host

The binary and `daemon.json` are the whole install. There is no third file.

Make the three directories first. This runs from the Client machine and works whatever shell the
Host's `sshd` starts.

```powershell
ssh victor@192.168.1.20 "powershell -NoProfile -Command New-Item -ItemType Directory -Force C:/Users/victor/dispatch, C:/Users/victor/work, C:/Users/victor/AppData/Local/dispatch"
scp dispatch.exe daemon.json victor@192.168.1.20:C:/Users/victor/dispatch/
ssh victor@192.168.1.20 "powershell -NoProfile -Command (Get-FileHash C:/Users/victor/dispatch/dispatch.exe -Algorithm SHA256).Hash"
```

**Compare that checksum with the one from step 2.** `scp` can report success and leave you with an
old file or an empty one. This repo has lost two runs to exactly that, so check the number rather
than the exit code.

## 5. Start the Daemon by hand

Sign in to the Host, open PowerShell there, and start it in the foreground. Read the first lines it
prints.

```powershell
cd C:\Users\victor\dispatch
.\dispatch.exe daemon -config daemon.json
```

A Daemon that started prints two lines, `dispatch starting` and `daemon listening`. A Harness with no
Adapter in this build prints a warning and the Daemon keeps running.

Leave that window open. Nothing here installs a service. To keep the Daemon alive after you sign
out, make a Task Scheduler task that runs it at startup.

Check it from a second PowerShell window on the Host. Write `curl.exe` with the extension, because
plain `curl` in PowerShell is `Invoke-WebRequest` and it answers differently.

```powershell
curl.exe http://127.0.0.1:7717/v1/sessions
```

A Host with no Session yet answers `{"sessions":[],"cursor":0}`. If this fails, the problem is on the
Host and not in the tunnel. Fix it here before you go back to the Client machine.

## 6. Give the Hub a key

The Hub is an SSH client in its own process. It does not read `~/.ssh/config`, so every value it
needs is in `hub.json`.

Make an ed25519 key with no passphrase, on the Client machine. The Hub takes no other key type and
cannot answer a passphrase prompt. It stops at start if the file holds either.

```powershell
ssh-keygen -t ed25519 -f $HOME\.ssh\dispatch_hub -N '""' -C dispatch-hub
scp $HOME\.ssh\dispatch_hub.pub victor@192.168.1.20:C:/Users/victor/dispatch_hub.pub
```

`ssh-copy-id` is not on Windows, so install the key yourself. **Where it goes depends on the account,
and this is the step that catches everyone.** Windows `sshd` ships with this in `sshd_config`:

```
Match Group administrators
       AuthorizedKeysFile __PROGRAMDATA__/ssh/administrators_authorized_keys
```

An account in the Administrators group does not use its own `.ssh\authorized_keys` at all. Find out
which account you have, in a PowerShell window **on the Host**:

```powershell
Get-LocalGroupMember -Group Administrators | Select-Object -ExpandProperty Name
```

Ask for the group, not for the current token. `IsInRole("Administrators")` answers `False` in an
ordinary window even for an account that is in the group, because Windows removes the group from the
token until you elevate. Reading that answer sends you to the wrong file.

Your account in that list means an administrator. Run this on the Host, as Administrator:

```powershell
Get-Content C:\Users\victor\dispatch_hub.pub | Add-Content -Path C:\ProgramData\ssh\administrators_authorized_keys -Encoding ascii
icacls C:\ProgramData\ssh\administrators_authorized_keys /inheritance:r /grant "Administrators:F" /grant "SYSTEM:F"
```

The `icacls` line is not optional. `sshd` ignores that file if any other account can write it, and it
says nothing about why.

Your account absent from that list means an ordinary account. Run this on the Host instead:

```powershell
New-Item -ItemType Directory -Force C:\Users\victor\.ssh | Out-Null
Get-Content C:\Users\victor\dispatch_hub.pub | Add-Content -Path C:\Users\victor\.ssh\authorized_keys -Encoding ascii
```

Use `Add-Content -Encoding ascii` in both. PowerShell's `>>` puts a byte order mark at the head of a
new file, and `sshd` then reads the first key as text that is not a key.

## 7. Record the Host's own key

The Hub checks the Host's key on every connection and refuses a Host that answers with a key it has
not seen. Run this on the Client machine:

```powershell
ssh-keyscan -H 192.168.1.20 | Add-Content -Path $HOME\.ssh\known_hosts -Encoding ascii
```

`Add-Content -Encoding ascii` again, for the same reason. A byte order mark on the first line of
`known_hosts` makes the Hub refuse that Host with `the Host's key is not the one in known_hosts`,
which reads like an attack and is a text encoding.

`ssh-keyscan` trusts whoever answers, so run it on a network you trust. To be certain instead, copy
the line out of `C:\ProgramData\ssh\ssh_host_ed25519_key.pub` on the Host. That file needs an
Administrator window to read, and the line needs the Host's address written in front of it.

Now prove both keys work before the Hub uses them:

```powershell
ssh -i $HOME\.ssh\dispatch_hub victor@192.168.1.20 "powershell -NoProfile -Command Write-Output ok"
```

This must print `ok` and ask for nothing. A password prompt means step 6 put the key in the wrong
file.

## 8. Write `hub.json` on the Client machine

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
- `address` needs the SSH port written out. The Hub dials the string as you write it and assumes
  nothing, so `192.168.1.20` alone is refused at start and `192.168.1.20:22` is what you want.
- `daemonPort` is the port from the Host's `listen`. The Hub opens a channel onto `127.0.0.1` at that
  port, on the far side of the tunnel.
- `knownHosts` is optional. Leave it out and the Hub reads `%USERPROFILE%\.ssh\known_hosts`.

The Hub reads the key and `known_hosts` at start. A path that is wrong stops the Hub with the path in
the message, rather than leaving a Host that never connects.

## 9. Start the Hub and reach the Host

On the Client machine, with the binary from step 2:

```powershell
.\dispatch.exe hub -config hub.json
```

On a Client machine that has the repository and Go, `go run ./cmd/dispatch hub -config hub.json` is
the same thing.

Then, from another window:

```powershell
curl.exe http://127.0.0.1:7700/v1/hosts
curl.exe http://127.0.0.1:7700/v1/hosts/workstation/sessions
```

The first answers from the Hub alone and lists what `hub.json` names. The second travels down the
tunnel and is answered by the Daemon on the Host. When both answer, the install is done.

## 10. Check the Event stream

The two commands above are a request and a reply. The Event stream is the long connection the Client
holds open, and it is worth proving on its own.

```powershell
curl.exe -N http://127.0.0.1:7700/v1/events
```

**Write `curl.exe`, not `curl`.** In Windows PowerShell, `curl` is an alias for `Invoke-WebRequest`,
which reads the whole reply into memory before it prints anything. A stream never ends, so it prints
nothing at all and shows a counter climbing under "Reading response stream". The stream is fine and
you are looking at the wrong tool.

A Hub with nothing happening on it sends `: keepalive` and a blank line every ten seconds. That beat
is the stream working. Ctrl+C ends it.

To watch the beat with the time on each line, PowerShell can read the stream itself:

```powershell
Add-Type -AssemblyName System.Net.Http
$client = [System.Net.Http.HttpClient]::new()
$reader = [System.IO.StreamReader]::new($client.GetStreamAsync("http://127.0.0.1:7700/v1/events").Result)
while ($null -ne ($line = $reader.ReadLine())) { "{0:HH:mm:ss} {1}" -f (Get-Date), $line }
```

## 11. Check that a stop leaves nothing behind

Behaviour 6 is checked from a shell on the Host, not from the Client. The Client can only tell you
what the Daemon believes. A shell tells you what is running.

Start a Session, then find the Harness and the children it started. On Windows:

```powershell
Get-CimInstance Win32_Process |
  Where-Object { $_.ParentProcessId -eq (Get-Process dispatch).Id } |
  Select-Object ProcessId, Name, CommandLine
```

Note the process ids, including the ones the Harness started itself. Stop the Session from the
Client, wait five seconds, then ask for those ids again:

```powershell
Get-Process -Id 1234, 5678 -ErrorAction SilentlyContinue
```

Nothing must answer. A process that is still there is the orphan a Job Object exists to prevent, and
it holds the Model in VRAM while the Daemon believes the slot is free.

On Linux the same check is two commands:

```bash
pgrep -P $(pgrep -x dispatch) -a     # before the stop
ps -p 1234,5678                      # after it
```

**This check needs a Harness that starts a process.** Passthrough starts none, so until the OpenCode
Adapter lands there is nothing on the Host to look for. What stands in the meantime is
`TestTheWholeTreeGoesWithTheHarness`, which kills a real process tree and asks the children
themselves whether they are alive.

## When it does not work

The Hub names five failures. Each one has one place to look.

| What the Hub says | What it means | What to do |
| --- | --- | --- |
| `the Host does not answer` | The SSH connection failed | Check `address`, the network, and that `sshd` is running on the Host |
| `the Host refused this key` | `sshd` rejected the key | Step 6. An administrator account uses `administrators_authorized_keys`, and that file needs the `icacls` line |
| `the Host's key is not the one in known_hosts` | The Host answered with a key that is not the recorded one | Step 7. Look for a byte order mark first, then for a rebuilt Host, whose old line you must delete |
| `the Host answers but no Daemon is listening` | SSH works and the port is closed | The Daemon is not running, or `daemonPort` and the Host's `listen` port differ |
| `the Host will not forward a channel` | `sshd` refused the channel itself | Set `AllowTcpForwarding yes` in `C:\ProgramData\ssh\sshd_config` on the Host, then `Restart-Service sshd` |

The last two are the useful ones. Both say the machine is up and the SSH connection is good, so
neither is a network problem.

## If the Host runs Linux

Five things change. Everything else on this page is the same.

- **Step 1** is your distribution's `openssh-server` package, and the config is
  `/etc/ssh/sshd_config`.
- **Step 2** builds for Linux. Use `$env:GOARCH = "arm64"` for a Raspberry Pi 4 or 5.

  ```powershell
  $env:GOOS = "linux"; $env:GOARCH = "amd64"; $env:CGO_ENABLED = "0"
  go build -o dispatch ./cmd/dispatch
  Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
  ```

- **Step 4** makes the directories with `mkdir -p`, and the copied file needs `chmod +x`. Compare the
  checksum with `sha256sum` on the Host.

  ```powershell
  ssh victor@192.168.1.20 "mkdir -p ~/dispatch ~/.local/state/dispatch ~/work"
  scp dispatch daemon.json victor@192.168.1.20:~/dispatch/
  ssh victor@192.168.1.20 "chmod +x ~/dispatch/dispatch && sha256sum ~/dispatch/dispatch"
  ```

- **Step 5** runs `./dispatch daemon -config daemon.json`. Use `tmux` to keep it alive after you log
  out, or write a `systemd` unit.
- **Step 6** has no administrators rule and no `icacls`. The key goes in `~/.ssh/authorized_keys`, and
  `ssh-copy-id -i $HOME\.ssh\dispatch_hub.pub victor@192.168.1.20` puts it there.
