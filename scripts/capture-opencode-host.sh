#!/usr/bin/env bash
#
# Drive `opencode acp` on a Host over SSH and answer the three gates — issue #16.
#
# Runs on the CLIENT. Drives the Host over SSH. The Host is the machine with the
# GPU, the Vendor and OpenCode on it; the Client is the machine you are sitting
# at. Nothing here runs OpenCode locally, on purpose: the development machine is
# where Hermes looked healthy, and the Host is where it died.
#
#   bash scripts/capture-opencode-host.sh --check    preflight only, changes nothing
#   bash scripts/capture-opencode-host.sh            preflight, then the real run
#
# Read docs/research/remote-host-prerequisites.md first and do it at the Host.
# Prerequisite 3 (Git Bash as the SSH default shell) is required here, because
# every remote command below is POSIX.
#
# What this proves, and what it only records:
#
#   gate 1  a tool call completes on the Host over SSH          FATAL if it fails
#   gate 2  session/request_permission fires per tool class     recoverable
#   gate 3  terminal Events counted per tool class              knowledge, not perfection
#
# The gates are counted by scripts/opencode-gates.py from the captured frames,
# not by this script reading its own output. acp-capture.py is reused unchanged
# so the OpenCode transcripts stay comparable with the Hermes ones.

set -uo pipefail

# ── Output ────────────────────────────────────────────────────────────────

if [[ -t 1 ]] && command -v tput >/dev/null 2>&1 && [[ "$(tput colors 2>/dev/null || echo 0)" -ge 8 ]]; then
  BOLD=$(tput bold); DIM=$(tput dim); RESET=$(tput sgr0)
  BLUE=$(tput setaf 4); GREEN=$(tput setaf 2); YELLOW=$(tput setaf 3); RED=$(tput setaf 1)
else
  BOLD=""; DIM=""; RESET=""; BLUE=""; GREEN=""; YELLOW=""; RED=""
fi

say()   { printf '  %s\n' "$1"; }
note()  { printf '  %s%s%s\n' "$DIM" "$1" "$RESET"; }
warn()  { printf '  %s! %s%s\n' "$YELLOW" "$1" "$RESET"; }
head2() { printf '\n%s%s== %s%s\n\n' "$BOLD" "$BLUE" "$1" "$RESET"; }

FAILED=0
pass() { printf '  %sPASS%s  %s\n' "$GREEN" "$RESET" "$1"; }
fail() {
  printf '  %sFAIL%s  %s\n' "$RED" "$RESET" "$1"; shift
  local line; for line in "$@"; do printf '        %s%s%s\n' "$DIM" "$line" "$RESET"; done
  FAILED=$((FAILED + 1))
}

ask() {
  local prompt="$1" default="${2:-}" reply
  if [[ -n "$default" ]]; then
    read -r -p "  $prompt [$default]: " reply
    printf '%s' "${reply:-$default}"
  else
    read -r -p "  $prompt: " reply
    printf '%s' "$reply"
  fi
}

confirm() {
  local reply
  read -r -p "  $1 [y/N] " reply
  [[ "$reply" == [yY]* ]]
}

# ── Config ────────────────────────────────────────────────────────────────

MODE="run"
VENDOR_PICK=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --check)  MODE="check" ;;
    --vendor) VENDOR_PICK="${2:-}"; shift ;;
    -h|--help) sed -n '2,26p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) warn "unknown argument: $1"; exit 2 ;;
  esac
  shift
done

REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
LANDING="$REPO_ROOT/docs/research/captures/opencode"
CONF="$HOME/.opencode-host.env"
KEY="$HOME/.ssh/id_ed25519_capstone_host"

# Where the capture lives on the Host. Relative to the Host's home directory so
# scp needs no absolute path and no shell expansion on the far side.
REMOTE_DIR="capstone-opencode"

HOST_ADDR=""; HOST_USER=""; SSH_PORT=""
VENDOR_KIND=""; VENDOR_URL=""; VENDOR_MODEL=""
# shellcheck disable=SC1090
[[ -f "$CONF" ]] && source "$CONF"

