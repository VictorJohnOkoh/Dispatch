#!/usr/bin/env python3
"""Find a Harness executable that a supervisor can spawn, the way a Daemon would.

Runs ON the Host. Prints JSON.

A Daemon spawns a Harness directly. It does not go through a shell, because the
whole point of owning stdin is owning the process. On Windows, `npm install -g`
leaves shell shims on the PATH -- `opencode`, `opencode.cmd`, `opencode.ps1` --
and none of them can be handed to CreateProcess. So:

    command -v opencode      succeeds
    opencode --version       succeeds, through a shell
    Popen(["opencode"])      FileNotFoundError: [WinError 2]

That is the same shape as the Hermes finding already in the record: a Harness can
pass every installation check and still refuse to run. An orchestrator cannot
learn this by asking whether a tool is installed, only by trying to spawn it.

So this tries each candidate the way the Daemon will, and reports every one --
the failures are the finding, not noise. Exit 0 if something spawned, 1 if
nothing did.

    python resolve-harness-exe.py opencode
    python resolve-harness-exe.py pi --package @earendil-works/pi-coding-agent
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys


def npm_global_root():
    """Where npm puts global packages, or "" if npm cannot say.

    shell=True on Windows because npm is itself a shim, which is the very
    problem this script exists to report. Asking it a question through a shell
    is fine; spawning the Harness through one is not.
    """
    try:
        r = subprocess.run(["npm", "root", "-g"], capture_output=True, text=True,
                           timeout=60, shell=(os.name == "nt"))
        return r.stdout.strip() if r.returncode == 0 else ""
    except Exception:
        return ""


def candidates(name, package):
    """Every path worth trying, the real binary first.

    A .CMD shim spawns fine, but it puts a cmd.exe between the supervisor and
    the Harness, and the supervisor then has to kill through it to stop a
    Session. The package's own binary has no such wrapper, so it is preferred
    whenever it exists. Both are still reported: which ones are shell-only is
    the finding.
    """
    found = []

    root = npm_global_root()
    if root and package:
        pkg_dir = os.path.join(root, *package.split("/"))
        for tail in (name + ".exe", name):
            p = os.path.join(pkg_dir, "bin", tail)
            if os.path.exists(p):
                found.append(p)

    on_path = shutil.which(name)
    if on_path:
        found.append(on_path)

    # The bare name is what a naive supervisor passes, and on Windows it is the
    # one that fails. Try it last so the report records that it was tried.
    found.append(name)

    seen = set()
    return [c for c in found if not (c in seen or seen.add(c))]


def try_spawn(path, timeout):
    """Spawn path --version with no shell. This is exactly what the Daemon does."""
    try:
        r = subprocess.run([path, "--version"], capture_output=True, text=True,
                           timeout=timeout)
        said = (r.stdout or r.stderr or "").strip().splitlines()
        return r.returncode == 0, (said[0] if said else ""), r.returncode
    except FileNotFoundError as exc:
        # The interesting failure. Present on the PATH, unusable by a supervisor.
        return False, "%s: %s" % (type(exc).__name__, exc), None
    except Exception as exc:
        return False, "%s: %s" % (type(exc).__name__, exc), None


def main():
    p = argparse.ArgumentParser()
    p.add_argument("name", help="the Harness command, e.g. opencode")
    p.add_argument("--package", default="", help="npm package name, to find the real binary")
    p.add_argument("--timeout", type=float, default=60.0)
    args = p.parse_args()

    if not args.package and args.name == "opencode":
        args.package = "opencode-ai"

    tried = []
    chosen = None
    for path in candidates(args.name, args.package):
        ok, said, rc = try_spawn(path, args.timeout)
        tried.append({
            "path": path,
            "direct_spawn_ok": ok,
            "exit_code": rc,
            "said": said,
        })
        if ok and chosen is None:
            chosen = path

    # chosen_spawn is the one to put in a command STRING. acp-capture.py splits
    # --agent-cmd with shlex, which treats a backslash as an escape, so
    # C:\Users\... arrives as C:Users... and the spawn fails on a path that was
    # never tried. Forward slashes survive shlex and CreateProcess accepts them.
    print(json.dumps({
        "name": args.name,
        "chosen": chosen,
        "chosen_spawn": chosen.replace("\\", "/") if chosen else None,
        "shell_only": [t["path"] for t in tried if not t["direct_spawn_ok"]],
        "tried": tried,
    }, indent=2))
    return 0 if chosen else 1


if __name__ == "__main__":
    sys.exit(main())
