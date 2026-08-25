#!/usr/bin/env bash
#
# Stand up a remote Host and capture Harness output from it — issue #4.
#
# Runs on YOUR machine. Drives the Host over SSH.
#
# Read docs/research/remote-host-prerequisites.md FIRST and do everything in it
# at the Host. This script verifies those prerequisites; it cannot perform them.
#
#   bash scripts/capture-remote-host.sh --check    preflight only, changes nothing
#   bash scripts/capture-remote-host.sh            preflight, then the real run
#
# Iterate with --check until every line is PASS. Each failure prints its own
# remedy, so a fix is one command rather than a search.

set -uo pipefail

# ── Output ────────────────────────────────────────────────────────────────

if [[ -t 1 ]] && command -v tput >/dev/null 2>&1 && [[ "$(tput colors 2>/dev/null || echo 0)" -ge 8 ]]; then
  BOLD=$(tput bold); DIM=$(tput dim); RESET=$(tput sgr0)
  BLUE=$(tput setaf 4); GREEN=$(tput setaf 2); YELLOW=$(tput setaf 3); RED=$(tput setaf 1)
else
  BOLD=""; DIM=""; RESET=""; BLUE=""; GREEN=""; YELLOW=""; RED=""
fi

say()  { printf '  %s\n' "$1"; }
note() { printf '  %s%s%s\n' "$DIM" "$1" "$RESET"; }
warn() { printf '  %s! %s%s\n' "$YELLOW" "$1" "$RESET"; }
head2() { printf '\n%s%s== %s%s\n\n' "$BOLD" "$BLUE" "$1" "$RESET"; }

CHECK_NO=0
PASSES=0
FAILS=0

# pass "what" — record a satisfied prerequisite.
pass() {
  CHECK_NO=$((CHECK_NO + 1)); PASSES=$((PASSES + 1))
  printf '  %s%2d PASS%s  %s\n' "$GREEN" "$CHECK_NO" "$RESET" "$1"
}

# fail "what" "remedy..." — record a failure with the exact fix. Never aborts;
# one run should surface every problem, not the first one.
fail() {
  CHECK_NO=$((CHECK_NO + 1)); FAILS=$((FAILS + 1))
  printf '  %s%2d FAIL%s  %s\n' "$RED" "$CHECK_NO" "$RESET" "$1"
  shift
  for line in "$@"; do printf '         %s%s%s\n' "$DIM" "$line" "$RESET"; done
}

confirm() {
  printf '  %s%s%s [y/N] ' "$BOLD" "$1" "$RESET"
  read -r a || true
  [[ "$a" =~ ^[Yy] ]]
}

ask() {
  local key="$1" prompt="$2" current input
  current="${!key:-}"
  if [[ -n "$current" ]]; then
    printf '  %s%s%s %s[%s]%s ' "$BOLD" "$prompt" "$RESET" "$DIM" "$current" "$RESET"
  else
    printf '  %s%s%s ' "$BOLD" "$prompt" "$RESET"
  fi
  read -r input || true
  [[ -z "$input" && -n "$current" ]] && input="$current"
  printf -v "$key" '%s' "$input"
}

# ── Config ────────────────────────────────────────────────────────────────

MODE="run"
[[ "${1:-}" == "--check" ]] && MODE="check"

REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
LANDING="$REPO_ROOT/docs/research/captures/remote-host"
CONF="$HOME/.remote-host.env"
KEY="$HOME/.ssh/id_ed25519_capstone_host"

HOST_OS=""; HOST_ADDR=""; HOST_USER=""; SSH_PORT=""
VENDOR_KIND=""; VENDOR_URL=""; VENDOR_MODEL=""
# shellcheck disable=SC1090
[[ -f "$CONF" ]] && source "$CONF"

save_conf() {
  cat > "$CONF" <<EOF
HOST_OS="$HOST_OS"
HOST_ADDR="$HOST_ADDR"
HOST_USER="$HOST_USER"
SSH_PORT="$SSH_PORT"
VENDOR_KIND="$VENDOR_KIND"
VENDOR_URL="$VENDOR_URL"
VENDOR_MODEL="$VENDOR_MODEL"
EOF
}

