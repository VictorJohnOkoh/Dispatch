# Remote Host prerequisites

Do all of this **at the Host**, before you run `scripts/capture-remote-host.sh`.
The script verifies every item below and refuses to start if one is missing, but
it cannot do any of them for you — they need a keyboard at the machine.

Budget about 20 minutes, plus a model download.

**Use Windows for the Host.** `scripts/capture-hermes.sh` reads the Hermes config
from `%LOCALAPPDATA%\hermes`, which is wrong on macOS. A macOS Host means fixing
that first. See [macOS](#macos-host) at the end.

---

## 1. OpenSSH Server

PowerShell **as Administrator**:

```powershell
Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0
Start-Service sshd
Set-Service -Name sshd -StartupType Automatic
```

Confirm the firewall rule exists — the installer normally adds it:

```powershell
Get-NetFirewallRule -Name *OpenSSH-Server* | Select Name, Enabled
```

If it is missing:

```powershell
New-NetFirewallRule -Name sshd -DisplayName "OpenSSH Server (sshd)" `
  -Enabled True -Direction Inbound -Protocol TCP -Action Allow -LocalPort 22
```

## 2. Git for Windows

<https://git-scm.com/download/win>

Required for two separate reasons: the capture scripts are bash, and Hermes
shells out through Git Bash for its terminal tool.

## 3. Make bash the default SSH shell

**The most common cause of a wasted afternoon.** Windows OpenSSH hands remote
commands to `cmd.exe` by default. Every POSIX command the capture scripts send
fails in a way that reads like a broken script rather than a wrong shell.

PowerShell as Administrator:

```powershell
New-ItemProperty -Path "HKLM:\SOFTWARE\OpenSSH" -Name DefaultShell `
  -Value "C:\Program Files\Git\bin\bash.exe" -PropertyType String -Force
```

## 4. Python and Node

- Python 3.11 or newer — <https://python.org/downloads> — tick **Add python.exe to PATH**
- Node LTS — <https://nodejs.org>

Hermes is a Python program; Pi is a Node one. Neither runs without its runtime
on `PATH` for the SSH session, not just for the desktop session.

### PATH order decides which one runs

Windows OpenSSH gives the SSH session the **Machine** `PATH`. Your User `PATH`
is not in it, so a directory you added for the desktop session stays invisible
to every capture script.

Order decides the rest. `command -v python` returns the first match, not the
working one, so a single dead entry near the front hides every good entry behind
it. On the development Host the list began with a Hermes venv `Scripts`
directory, and its `python.exe` is a uv trampoline:

```
error: uv trampoline failed to spawn Python child process
  Caused by: uncategorized error (os error 4551)
```

The preflight reported Python as found and unable to start. That reads like a
broken interpreter, and it was a `PATH` written in the wrong order.

Check from the Client over SSH rather than at the Host's keyboard. The two
sessions get different lists, so only the SSH one answers the question:

```bash
ssh <user>@<host> 'echo "$PATH"; command -v python; python --version'
```

If the wrong entry wins, move the real interpreter's directory to the front of
the Machine `PATH` in an elevated PowerShell, then `Restart-Service sshd`. Move
it rather than deleting whatever sat in front of it: that directory usually
carries other commands that still work. Back the old value up first, because
this variable is easy to truncate and hard to reconstruct.

The same trap catches Node and `npm`.

## 5. A fixed address

Set a static IP, or reserve the Host's address on your router. A capture run
takes minutes, and a DHCP lease that moves mid-run kills it.

Record the address — the script asks for it.

## 6. Stop the machine sleeping

Settings → System → Power & battery → Screen and sleep → set **both** dropdowns
to **Never**.

A Host that sleeps mid-capture produces a truncated transcript and a stack of
connection errors. It looks exactly like a broken script.

## 7. Network profile: Private

Settings → Network & internet → your adapter → **Private network**.

On a Public profile, Windows Firewall drops inbound SSH whatever the rule says.

## 8. Know whether your account is an Administrator

This decides where your public key goes, and the wrong choice fails with an
unhelpful `Permission denied (publickey)`.

| Account | Key file |
| --- | --- |
| Administrator | `C:\ProgramData\ssh\administrators_authorized_keys` |
| Standard | `C:\Users\<you>\.ssh\authorized_keys` |

Check with:

```powershell
whoami /groups | findstr /C:"S-1-5-32-544"
```

A hit means you are an Administrator.

The admin file also needs a locked-down ACL or sshd ignores it:

```powershell
icacls C:\ProgramData\ssh\administrators_authorized_keys /inheritance:r
icacls C:\ProgramData\ssh\administrators_authorized_keys /grant SYSTEM:F
icacls C:\ProgramData\ssh\administrators_authorized_keys /grant Administrators:F
```

You do not need the key yet. Run `--check` once when you reach the end of this
list: it generates the keypair, prints the **public** half, and gives you the
exact `Add-Content` and `icacls` commands for your account type. Paste it then.

Only the `.pub` file ever leaves your machine. The private key — same path, no
extension — stays put.

## 9. A Vendor, serving, with a tool-calling model

**Ollama** is the easier one over SSH — headless, and models pull from the CLI:

```powershell
# install from https://ollama.com/download, then:
ollama pull qwen3:8b
ollama list
```

**LM Studio** matches the existing captures more closely, but is GUI-first: it
needs a desktop session open on the Host, and the server started by hand from
Developer → Start Server. Over SSH alone it will not start.

Either way the model **must** be tool-calling capable. A model that cannot call
a tool produces an empty capture, which is the whole point of the exercise lost.

## 10. Reboot

One reboot settles the service, the `PATH` entries and the shell registry key
together. Skipping it is the third common time-sink.

---

## Then, from your own machine

`scripts/capture-opencode-host.sh` needs **Python 3.11+ on the Client too**, and
that is the only thing this side asks for. `scripts/opencode-gates.py` counts the
three gates there rather than on the Host, on purpose: the Host is the thing
under test, so the measurement should not share its environment.

The preflight runs a real script rather than `--version`, because a shim answers
`--version` and then fails to spawn its child. That happened on the development
machine: a `uv` trampoline first on the `PATH` reported 3.11 and died with
`os error 4551` on every actual script.

```bash
bash scripts/capture-remote-host.sh --check
```

`--check` runs the preflight only. It verifies every item above, prints a
numbered PASS/FAIL for each with the exact remedy, and changes nothing. Iterate
on that until it is all green — it takes seconds per attempt.

Then run it without the flag to do the real capture.

---

## Harnesses

The script installs Pi over SSH if it is missing (`npm i -g`).

Hermes needs its source tree on the Host and cannot be installed remotely:

```bash
git clone <the hermes repo> && cd hermes && pip install -e '.[acp]'
```

If Hermes will not install on the Host, that is a **recorded finding**, not a
failure — issue #4 accepts "a recorded reason why one is not". Note the reason
and carry on with Pi.

---

## macOS Host

Only if you have no second Windows machine. Expect to fix the Hermes config path
in `scripts/capture-hermes.sh` first.

1. System Settings → General → Sharing → **Remote Login** on, and allow your user
2. System Settings → Lock Screen → set displays never to sleep; or run the
   captures under `caffeinate -i`
3. `brew install node python@3.11 ollama`, then `ollama serve` and `ollama pull qwen3:8b`
4. Fixed address, as in step 5 above

The key copy is easier — `ssh-copy-id` exists, so the script uses it and step 8
does not apply.