save_conf() {
  cat > "$CONF" <<EOF
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

# ControlMaster=no and ControlPath=none are not tidiness. A stale multiplexing
# socket makes ssh print "mux_client_request_session: read from master failed"
# INSTEAD of running the command, and a check that reads stdout accepts that
# text as a version string. That has already happened once in this project.
SSH_BASE=(-o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new
          -o ControlMaster=no -o ControlPath=none)

SSH_ERR_FILE="${TMPDIR:-/tmp}/opencode-host-ssh-err.$$"
_TOP_PID=$$
trap 'rm -f "$SSH_ERR_FILE"' EXIT

# rsh CMD — run CMD on the Host, print its stdout, return its exit code.
#
# ssh's OWN stderr goes to a file and never into the captured output. The client
# writes there on its own behalf on every connection, and merged, that prose has
# already landed in this project's research record as a version number. A caller
# that wants the remote stderr merges it REMOTELY, inside the command string.
rsh() {
  local rc=0 out
  out=$(ssh -n "${SSH_BASE[@]}" -i "$KEY" -p "$SSH_PORT" "$HOST_USER@$HOST_ADDR" "$1" 2>"$SSH_ERR_FILE") || rc=$?
  printf '%s' "$out"
  if (( rc == 255 )); then
    kill -s TERM "$_TOP_PID" 2>/dev/null
    exit 255
  fi
  return $rc
}

# rsh_live CMD — same, but stream the Host's stdout instead of capturing it.
# Used for the capture runs, which are slow and worth watching. -n keeps the
# Host's process off this terminal's stdin, which is also the Daemon's
# situation: no TTY, and the supervisor owns the pipe.
rsh_live() {
  local rc=0
  ssh -n "${SSH_BASE[@]}" -i "$KEY" -p "$SSH_PORT" "$HOST_USER@$HOST_ADDR" "$1" 2>"$SSH_ERR_FILE" || rc=$?
  if (( rc == 255 )); then
    kill -s TERM "$_TOP_PID" 2>/dev/null
    exit 255
  fi
  return $rc
}

_ssh_died() {
  printf '\n'
  warn "SSH failed talking to $HOST_USER@$HOST_ADDR:$SSH_PORT — stopping."
  [[ -s "$SSH_ERR_FILE" ]] && note "  $(tail -3 "$SSH_ERR_FILE" | tr '\n' ' ')"
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

# ── Details ───────────────────────────────────────────────────────────────

printf '\n%s%s  OpenCode ACP on a Host — issue #16%s\n' "$BOLD" "$BLUE" "$RESET"
if [[ "$MODE" == "check" ]]; then
  note "  preflight only — nothing will be changed"
else
  note "  full run — preflight, then three captures"
fi
printf '\n'

[[ -z "$HOST_ADDR" ]] && HOST_ADDR=$(ask "Host address (IP or hostname)")
[[ -z "$HOST_USER" ]] && HOST_USER=$(ask "Username on the Host")
[[ -z "$SSH_PORT"  ]] && SSH_PORT=$(ask "SSH port" "22")
save_conf

# ── Preflight ─────────────────────────────────────────────────────────────

head2 "Preflight"

# The gates are counted on the Client, so the Client needs a Python too. Checked
# first, so a broken interpreter stops the run here rather than after three slow
# captures. Run a real script rather than --version, for the same reason the
# Host's probe does: on the PATH and able to run are different questions, and a
# shim answers the first while failing the second.
if python -c "import json,glob,argparse" >/dev/null 2>&1; then
  pass "Client Python runs — $(python --version 2>&1 | tr -d '\r')"
else
  fail "the Client's python is on the PATH and will not run a script" \
    "Prerequisite 4, at the Client. The gates are counted here, not on the" \
    "Host, so the run stops without it." \
    "Install Python 3.11+ and put a working one first on the PATH." \
    "It answers --version, so this is a shim that cannot spawn its child" \
    "rather than a missing interpreter. Record which, then fix it."
fi

if [[ -f "$KEY" ]]; then
  pass "SSH key present — $KEY"
else
  fail "no SSH key at $KEY" \
    "Prerequisite 5. On the Client:" \
    "  ssh-keygen -t ed25519 -f $KEY -C capstone-host" \
    "then add the public half to the Host's authorized_keys."
fi

WHOAMI=$(rsh "whoami" 2>/dev/null | tr -d '\r')
if [[ -n "$WHOAMI" ]]; then
  pass "SSH reachable — the Host answers as $WHOAMI"
else
  fail "cannot reach the Host over SSH" \
    "Check the address, the port and that sshd is running on the Host." \
    "  ssh -vvv -i $KEY -p $SSH_PORT $HOST_USER@$HOST_ADDR"
fi

# Prerequisite 3. Every remote command in this script is POSIX, so a cmd.exe
# default shell fails in a way that reads like a broken script.
SHELL_OK=$(rsh 'printf posix-shell-ok' 2>/dev/null | tr -d '\r')
if [[ "$SHELL_OK" == "posix-shell-ok" ]]; then
  pass "Host's SSH shell is POSIX"
else
  fail "the Host's SSH shell is not POSIX" \
    "Prerequisite 3. In an elevated PowerShell on the Host:" \
    '  New-ItemProperty -Path "HKLM:\SOFTWARE\OpenSSH" -Name DefaultShell `' \
    '    -Value "C:\Program Files\Git\bin\bash.exe" -PropertyType String -Force' \
    "then: Restart-Service sshd"
fi

# A Windows SSH session inherits only the MACHINE PATH. Anything installed with
# "Add to PATH" lands in the USER PATH, works at the desktop, and is invisible
# here. That is the usual cause of the next two failing.
PATH_REMEDY=(
  "Installed but not on the SSH PATH? A Windows SSH session sees only the"
  "MACHINE PATH, never your USER PATH. Check with:"
  "  [Environment]::GetEnvironmentVariable('Path','Machine')"
  "Add the directory there in an elevated PowerShell, then: Restart-Service sshd"
)

# On PATH and able to run are different questions, and a check that asks only
# the first passes a launcher stub that cannot start. Keep stderr off the
# success path: these tools print their version to stdout, so a stub that dies
# writes to stderr and leaves stdout empty. Merge the two and a failure message
# becomes the version.
PROBE_OUT=""
remote_probe() {
  local rc=0
  PROBE_OUT=""
  rsh "command -v $1 >/dev/null 2>&1" >/dev/null || return 127
  PROBE_OUT=$(rsh "$1 --version 2>/dev/null" 2>/dev/null | tr -d '\r' | head -3) || rc=$?
  (( rc == 0 )) && [[ -z "$PROBE_OUT" ]] && rc=1
  (( rc != 0 )) && PROBE_OUT=$(rsh "$1 --version 2>&1" 2>/dev/null | tr -d '\r' | head -3)
  return $rc
}

check_remote_cmd() {
  local cmd="$1" label="$2" rc=0; shift 2
  remote_probe "$cmd" || rc=$?
  case $rc in
    0)   pass "$label — $(printf '%s' "$PROBE_OUT" | head -1)" ;;
    127) fail "$label missing from the Host's PATH" "$@" ;;
    *)   fail "$label is on the Host's PATH but will not run (exit $rc)" \
              "It is found and then fails to start, so this is not a PATH problem." \
              "The Host reported:" \
              "  ${PROBE_OUT:-(no output)}" \
              "Record this before fixing it. Hermes failed exactly here, and that" \
              "failure is why OpenCode is being captured at all." ;;
  esac
}

