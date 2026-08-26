#!/usr/bin/env python3
"""Answer the three gates in issue #16 from captured ACP frames.

Reads the ``*-frames.jsonl`` files that ``acp-capture.py`` writes and reports,
per tool class:

    gate 2   how many times ``session/request_permission`` fired
    gate 3   how many tool calls started, and how many reached a terminal status

Gate 1 is derived: it passes when at least one run ended with a stop reason and
at least one tool call reached a terminal status. A Harness that never started
leaves no frames at all, which is the failure this gate exists to catch. The
wizard can override the verdict with ``--gate1``, and supplies the transport
detail through ``--gate1-note`` either way.

Why this is separate from ``acp-capture.py``: that script and its outputs are
frozen research artefacts, reused unchanged so the OpenCode transcripts stay
comparable with the Hermes ones. Counting is a different job from capturing, and
it runs on the Client afterwards against files that already exist.

The ACP shapes this reads, all from the agent's own frames:

    session/update      {update: {sessionUpdate: "tool_call",
                                  toolCallId, kind, status}}
    session/update      {update: {sessionUpdate: "tool_call_update",
                                  toolCallId, status}}
    session/request_permission
                        {toolCall: {toolCallId, kind, ...}, options: [...]}

A tool call is terminal at status ``completed`` or ``failed``. Anything still
``pending`` or ``in_progress`` when the Session ended is a call the Client would
render as running forever. That is the Hermes defect, counted the same way.
"""

from __future__ import annotations

import argparse
import glob
import json
import os
import sys
from collections import Counter

TERMINAL = {"completed", "failed"}
GATE_CLASSES = ("read", "edit", "execute")