# ── SSH ───────────────────────────────────────────────────────────────────

# Git Bash rewrites POSIX-looking arguments before handing them to native
# executables. ssh.exe and scp.exe are native, so turn that off.
export MSYS_NO_PATHCONV=1
export MSYS2_ARG_CONV_EXCL='*'

# ControlMaster=no and ControlPath=none are not tidiness. If the user's
# ~/.ssh/config enables connection multiplexing and the master socket is stale,
# ssh prints "mux_client_request_session: read from master failed" INSTEAD of
# running the command — and a check that reads stdout accepts that text as a
# version string. Every tool then reports as installed without being run.
SSH_BASE=(-o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new
          -o ControlMaster=no -o ControlPath=none)

_TOP_PID=$$

# rsh "cmd" — run a command on the Host. Returns the REMOTE command's exit
# code, because those are findings and must survive.
#
# ssh reserves 255 for its own failures. That is not a finding, it is a dead
# run, so it stops everything. The kill is load-bearing: every caller wraps
# rsh in $(...), and a plain exit there would leave only the subshell and be
# swallowed by the caller's `|| echo`. Signalling the top-level PID is the
# only thing that actually stops the script.
rsh() {
  local rc=0 out
  out=$(ssh "${SSH_BASE[@]}" -i "$KEY" -p "$SSH_PORT" "$HOST_USER@$HOST_ADDR" "$1" 2>&1) || rc=$?
  printf '%s' "$out"
  if (( rc == 255 )); then
    printf '%s' "$out" > "$SSH_ERR_FILE"
    kill -s TERM "$_TOP_PID" 2>/dev/null
    exit 255
  fi
  return $rc
}

SSH_ERR_FILE="${TMPDIR:-/tmp}/remote-host-ssh-err.$$"
trap 'rm -f "$SSH_ERR_FILE"' EXIT

# _ssh_died — the TERM handler. Reached only when ssh itself failed, never when
# a remote command merely returned non-zero.
_ssh_died() {
  printf '\n'
  warn "SSH failed talking to $HOST_USER@$HOST_ADDR:$SSH_PORT — stopping."
  [[ -s "$SSH_ERR_FILE" ]] && note "  $(head -3 "$SSH_ERR_FILE" | tr '\n' ' ')"
  printf '\n'
  note "  Usual causes: the Host slept, its address moved, or Wi-Fi dropped."
  note "  Diagnose: ssh -vvv -i $KEY -p $SSH_PORT $HOST_USER@$HOST_ADDR"
  printf '\n'
  say "Stopped on purpose — a transport error must never be read as a result."
  [[ -n "${MANIFEST:-}" ]] && printf 'ABORTED: SSH failed at %s\n' "$(date -Iseconds)" >> "$MANIFEST"
  rm -f "$SSH_ERR_FILE"
  exit 1
}
trap _ssh_died TERM

# require_live is now redundant — rsh stops the run itself. Kept as a no-op so
# the call sites still read as deliberate checkpoints.
require_live() { :; }

# ── Details ───────────────────────────────────────────────────────────────

printf '\n%s%s  Remote Host — issue #4%s\n' "$BOLD" "$BLUE" "$RESET"
if [[ "$MODE" == "check" ]]; then
  note "  preflight only — nothing will be changed"
else
  note "  full run — preflight, then capture"
fi
printf '\n'

if [[ -z "$HOST_ADDR" ]]; then
  say "First run. Four questions, then everything else is checks."
  note "Read docs/research/remote-host-prerequisites.md before continuing."
  printf '\n'
fi
ask HOST_OS   "Host OS ('windows' or 'macos'):"
ask HOST_ADDR "Host address:"
ask HOST_USER "Username on the Host:"
ask SSH_PORT  "SSH port:"
[[ -z "$SSH_PORT" ]] && SSH_PORT=22
save_conf

