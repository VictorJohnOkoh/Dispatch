#!/usr/bin/env bash
#
# Build the two binaries SPEC.md behaviour 12 needs — issue #54.
#
# The check is a Hub that requires one protocol version against a Daemon that
# serves another. One tree cannot make both, because the version is one constant
# both roles read, so this builds the Daemon from the tree as it stands and the
# Hub from a copy with that constant raised.
#
#   bash scripts/handshake-builds.sh [outdir]     default outdir: build/handshake
#
# It writes two binaries and a record.txt naming the commit and the versions, so
# the run can be repeated from the same two builds. See docs/checks/handshake.md.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
out=${1:-"$root/build/handshake"}
commit=$(git -C "$root" rev-parse --short HEAD)
served=$(grep -oP 'const Version = \K[0-9]+' "$root/internal/protocol/protocol.go")
asked=$((served + 1))

mkdir -p "$out"
out=$(cd "$out" && pwd)

# Both binaries come from the commit rather than from the working tree, so the
# record below is enough to make them again. The copies are thrown away.
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
git -C "$root" archive HEAD | tar -x -C "$work"

# The old Daemon: the commit, unchanged.
(cd "$work" && go build -o "$out/dispatch-daemon" ./cmd/dispatch)

# The new Hub: the same commit with the version raised, so it requires one the
# Daemon above cannot serve.
sed -i "s/^const Version = $served\$/const Version = $asked/" "$work/internal/protocol/protocol.go"
if ! grep -q "^const Version = $asked\$" "$work/internal/protocol/protocol.go"; then
  echo "the version was not raised, so both builds would speak $served" >&2
  exit 1
fi
(cd "$work" && go build -o "$out/dispatch-hub" ./cmd/dispatch)

cat > "$out/record.txt" <<RECORD
Behaviour 12, the Handshake. Built $(date -u +%Y-%m-%dT%H:%M:%SZ) from $commit.

dispatch-daemon  protocol $served, the commit unchanged. Runs on the Host.
dispatch-hub     protocol $asked, the same commit with internal/protocol/protocol.go
                 raised by one. Runs on the Client machine.

Expect: the Host reads Incompatible and it is never retried.
RECORD

echo "two builds in $out"
cat "$out/record.txt"
