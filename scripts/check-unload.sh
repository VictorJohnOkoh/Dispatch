#!/usr/bin/env bash
# Load disables the Vendor's evictor for a Session's life, so the Daemon calls
# Unload when that life ends. Every ending path and the boot sweep converge on
# one call in the Daemon instead of each owning part of the Vendor lifecycle.
#
# This is that ownership checked: one production call, in the Daemon.
set -uo pipefail

callers=$(grep -rn --include='*.go' --exclude-dir=.worktrees '\.Unload(' . |
	grep -v '_test\.go:')

count=$(sed '/^$/d' <<<"$callers" | wc -l)
if [ "$count" -ne 1 ] || [[ "$callers" != ./internal/daemon/sessions.go:* ]]; then
	echo "FAIL: Unload must have one production caller in internal/daemon/sessions.go:"
	sed 's/^/  /' <<<"$callers"
	exit 1
fi

echo "Unload: one production call owned by the Daemon lifecycle"