# ── Preflight ─────────────────────────────────────────────────────────────

head2 "Preflight"

# --- This machine ---

for c in ssh scp ssh-keygen; do
  if command -v "$c" >/dev/null 2>&1; then
    pass "$c present locally"
  else
    fail "$c missing locally" "Install OpenSSH on this machine."
  fi
done

# Generated in --check mode too, deliberately. You cannot set up the Host until
# you have a public key to paste there, so a --check that refuses to make one
# leaves you unable to fix the very thing it is complaining about.
if [[ -f "$KEY" ]]; then
  pass "local key exists ($KEY)"
else
  ssh-keygen -t ed25519 -f "$KEY" -C "capstone-host" -N "" >/dev/null 2>&1
  pass "generated $KEY"
fi

# --- Reachability ---

if [[ -f "$KEY" ]] && rsh "echo ok" 2>/dev/null | grep -q ok; then
  pass "SSH key auth works (no password fallback)"
  REACHABLE=1
else
  REACHABLE=0
  if [[ "$HOST_OS" == "windows" ]]; then
    fail "SSH key auth failed" \
      "Prerequisite 1: is sshd running? Start-Service sshd" \
      "Prerequisite 3: is the remote default shell bash, not cmd.exe?" \
      "Prerequisite 8: is your key in the right authorized_keys file?" \
      "The key and the exact commands are printed below." \
      "Diagnose: ssh -vvv -i $KEY -p $SSH_PORT $HOST_USER@$HOST_ADDR"
  else
    fail "SSH key auth failed" \
      "Prerequisite: System Settings -> General -> Sharing -> Remote Login" \
      "Copy the key: ssh-copy-id -i $KEY.pub -p $SSH_PORT $HOST_USER@$HOST_ADDR" \
      "Diagnose: ssh -vvv -i $KEY -p $SSH_PORT $HOST_USER@$HOST_ADDR"
  fi
fi

# Every check below needs a live connection. Report them as blocked rather than
# as failures — an unreachable Host says nothing about Node being installed.
if (( ! REACHABLE )); then
  printf '\n'
  warn "Cannot reach the Host, so the remaining checks were skipped."
  note "They say nothing until SSH works. Fix the above and re-run --check."

  # The public key, and only the public key. Printed in full at the one moment
  # it is needed, so it can be copied straight out of the terminal.
  printf '\n%s%s-- Your PUBLIC key — copy this whole line to the Host --%s\n\n' \
    "$BOLD" "$BLUE" "$RESET"
  printf '%s\n' "$(cat "$KEY.pub")"
  printf '\n'
  warn "Never copy $KEY (no .pub). That one stays on this machine."
  printf '\n'

  if [[ "$HOST_OS" == "windows" ]]; then
    say "On the Host, in PowerShell as Administrator:"
    printf '\n'
    note '  # Administrator account:'
    note '  Add-Content -Path C:\ProgramData\ssh\administrators_authorized_keys -Value "<paste>"'
    note '  icacls C:\ProgramData\ssh\administrators_authorized_keys /inheritance:r'
    note '  icacls C:\ProgramData\ssh\administrators_authorized_keys /grant SYSTEM:F'
    note '  icacls C:\ProgramData\ssh\administrators_authorized_keys /grant Administrators:F'
    printf '\n'
    note '  # Standard account:'
    note '  Add-Content -Path $env:USERPROFILE\.ssh\authorized_keys -Value "<paste>"'
    printf '\n'
    note "Which one you need is prerequisite 8. Guessing wrong gives"
    note "'Permission denied (publickey)' with no hint as to why."
  else
    say "From this machine — asks for the Host password one last time:"
    printf '\n'
    note "  ssh-copy-id -i $KEY.pub -p $SSH_PORT $HOST_USER@$HOST_ADDR"
  fi

  printf '\n  %s%d passed, %d failed%s\n\n' "$BOLD" "$PASSES" "$FAILS" "$RESET"
  exit 1