check_remote_cmd "opencode" "OpenCode" \
  "Install it on the Host: npm install -g opencode-ai" "${PATH_REMEDY[@]}"
check_remote_cmd "python" "Python" \
  "acp-capture.py runs ON the Host, so the Host needs Python 3.11+." "${PATH_REMEDY[@]}"
check_remote_cmd "curl" "curl" \
  "Ships with Git for Windows and with macOS." "${PATH_REMEDY[@]}"

# `opencode acp` in a --help listing is a claim, not a capability. Hermes shipped
# docs for HTTP endpoints it did not have, and reversing that cost a ticket.
if rsh "opencode acp --help >/dev/null 2>&1"; then
  pass "opencode acp answers --help"
  note "  Through a shell. Whether a supervisor can spawn it is a different"
  note "  question, settled later by resolve-harness-exe.py."
else
  fail "opencode acp does not answer --help" \
    "The subcommand may not exist in this build. Check the Host's version," \
    "and record the result: this is gate 1 failing early, which means v1 ships" \
    "Pi alone (ADR 0003)."
fi

# --- Vendor ---

head2 "Vendor on the Host"
say "All three are probed and all three results are recorded. A Vendor that is"
say "not running is an observation, not a crash."
printf '\n'

FOUND_KINDS=()
FOUND_URLS=()
for v in "ollama|http://127.0.0.1:11434|/api/tags" \
         "lmstudio|http://127.0.0.1:1234|/api/v1/models" \
         "llamaswap|http://127.0.0.1:8080|/v1/models"; do
  IFS='|' read -r vk vu vp <<< "$v"
  code=$(rsh "curl -sS -m 5 -o /dev/null -w '%{http_code}' $vu$vp" 2>/dev/null | tr -d '\r')
  if [[ "$code" == *200* ]]; then
    note "  $vk at $vu$vp — HTTP $code"
    FOUND_KINDS+=("$vk"); FOUND_URLS+=("$vu")
  else
    note "  $vk at $vu$vp — HTTP ${code:-000} (not serving)"
  fi
