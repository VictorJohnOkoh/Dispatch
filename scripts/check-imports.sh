#!/usr/bin/env bash
# The two import fences from ADR 0010. Each package is listed on its own,
# because one missing directory makes go list fail every pattern at once and
# print nothing, which a single combined command cannot tell from a pass.
#
# This checks imports, not compilation: go list -deps reads import declarations
# and exits 0 on a package that does not build. go build ./... is the CI job
# that catches that.
set -uo pipefail

module=github.com/VictorJohnOkoh/Dispatch
status=0
checked=0

# deps prints the packages in this module that ./internal/$1/... reaches,
# excluding the package itself and anything nested under it. It returns 1 if
# go list failed.
deps() {
	go list -deps "./internal/$1/..." | grep "^$module/" | grep -Ev "^$module/internal/$1(/|\$)"
	return "${PIPESTATUS[0]}"
}

# Fence one: a Daemon knows only its own Host, so it never reaches the Hub.
if [ -d internal/daemon ]; then
	checked=$((checked + 1))
	if ! reached=$(deps daemon); then
		echo "FAIL: go list failed for internal/daemon"
		status=1
	elif grep -q "^$module/internal/hub" <<<"$reached"; then
		echo "FAIL: internal/daemon imports internal/hub"
		status=1
	fi
fi

# Fence two: the four L0 packages depend on nothing else in this module.
for pkg in event vendors workspace protocol; do
	[ -d "internal/$pkg" ] || continue
	checked=$((checked + 1))
	if ! reached=$(deps "$pkg"); then
		echo "FAIL: go list failed for internal/$pkg"
		status=1
	elif [ -n "$reached" ]; then
		echo "FAIL: L0 package internal/$pkg has project dependencies:"
		sed 's/^/  /' <<<"$reached"
		status=1
	fi
done

[ "$status" -eq 0 ] && echo "import fences: ok ($checked of 5 packages exist)"
exit "$status"