fi

# --- The Host's shell ---

# Prerequisite 3. Checked early and explicitly: under cmd.exe every later check
# fails for the wrong reason, which is the single worst way to lose an evening.
if [[ "$HOST_OS" == "windows" ]]; then
  SHELL_PROBE=$(rsh 'echo $0' 2>/dev/null)
  if [[ "$SHELL_PROBE" == *bash* || "$SHELL_PROBE" == *sh* ]]; then
    pass "remote default shell is bash"
  else
    fail "remote default shell is not bash (got: ${SHELL_PROBE:-cmd.exe})" \
      "Prerequisite 3 — PowerShell as Administrator on the Host:" \
      "  New-ItemProperty -Path \"HKLM:\\SOFTWARE\\OpenSSH\" -Name DefaultShell \\" \
      "    -Value \"C:\\Program Files\\Git\\bin\\bash.exe\" -PropertyType String -Force" \
      "Nothing below this line is trustworthy until it is fixed."
  fi
fi

# --- Runtimes on the Host ---

# have_remote CMD — is CMD on the Host's PATH? Decided by `command -v`'s EXIT
# CODE, not by reading text.
#
# Parsing output cannot be made safe here. Anything ssh prints on its own
# behalf — a multiplexing error, a post-quantum warning, a banner — arrives on
# the same stream as the command's output, and a check that scans that text
# for "not found" accepts the error as proof the tool is installed. Exit codes
# do not have that failure mode.
have_remote() {
  rsh "command -v $1 >/dev/null 2>&1" >/dev/null
}

# remote_probe CMD — run `CMD --version` on the Host. Sets PROBE_OUT to what it
# said and returns 0 if it ran, 127 if CMD is not on the PATH at all, or its
# own exit code if it is there and fails.
#
# On PATH and runnable are two different questions, and a check that asks only
# the first passes a binary that cannot start. A uv-installed Python is a
# launcher stub that spawns the real interpreter through a chain of paths; the
# stub sits on PATH and answers `command -v` while the spawn fails. The two
# have different remedies, so they need different verdicts.
PROBE_OUT=""
remote_probe() {
  local rc=0
  PROBE_OUT=""
  have_remote "$1" || return 127
  PROBE_OUT=$(rsh "$1 --version 2>&1 | head -3" 2>/dev/null | tr -d '\r') || rc=$?
  (( rc == 0 )) && [[ -z "$PROBE_OUT" ]] && rc=1
  return $rc
}

# broken_remedy CMD RC — the remedy lines for a command that is present but dead.
broken_remedy() {
  printf '%s\n' \
    "It is found and then fails to start, so this is not a PATH problem." \
    "The Host reported:" \
    "  ${PROBE_OUT:-(no output)}" \
    "Record this before fixing it — a tool that runs at the Host's desktop" \
    "but not over SSH is a finding for issue #4."
}

# check_remote_cmd CMD LABEL REMEDY... — one PATH check.
check_remote_cmd() {
  local cmd="$1" label="$2" rc=0; shift 2
  remote_probe "$cmd" || rc=$?
  case $rc in
    0)   pass "$label — $(printf '%s' "$PROBE_OUT" | head -1)" ;;
    127) fail "$label missing from the Host's PATH" "$@" ;;
    *)   local -a m; mapfile -t m < <(broken_remedy)
         fail "$label is on the Host's PATH but will not run (exit $rc)" "${m[@]}" ;;
  esac
}

# A Windows SSH session inherits only the MACHINE PATH. Anything installed with
# "Add to PATH" normally lands in the USER PATH, so it works at the desktop and
# is invisible here. That is the usual cause of these three failing.
PATH_REMEDY=(
  "Installed but not on the SSH PATH? A Windows SSH session sees only the"
  "MACHINE PATH, never your USER PATH. Check with:"
  "  [Environment]::GetEnvironmentVariable('Path','Machine')"
  "Add the directory there in an elevated PowerShell, then: Restart-Service sshd"
)