done
printf '\n'

if (( ${#FOUND_KINDS[@]} == 0 )); then
  fail "no Vendor serving on the Host" \
    "OpenCode has nothing to talk to. Start one:" \
    "  ollama serve            (then: ollama pull qwen3:8b)" \
    "  llama-swap --config ... (127.0.0.1:8080)" \
    "  LM Studio needs a desktop session and Developer -> Start Server."
else
  pass "Vendor serving — ${FOUND_KINDS[*]}"
fi

# Reaching all three Vendors is recorded, not gated (ADR 0003), so one is picked
# for the run and the rest are noted. Re-run with --vendor to cover another.
if (( ${#FOUND_KINDS[@]} > 0 )); then
  VENDOR_KIND=""
  if [[ -n "$VENDOR_PICK" ]]; then
    for i in "${!FOUND_KINDS[@]}"; do
      [[ "${FOUND_KINDS[$i]}" == "$VENDOR_PICK" ]] && { VENDOR_KIND="$VENDOR_PICK"; VENDOR_URL="${FOUND_URLS[$i]}"; }
    done
    [[ -z "$VENDOR_KIND" ]] && warn "--vendor $VENDOR_PICK is not serving; falling back to the first that is"
  fi
  if [[ -z "$VENDOR_KIND" ]]; then
    VENDOR_KIND="${FOUND_KINDS[0]}"; VENDOR_URL="${FOUND_URLS[0]}"
  fi
  say "Using $VENDOR_KIND at $VENDOR_URL"
  if (( ${#FOUND_KINDS[@]} > 1 )); then
    note "  others serving: ${FOUND_KINDS[*]} — re-run with --vendor <kind> to cover them"
  fi

  # Ask the Vendor what it has rather than trusting a typed string. All three
  # answer the OpenAI-compatible listing, and that is the id OpenCode's
  # provider block needs.
  MODELS=$(rsh "curl -sS -m 10 $VENDOR_URL/v1/models" 2>/dev/null | tr -d '\r')
  MODEL_IDS=$(printf '%s' "$MODELS" | grep -oE '"id"[[:space:]]*:[[:space:]]*"[^"]+"' | sed 's/.*"\([^"]*\)"$/\1/')
  if [[ -z "$MODEL_IDS" ]]; then
    fail "the Vendor served no model ids at $VENDOR_URL/v1/models" \
      "It answered the health probe and then listed nothing usable." \
      "The Host said: $(printf '%s' "$MODELS" | head -2)"
  else
    pass "Vendor offers $(printf '%s\n' "$MODEL_IDS" | grep -c .) model(s)"
    printf '%s\n' "$MODEL_IDS" | head -8 | sed 's/^/         /'
  fi
fi

printf '\n'
if (( FAILED > 0 )); then
  warn "$FAILED check(s) failed. Fix them, then run --check again."
  printf '\n'
  exit 1
fi
pass "preflight clean"

if [[ "$MODE" == "check" ]]; then
  printf '\n'
  say "Preflight only. Nothing was changed. Drop --check to capture."
  printf '\n'
  exit 0
fi

# ── Model ─────────────────────────────────────────────────────────────────

head2 "Model"
if [[ -z "$VENDOR_MODEL" ]] || ! printf '%s\n' "$MODEL_IDS" | grep -qx "$VENDOR_MODEL"; then
  DEFAULT_MODEL=$(printf '%s\n' "$MODEL_IDS" | head -1)
  say "Pick one the Vendor actually serves:"
  printf '%s\n' "$MODEL_IDS" | head -8 | sed 's/^/         /'
  printf '\n'
  VENDOR_MODEL=$(ask "Model id" "$DEFAULT_MODEL")
fi
if printf '%s\n' "$MODEL_IDS" | grep -qx "$VENDOR_MODEL"; then
  pass "Model — $VENDOR_MODEL"
else
  fail "$VENDOR_MODEL is not in the Vendor's list" \
    "A model the Vendor does not serve fails inside OpenCode, where the error" \
    "looks like a Harness defect rather than a configuration one."
  exit 1
fi
save_conf

# ── Stage the Host ────────────────────────────────────────────────────────

head2 "Stage the Session on the Host"

mkdir -p "$LANDING"
MANIFEST="$LANDING/manifest.txt"
: > "$MANIFEST"
mf() { printf '%s\n' "$1" >> "$MANIFEST"; }

mf "=== OpenCode ACP on a Host: $(date -Iseconds) ==="
mf "Issue:  #16   ADR: 0003"
mf "Client: $(uname -a 2>/dev/null || echo unknown)"
mf "Host:   $HOST_USER@$HOST_ADDR:$SSH_PORT"
mf "Vendor: $VENDOR_KIND at $VENDOR_URL"
mf "Model:  $VENDOR_MODEL"
mf "OpenCode: $(rsh 'opencode --version 2>/dev/null' | tr -d '\r' | head -1)"
mf ""

rsh "rm -rf $REMOTE_DIR && mkdir -p $REMOTE_DIR/work" >/dev/null
REMOTE_ABS=$(rsh "cd $REMOTE_DIR && pwd" 2>/dev/null | tr -d '\r')
REMOTE_WORK="$REMOTE_ABS/work"
say "Session working directory: $REMOTE_WORK"
mf "Session working directory: $REMOTE_WORK"

# The per-Session config that ADR 0003 assumes the Daemon can write. Two things
# it is testing at once: that OpenCode reads configuration from the Session's
# working directory, and that "ask" on every tool class actually makes the
# Harness ask. If the Harness is not asking for everything, gate 2 measures the
# config rather than the Harness.
STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"; rm -f "$SSH_ERR_FILE"' EXIT

cat > "$STAGE/opencode.json" <<EOF
{
  "\$schema": "https://opencode.ai/config.json",
  "model": "capstone/$VENDOR_MODEL",
  "permission": {
    "edit": "ask",
    "bash": "ask",
    "webfetch": "ask"
  },
  "provider": {
    "capstone": {
      "name": "Capstone Vendor ($VENDOR_KIND)",
      "npm": "@ai-sdk/openai-compatible",
      "options": {
        "baseURL": "$VENDOR_URL/v1",
        "apiKey": "not-required-for-a-local-vendor"
      },
      "models": {
        "$VENDOR_MODEL": { "name": "$VENDOR_MODEL" }
      }
    }
  }
}
EOF

# The file the read-class prompt reads. A known word, so a wrong answer is
# obvious and cannot be a hallucination that happens to look right.
printf 'pomegranate\n' > "$STAGE/notes.txt"

scp -q "${SSH_BASE[@]}" -i "$KEY" -P "$SSH_PORT" \
  "$STAGE/opencode.json" "$STAGE/notes.txt" \
  "$HOST_USER@$HOST_ADDR:$REMOTE_WORK/" && say "config and fixture copied"
scp -q "${SSH_BASE[@]}" -i "$KEY" -P "$SSH_PORT" \
  "$REPO_ROOT/scripts/acp-capture.py" \
  "$REPO_ROOT/scripts/resolve-harness-exe.py" \
  "$HOST_USER@$HOST_ADDR:$REMOTE_ABS/" && say "acp-capture.py copied, unchanged"

cp "$STAGE/opencode.json" "$LANDING/session-opencode.json"

# --- Resolve OpenCode the way the Daemon will ---
#
# The Daemon spawns a Harness directly, with no shell. On Windows the PATH entry
# is a shim that CreateProcess cannot run, so `command -v opencode` passes and
# the spawn still fails. Settle it by spawning, not by asking.
head2 "Resolve the Harness the way a supervisor would"
rsh "cd $REMOTE_ABS && python resolve-harness-exe.py opencode 2>&1" \
  > "$LANDING/harness-resolution.json"
HARNESS_EXE=$(python - "$LANDING/harness-resolution.json" <<'PY'
import json, sys
try:
    with open(sys.argv[1], encoding="utf-8") as fh:
        print(json.load(fh).get("chosen_spawn") or "")
except Exception:
    print("")
PY
)
SHELL_ONLY=$(python - "$LANDING/harness-resolution.json" <<'PY'
import json, sys
try:
    with open(sys.argv[1], encoding="utf-8") as fh:
        print(", ".join(json.load(fh).get("shell_only") or []))
except Exception:
    print("")
PY
)

if [[ -z "$HARNESS_EXE" ]]; then
  fail "nothing named opencode could be spawned on the Host" \
    "It is on the PATH and it will not start under a supervisor. That is gate 1" \
    "failing, and under ADR 0003 gate 1 is fatal: v1 ships Pi alone." \
    "The Host's attempts are recorded in harness-resolution.json." \
    "Record this before fixing it — it is the same shape as the Hermes finding."
  mf "Harness resolution: FAILED, nothing spawnable"
  printf '\n'
  exit 1
fi
pass "spawnable — $HARNESS_EXE"
[[ -n "$SHELL_ONLY" ]] && note "  shell-only, unusable by a supervisor: $SHELL_ONLY"
mf "Harness spawn path: $HARNESS_EXE"
mf "Shell-only paths:   ${SHELL_ONLY:-none}"

# --- Does OpenCode read the working directory? ---
#
# Recorded, not gated. ADR 0003's per-Session config depends on it: if OpenCode
# ignores the working directory, the Model cannot be chosen per Session and
# configuration falls back to a manual per-Host prerequisite.
head2 "Where OpenCode reads configuration from"
CFG_PROBE=$(rsh "cd $REMOTE_WORK && opencode models 2>&1" 2>/dev/null | tr -d '\r')
printf '%s\n' "$CFG_PROBE" | head -12 | sed 's/^/         /'
printf '\n'
if printf '%s\n' "$CFG_PROBE" | grep -q "^capstone/"; then
  pass "the working directory's opencode.json is read — per-Session config works"
  CFG_FINDING="working-directory config is read; per-Session config holds"
else
  warn "the working directory's opencode.json was NOT picked up"
  note "  ADR 0003's per-Session config assumption is reversed by this."
  note "  Record it, then fall back to a manual per-Host config."
  CFG_FINDING="working-directory config IGNORED; ADR 0003 per-Session assumption fails"
fi
mf "Config discovery: $CFG_FINDING"
printf '%s\n' "$CFG_PROBE" > "$LANDING/config-discovery.txt"

# ── Capture ───────────────────────────────────────────────────────────────

head2 "Three runs, one per tool class"
say "One class per run, so the counts cannot be confused with each other."
printf '\n'

# Each prompt is written to make exactly one tool class fire, and to have an
# answer that can be checked. --permission allow keeps the gate firing per call
# rather than disabling it, which is what gate 2 counts.
run_capture() {
  local label="$1" prompt="$2" rc=0
  printf '  %s%s%s  %s\n' "$BOLD" "$label" "$RESET" "$prompt"
  rsh_live "cd $REMOTE_WORK && python $REMOTE_ABS/acp-capture.py \
    --agent-cmd '$HARNESS_EXE acp' \
    --cwd '$REMOTE_WORK' \
    --outdir '$REMOTE_ABS/out' \
    --label '$label' \
    --permission allow \
    --timeout 300 \
    --prompt '$prompt' 2>&1" || rc=$?
  if (( rc == 0 )); then
    say "$label finished"
  else
    warn "$label exited $rc — kept, because a failure is a finding"
  fi
  mf "run $label: exit $rc"
  printf '\n'
  return 0
}

run_capture "read" \
  "Read the file notes.txt in this directory and reply with only the single word it contains."
run_capture "edit" \
  "Create a file called out.txt in this directory whose entire contents are the word banana."
run_capture "execute" \
  "Run the shell command: echo capstone-probe. Then reply with exactly what it printed."

# ── Land the artefacts ────────────────────────────────────────────────────

head2 "Bring the capture home"

scp -q "${SSH_BASE[@]}" -i "$KEY" -P "$SSH_PORT" \
  "$HOST_USER@$HOST_ADDR:$REMOTE_ABS/out/*" "$LANDING/" 2>/dev/null \
  && say "artefacts copied to $LANDING" \
  || warn "nothing came back from $REMOTE_ABS/out — the runs produced no files"

# What the Session actually did on disk, which the frames alone cannot show.
rsh "ls -la $REMOTE_WORK 2>&1; echo '--- out.txt ---'; cat $REMOTE_WORK/out.txt 2>&1" \
  > "$LANDING/workdir-after.txt" 2>/dev/null
say "workdir listing -> workdir-after.txt"

# ── Gates ─────────────────────────────────────────────────────────────────

head2 "The three gates"

GATE_NOTE="Host $HOST_USER@$HOST_ADDR:$SSH_PORT over SSH, no TTY, supervisor owned stdin; Vendor $VENDOR_KIND, Model $VENDOR_MODEL"
python "$REPO_ROOT/scripts/opencode-gates.py" \
  --capture-dir "$LANDING" \
  --out "$LANDING/gates.json" \
  --gate1-note "$GATE_NOTE"
GATE_RC=$?

{
  printf '\n=== gates ===\n'
  printf 'gate note: %s\n' "$GATE_NOTE"
  printf 'gates.json exit: %s  (0 all pass, 1 a gate failed, 3 something unknown)\n' "$GATE_RC"
} >> "$MANIFEST"

printf '\n'
case $GATE_RC in
  0) say "All three gates answered and passed."
     say "OpenCode enters v1. Update CONTEXT.md and the sketch, then close #16." ;;
  1) warn "A gate failed. That is a result, not an error."
     note "  Gate 1 failing is fatal: v1 ships Pi alone (ADR 0003)."
     note "  Gate 2 failing is recoverable: write \"deny\" for the silent classes." ;;
  3) warn "Something is still unknown — usually a tool class that never ran."
     note "  Gate 3 asks for knowledge, so an unexercised class is not an answer." ;;
  *) warn "The gate script itself failed (exit $GATE_RC). Nothing was concluded." ;;
esac

printf '\n'
say "Capture:  $LANDING"
say "Gates:    $LANDING/gates.json"
say "Manifest: $MANIFEST"
printf '\n'
note "Write the findings to docs/research/opencode-acp-host.md and answer #16."
printf '\n'