def _load_frames(path):
    """Yield parsed frames. A malformed line is a finding, not a crash."""
    bad = 0
    with open(path, encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            try:
                yield json.loads(line)
            except json.JSONDecodeError:
                bad += 1
    if bad:
        print("  ! %d unparseable line(s) in %s" % (bad, os.path.basename(path)),
              file=sys.stderr)


class Run:
    """One capture label, counted."""

    def __init__(self, label):
        self.label = label
        self.kind_of = {}             # toolCallId -> kind
        self.last_status = {}         # toolCallId -> most recent status
        self.started = Counter()      # kind -> tool calls started
        self.terminal = Counter()     # kind -> tool calls that ended
        self.permissions = Counter()  # kind -> request_permission count
        self.permission_no_kind = 0
        self.stop_reason = None
        self.agent_exit_code = None

    def feed(self, frame):
        if frame.get("dir") != "in":
            return
        msg = frame.get("frame") or {}
        method = msg.get("method")
        params = msg.get("params") or {}

        if method == "session/request_permission":
            tc = params.get("toolCall") or {}
            kind = tc.get("kind")
            tid = tc.get("toolCallId")
            if tid and kind:
                self.kind_of.setdefault(tid, kind)
            if kind:
                self.permissions[kind] += 1
            else:
                # A request with no kind is itself a finding: the Daemon cannot
                # then tell which class it is being asked about.
                self.permission_no_kind += 1
                self.permissions[self.kind_of.get(tid, "<no kind>")] += 1
            return

        if method != "session/update":
            return

        update = params.get("update") or {}
        su = update.get("sessionUpdate")
        tid = update.get("toolCallId")
        if not tid:
            return

        if su == "tool_call":
            kind = update.get("kind") or "<no kind>"
            self.kind_of[tid] = kind
            self.started[kind] += 1

        status = update.get("status")
        if status:
            self.last_status[tid] = status

    def finish(self):
        for tid, status in self.last_status.items():
            if status in TERMINAL:
                self.terminal[self.kind_of.get(tid, "<no kind>")] += 1

    @property
    def quiet(self):
        """Tool calls that started and never reached a terminal status."""
        out = {}
        for kind, n in self.started.items():
            missing = n - self.terminal.get(kind, 0)
            if missing:
                out[kind] = missing
        return out


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--capture-dir", required=True,
                   help="directory holding <label>-frames.jsonl from acp-capture.py")
    p.add_argument("--out", required=True, help="where to write gates.json")
    p.add_argument("--gate1", choices=["auto", "pass", "fail", "unknown"], default="auto",
                   help="did a tool call complete on the Host over SSH; auto derives it "
                        "from the frames, anything else overrides")
    p.add_argument("--gate1-note", default="", help="one line of evidence for gate 1")
    args = p.parse_args()

    runs = []
    for path in sorted(glob.glob(os.path.join(args.capture_dir, "*-frames.jsonl"))):
        label = os.path.basename(path)[: -len("-frames.jsonl")]
        run = Run(label)
        for frame in _load_frames(path):
            run.feed(frame)
        run.finish()

        summary_path = os.path.join(args.capture_dir, label + "-summary.json")
        if os.path.exists(summary_path):
            with open(summary_path, encoding="utf-8") as fh:
                s = json.load(fh)
            run.stop_reason = s.get("stop_reason")
            run.agent_exit_code = s.get("agent_exit_code")
        runs.append(run)

    if not runs:
        print("no *-frames.jsonl found, so nothing to count", file=sys.stderr)
        return 2

    # Roll the per-run counts up by tool class, because the gates are stated per
    # class and a class can appear in more than one run.
    started = Counter()
    terminal = Counter()
    permissions = Counter()
    for run in runs:
        started.update(run.started)
        terminal.update(run.terminal)
        permissions.update(run.permissions)

    unseen = sorted(k for k in GATE_CLASSES if started.get(k, 0) == 0)
    asked = sorted(k for k in GATE_CLASSES if permissions.get(k, 0) > 0)
    quiet = dict((k, started[k] - terminal.get(k, 0))
                 for k in started if started[k] - terminal.get(k, 0) > 0)

    # Gate 2 fails only for a class that ran and never asked. A class that never
    # ran is unknown, and unknown is not a pass.
    gate2_fail = sorted(k for k in GATE_CLASSES
                        if started.get(k, 0) > 0 and permissions.get(k, 0) == 0)
    if gate2_fail:
        gate2 = "fail"
    elif unseen:
        gate2 = "unknown"
    else:
        gate2 = "pass"

    # Gate 3 asks for knowledge, not perfection. A quiet class is recorded and
    # survivable; an unexercised class is not knowledge at all.
    gate3 = "unknown" if unseen else "pass"

    # Gate 1 is the bar Hermes failed: the Harness has to actually run out
    # there. A turn that ended AND a tool call that reached a terminal status
    # are two different claims, so require both.
    gate1 = args.gate1
    if gate1 == "auto":
        turn_ended = any(r.stop_reason for r in runs)
        tool_ended = sum(terminal.values()) > 0
        gate1 = "pass" if (turn_ended and tool_ended) else "fail"

    report = {
        "capture_dir": os.path.abspath(args.capture_dir),
        "runs": [
            {
                "label": r.label,
                "stop_reason": r.stop_reason,
                "agent_exit_code": r.agent_exit_code,
                "tool_calls_started": dict(r.started),
                "tool_calls_terminal": dict(r.terminal),
                "permission_requests": dict(r.permissions),
                "permission_requests_without_kind": r.permission_no_kind,
                "tool_calls_never_terminal": r.quiet,
            }
            for r in runs
        ],
        "totals": {
            "tool_calls_started": dict(started),
            "tool_calls_terminal": dict(terminal),
            "permission_requests": dict(permissions),
        },
        "gates": {
            "gate1_tool_call_on_host": {
                "verdict": gate1,
                "note": args.gate1_note,
            },
            "gate2_permission_per_class": {
                "verdict": gate2,
                "asked": asked,
                "ran_but_never_asked": gate2_fail,
                "never_exercised": unseen,
            },
            "gate3_terminal_per_class": {
                "verdict": gate3,
                "quiet_classes": quiet,
                "never_exercised": unseen,
            },
        },
    }

    with open(args.out, "w", encoding="utf-8", newline="\n") as fh:
        json.dump(report, fh, indent=2, ensure_ascii=False)
        fh.write("\n")

    print()
    print("  tool class    started  ended  asked permission")
    for kind in sorted(set(list(started) + list(permissions))):
        print("  %-12s %7d  %5d  %16d" % (kind, started.get(kind, 0),
                                          terminal.get(kind, 0),
                                          permissions.get(kind, 0)))
    print()
    print("  gate 1  tool call on the Host over SSH   %s" % gate1.upper())
    if args.gate1_note:
        print("          %s" % args.gate1_note)
    print("  gate 2  permission per tool class        %s" % gate2.upper())
    if gate2_fail:
        print("          ran but never asked: %s" % ", ".join(gate2_fail))
        print('          those classes get "deny" in the permission block (ADR 0003)')
        if "read" in gate2_fail:
            # A class with no key is not a class that forgot to ask, and the two
            # have different fixes. OpenCode's own examples only ever set edit,
            # bash and webfetch, so read may not be gateable at all -- and
            # denying reads outright is not a usable Session.
            print("          read has no key in OpenCode's permission block, so this may be")
            print("          'cannot ask' rather than 'did not ask'. Confirm which before")
            print("          applying the deny recovery: denying reads ends the Session's use.")
    if unseen:
        print("          never exercised: %s" % ", ".join(unseen))
    print("  gate 3  terminal Event per tool class    %s" % gate3.upper())
    if quiet:
        for kind, n in sorted(quiet.items()):
            print("          %s: %d call(s) started and never ended, so the adapter "
                  "synthesises" % (kind, n))
    elif not unseen:
        print("          every started tool call reached a terminal status")
    print()
    print("  written: %s" % args.out)

    # Only gate 1 and gate 2 can fail the run. Gate 3 reports knowledge.
    if gate1 == "fail" or gate2 == "fail":
        return 1
    if "unknown" in (gate1, gate2, gate3):
        return 3
    return 0


if __name__ == "__main__":
    sys.exit(main())