check_remote_cmd "python" "Python" \
  "Prerequisite 4 — install Python 3.11+, or expose it to SSH." \
  "Hermes will not run without it." "${PATH_REMEDY[@]}"
check_remote_cmd "node" "Node" \
  "Prerequisite 4 — install Node LTS from https://nodejs.org." \
  "Pi will not run without it." "${PATH_REMEDY[@]}"
check_remote_cmd "curl" "curl" \
  "Ships with Git for Windows and with macOS." "${PATH_REMEDY[@]}"

# --- Vendor ---

OLLAMA=$(rsh "curl -sS -m 5 -o /dev/null -w '%{http_code}' http://127.0.0.1:11434/api/tags" 2>/dev/null); require_live
LMS=$(rsh "curl -sS -m 5 -o /dev/null -w '%{http_code}' http://127.0.0.1:1234/api/v1/models" 2>/dev/null); require_live

if [[ "$OLLAMA" == *200* ]]; then
  VENDOR_KIND=ollama; VENDOR_URL="http://127.0.0.1:11434"; VENDOR_PATH="/api/tags"
  pass "Vendor serving — Ollama on 11434"
elif [[ "$LMS" == *200* ]]; then
  VENDOR_KIND=lmstudio; VENDOR_URL="http://127.0.0.1:1234"; VENDOR_PATH="/api/v1/models"
  pass "Vendor serving — LM Studio on 1234"
else
  VENDOR_KIND=""; VENDOR_PATH=""
  fail "no Vendor serving on the Host" \
    "Prerequisite 9. Ollama is easier over SSH:" \
    "  install from https://ollama.com/download, then: ollama pull qwen3:8b" \
    "LM Studio needs a desktop session and Developer -> Start Server."
fi

if [[ "$VENDOR_KIND" == "ollama" ]]; then
  MODELS=$(rsh "ollama list" 2>/dev/null); require_live
  if [[ $(printf '%s' "$MODELS" | wc -l) -gt 1 ]]; then
    pass "Vendor has models"
    note "$(printf '%s' "$MODELS" | tail -n +2 | head -5 | sed 's/^/         /')"
  else
    fail "Vendor has no models pulled" \
      "Prerequisite 9: ollama pull qwen3:8b" \
      "It MUST be tool-calling capable or the capture comes out empty."
  fi
fi

# --- Harnesses ---

HERMES_V=""; PI_V=""
_hrc=0; remote_probe hermes || _hrc=$?
case $_hrc in
  0)   HERMES_V="$(printf '%s' "$PROBE_OUT" | head -1)"; pass "Hermes — $HERMES_V" ;;
  127) fail "Hermes not on the Host's PATH" \
         "Needs its source tree there: pip install -e '.[acp]'" \
         "Cannot be installed remotely. If it will not install, that is a" \
         "RECORDED FINDING for issue #4, not a blocker — carry on with Pi." \
         "${PATH_REMEDY[@]}" ;;
  *)   mapfile -t _m < <(broken_remedy)
       fail "Hermes is on the Host's PATH but will not run (exit $_hrc)" "${_m[@]}" ;;
esac

_prc=0; remote_probe pi || _prc=$?
case $_prc in
  0)   PI_V="$(printf '%s' "$PROBE_OUT" | head -1)"; pass "Pi — $PI_V" ;;
  127) fail "Pi not on the Host's PATH" \
         "The real run offers to install it: npm i -g @earendil-works/pi-coding-agent" \
         "${PATH_REMEDY[@]}" ;;
  *)   mapfile -t _m < <(broken_remedy)
       fail "Pi is on the Host's PATH but will not run (exit $_prc)" "${_m[@]}" ;;
esac

# --- Verdict ---

printf '\n  %s%d passed, %d failed%s\n' "$BOLD" "$PASSES" "$FAILS" "$RESET"

