#!/usr/bin/env bash
# Unload is in the Vendor Adapter interface because all three Vendors need one and
# leaving it out would put three Vendors' unload mechanics in the Daemon. Nothing
# in v1 calls it: what decides when VRAM comes back is admission policy, and that
# is not built yet.
#
# This is that claim, checked. The day a caller appears, this script fails and the
# claim gets corrected rather than left quietly wrong.
set -uo pipefail

callers=$(grep -rn --include='*.go' --exclude-dir=.worktrees '\.Unload(' . |
	grep -v '_test\.go:' |
	grep -v '^\./internal/vendors/')

if [ -n "$callers" ]; then
	echo "FAIL: v1 code calls Unload, which SPEC.md says nothing does:"
	sed 's/^/  /' <<<"$callers"
	exit 1
fi

echo "Unload: implemented by every Adapter, called by no v1 code path"