if [[ "$MODE" == "check" ]]; then
  printf '\n'
  if (( FAILS )); then
    say "Fix the failures above and run --check again. Seconds per attempt."
  else
    say "All green. Run without --check to capture."
  fi
  printf '\n'
  exit $(( FAILS > 0 ))
fi

if (( FAILS )); then
  printf '\n'
  warn "$FAILS prerequisite(s) unmet."
  note "Hermes or Pi missing is survivable — issue #4 accepts a recorded reason."
  note "No Vendor, no Python, or a cmd.exe shell is not: the capture would be empty."
  printf '\n'
  confirm "Continue anyway?" || exit 1
fi

save_conf

# ── Capture ───────────────────────────────────────────────────────────────

mkdir -p "$LANDING"
MANIFEST="$LANDING/manifest.txt"
: > "$MANIFEST"
mf() { printf '%s\n' "$1" >> "$MANIFEST"; }

mf "=== Remote Host capture: $(date -Iseconds) ==="
mf "Client: $(uname -a 2>/dev/null || echo unknown)"
mf "Host:   $HOST_USER@$HOST_ADDR:$SSH_PORT ($HOST_OS)"
mf "Vendor: ${VENDOR_KIND:-none} at ${VENDOR_URL:-n/a}"
mf "Hermes: ${HERMES_V:-NOT INSTALLED}"
mf "Pi:     ${PI_V:-NOT INSTALLED}"
mf ""

head2 "Host specs and VRAM"
say "Recorded because issue #4 asks for them in the Answer."
SPECS="$LANDING/host-specs.txt"
if [[ "$HOST_OS" == "windows" ]]; then
  rsh 'systeminfo | grep -E "OS Name|OS Version|Total Physical Memory"' > "$SPECS" 2>&1
  require_live
  rsh 'wmic path win32_VideoController get name,AdapterRAM' >> "$SPECS" 2>&1
else
  rsh 'sw_vers; sysctl -n machdep.cpu.brand_string; sysctl -n hw.memsize' > "$SPECS" 2>&1
  require_live
  rsh 'system_profiler SPDisplaysDataType | head -40' >> "$SPECS" 2>&1
fi
require_live
sed 's/^/    /' "$SPECS"
note "-> host-specs.txt"

head2 "Vendor over a real network interface"
say "The WSL2 run reached the Vendor at 172.25.112.1 — a virtual adapter on the"
say "same box. A second physical machine gives a real NIC. This is the question"
say "one machine could never answer."
printf '\n'
if [[ "$HOST_OS" == "windows" ]]; then
  # The adapter carrying the default route, not simply the first one ipconfig
  # prints. A dev box has several virtual adapters — WSL, VMware, Bluetooth —
  # and picking one of those would test a virtual interface all over again,
  # which is the exact flaw in the WSL2 run this stage exists to correct.
  IFACE=$(rsh "powershell -NoProfile -Command \"(Get-NetIPConfiguration | Where-Object { \\\$_.IPv4DefaultGateway -ne \\\$null } | Select-Object -First 1).IPv4Address.IPAddress\"" 2>/dev/null | tr -d '\r' | grep -oE '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' | head -1)
else
  IFACE=$(rsh "ipconfig getifaddr en0 || ipconfig getifaddr en1" 2>/dev/null | tr -d '\r' | head -1)
fi
require_live
[[ -z "$IFACE" ]] && IFACE="$HOST_ADDR"
say "Host interface address: $IFACE"

if [[ -n "$VENDOR_KIND" ]]; then
  IFACE_URL="${VENDOR_URL/127.0.0.1/$IFACE}"
  CODE=$(rsh "curl -sS -m 10 -o /dev/null -w '%{http_code}' $IFACE_URL$VENDOR_PATH" 2>/dev/null)
  require_live
  if [[ "$CODE" == *200* ]]; then
    printf '  %s  OK%s  Vendor answers on the real interface\n' "$GREEN" "$RESET"
  else
    warn "HTTP $CODE — the Vendor is bound to loopback only."
    note "Ollama:    set OLLAMA_HOST=0.0.0.0 and restart the service"
    note "LM Studio: Developer -> Serve on Local Network"
    note "Revert it afterwards. The hermes-linux run had to, and said so."
  fi
  mf "Vendor over real interface ($IFACE_URL$VENDOR_PATH): HTTP $CODE"
fi

head2 "Install Pi, if missing"
if [[ -z "$PI_V" ]]; then
  if confirm "Install Pi on the Host over SSH now?"; then
    rsh "npm install -g @earendil-works/pi-coding-agent" | tail -5
    require_live
    PI_V=$(rsh "pi --version" 2>/dev/null); require_live
    say "pi: ${PI_V:-still missing}"
    mf "Pi installed during run: ${PI_V:-FAILED}"
  fi
else
  say "Already installed — nothing to do."
fi

head2 "Run the capture wizards on the Host"
say "Reusing capture-hermes.sh and capture-pi.sh unchanged, so the remote"
say "transcripts stay comparable with the local ones."
printf '\n'
scp -q "${SSH_BASE[@]}" -i "$KEY" -P "$SSH_PORT" \
  "$REPO_ROOT/scripts/capture-hermes.sh" \
  "$REPO_ROOT/scripts/capture-pi.sh" \
  "$REPO_ROOT/scripts/acp-capture.py" \
  "$REPO_ROOT/scripts/pi-rpc-capture.py" \
  "$HOST_USER@$HOST_ADDR:" && say "copied 4 scripts to the Host"
printf '\n'
warn "Those wizards are interactive — they need a terminal, not a pipe."
say "Each opens a real SSH session. Drive it at the Host prompt, then exit."
printf '\n'

for w in capture-hermes capture-pi; do
  if confirm "Run $w.sh on the Host now?"; then
    ssh -t -i "$KEY" -p "$SSH_PORT" "$HOST_USER@$HOST_ADDR" "bash ./$w.sh"
    rc=$?
    say "$w.sh exit code: $rc"
    mf "$w.sh exit code: $rc"
  fi
  printf '\n'
done

head2 "Pull the transcripts back"
say "Raw stdout and stderr are the evidence the Event model gets designed"
say "against. Nothing here is a summary."
printf '\n'
DIRS=$(rsh "ls -d hermes-capture-* pi-capture-* 2>/dev/null" 2>/dev/null); require_live
if [[ -z "$DIRS" ]]; then
  warn "No capture directories on the Host — did the wizards run?"
else
  printf '%s\n' "$DIRS" | sed 's/^/    /'
  printf '\n'
  while IFS= read -r d; do
    [[ -z "$d" ]] && continue
    d=$(printf '%s' "$d" | tr -d '\r')
    if confirm "Pull $d?"; then
      scp -q -r "${SSH_BASE[@]}" -i "$KEY" -P "$SSH_PORT" \
        "$HOST_USER@$HOST_ADDR:$d" "$LANDING/" && say "pulled $d"
      mf "pulled: $d"
    fi
  done <<< "$DIRS"
fi

# The limit this Host cannot lift. Written here so the Answer on #4 cannot
# quietly overclaim, the way hermes-linux/README.md records its own caveats.
mf ""
mf "LIMITS OF THIS RUN"
mf "  The Host is $HOST_OS, not bare-metal Linux. The Hermes stdin deadlock"
mf "  (C7) and phantom denial (C9) were disproved only under WSL2, and this"
mf "  run does NOT lift that caveat — a native Linux kernel and its pipe"
mf "  implementation remain untested."
mf "  What this run DOES establish: a separate machine reached over SSH key"
mf "  auth, and a Vendor reached over a real network interface."
mf ""
mf "Finished: $(date -Iseconds)"

head2 "Done"
say "Landed in $LANDING:"
ls -1 "$LANDING" 2>/dev/null | sed 's/^/    /'
printf '\n'
note "Manifest: $MANIFEST"
note "Next: comment the Answer on issue #4, then close it."
printf '\n'
