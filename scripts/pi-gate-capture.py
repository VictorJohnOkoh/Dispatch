#!/usr/bin/env python3
"""Drive Pi with the Dispatch Gate extension and settle two questions.

Question one is the one issue #56 owes: **does the Gate announce itself before
``Start`` returns?** The Daemon's cheapest definition of "Start returned" is one
command round-trip, so this script sends ``get_state`` as the very first frame on
stdin and records whether the extension's ``ready`` notification arrived before
that command's ``response``. Run it with ``--no-ext`` for the control: the same
probe with no extension loaded, which is what a failed load looks like.

Question two is coverage: every tool call the Gate sees is classified into one of
the five ToolKinds and held, rather than the three bash regexes Pi's own
``permission-gate.ts`` matches.

Artefacts follow ``pi-rpc-capture.py``'s conventions:

    <label>-raw.log       every line, prefixed >>> (to Pi) or <<< (from Pi)
    <label>-frames.jsonl  {ts, seq, dir, frame} per line
    <label>-stderr.log    Pi's stderr verbatim
    <label>-summary.json  the verdict, the gate decisions, the event histogram

Separate from ``pi-rpc-capture.py`` because that script and its outputs are
frozen research artefacts and this is a different experiment: a start probe, a
machine-readable gate protocol, and a decision per ToolKind.
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import threading
import time
from collections import Counter
from datetime import datetime, timezone
from typing import Any

PROTOCOL = "dispatch.gate/1"
KINDS = ("read", "edit", "execute", "fetch", "other")
PROBE_ID = "start-probe"


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()


def _payload(text: Any) -> dict[str, Any] | None:
    """Parse a Gate frame out of a Pi display string, or None if it is not one."""
    if not isinstance(text, str):
        return None
    try:
        frame = json.loads(text)
    except (json.JSONDecodeError, TypeError):
        return None
    if isinstance(frame, dict) and frame.get("protocol") == PROTOCOL:
        return frame
    return None


class PiGateCapture:
    """One Pi RPC session with the Dispatch Gate, recorded."""

    def __init__(self, args: argparse.Namespace) -> None:
        self.args = args
        self.outdir = args.outdir
        self.label = args.label

        self.raw_path = os.path.join(self.outdir, f"{self.label}-raw.log")
        self.frames_path = os.path.join(self.outdir, f"{self.label}-frames.jsonl")
        self.stderr_path = os.path.join(self.outdir, f"{self.label}-stderr.log")
        self.summary_path = os.path.join(self.outdir, f"{self.label}-summary.json")

        self._raw = open(self.raw_path, "w", encoding="utf-8", newline="\n")
        self._frames = open(self.frames_path, "w", encoding="utf-8", newline="\n")
        self._file_lock = threading.Lock()
        self._send_lock = threading.Lock()
        self._seq = 0

        self.event_types: Counter[str] = Counter()
        self.decisions: list[dict[str, Any]] = []
        self.tool_starts: list[dict[str, Any]] = []
        self.extension_errors: list[dict[str, Any]] = []
        self.ready_seq: int | None = None
        self.probe_seq: int | None = None
        self.probe_response: dict[str, Any] | None = None

        self._gated_ids: set[str] = set()
        self.probe_answered = threading.Event()
        self.settled = threading.Event()
        self._dead = threading.Event()
        self.proc: subprocess.Popen | None = None
        self._t0 = time.monotonic()

    # -- plumbing ---------------------------------------------------------

    def _record(self, direction: str, text: str, frame: Any) -> int:
        with self._file_lock:
            self._seq += 1
            seq = self._seq
            arrow = ">>>" if direction == "out" else "<<<"
            self._raw.write(f"{arrow} {text}\n")
            self._raw.flush()
            json.dump({"ts": _now(), "seq": seq, "dir": direction, "frame": frame}, self._frames)
            self._frames.write("\n")
            self._frames.flush()
        return seq

    def send(self, frame: dict[str, Any]) -> None:
        text = json.dumps(frame, ensure_ascii=False, separators=(",", ":"))
        with self._send_lock:
            assert self.proc is not None and self.proc.stdin is not None
            try:
                self.proc.stdin.write(text + "\n")
                self.proc.stdin.flush()
            except (BrokenPipeError, OSError) as exc:
                print(f"  ! pi stdin closed ({exc}) - it probably died", file=sys.stderr)
                self._dead.set()
                return
        self._record("out", text, frame)

    # -- pi to client -----------------------------------------------------

    def _decide(self, kind: str) -> str:
        return "deny" if kind in self.args.deny_kind else "allow"

    def _handle_ui_request(self, msg: dict[str, Any], seq: int) -> None:
        method = msg.get("method", "")
        rid = msg.get("id")

        if method == "notify":
            frame = _payload(msg.get("message"))
            if frame and frame.get("event") == "ready" and self.ready_seq is None:
                self.ready_seq = seq
                print(f"  . gate ready (seq {seq}, kinds {frame.get('kinds')})")
            return

        if method != "select":
            print(f"  . extension_ui_request/{method} - not a Gate frame, ignoring")
            return

        frame = _payload(msg.get("title"))
        if frame is None:
            print("  ! select with no Gate payload - cancelling")
            self.send({"type": "extension_ui_response", "id": rid, "cancelled": True})
            return

        kind = frame.get("kind", "other")
        decision = self._decide(kind)
        self._gated_ids.add(frame.get("toolCallId", ""))
        self.decisions.append({"seq": seq, "kind": kind, "toolName": frame.get("toolName"),
                               "toolCallId": frame.get("toolCallId"), "decision": decision})
        print(f"  . gate {frame.get('toolName')} ({kind}) -> {decision}")
        self.send({"type": "extension_ui_response", "id": rid, "value": decision})

    def _read_stdout(self) -> None:
        assert self.proc is not None and self.proc.stdout is not None
        for line in self.proc.stdout:
            text = line.rstrip("\r\n")
            if not text.strip():
                continue
            try:
                msg = json.loads(text)
            except json.JSONDecodeError:
                self._record("in", text, {"_unparseable": True, "text": text})
                continue

            seq = self._record("in", text, msg)
            if not isinstance(msg, dict):
                continue

            mtype = msg.get("type", "<none>")
            self.event_types[mtype] += 1

            if mtype == "extension_ui_request":
                self._handle_ui_request(msg, seq)
            elif mtype == "extension_error":
                self.extension_errors.append(msg)
                print(f"  ! extension_error: {msg.get('error')}")
            elif mtype == "response" and msg.get("id") == PROBE_ID:
                self.probe_seq = seq
                self.probe_response = msg
                self.probe_answered.set()
                print(f"  . start probe answered (seq {seq})")
            elif mtype == "tool_execution_start":
                self.tool_starts.append(msg)
                print(f"  . tool_execution_start: {msg.get('toolName')}")
            elif mtype == "agent_settled":
                self.settled.set()

        self._dead.set()

    def _read_stderr(self) -> None:
        assert self.proc is not None and self.proc.stderr is not None
        with open(self.stderr_path, "w", encoding="utf-8", newline="\n") as fh:
            for line in self.proc.stderr:
                fh.write(line)
                fh.flush()

    # -- the conversation -------------------------------------------------

    def _extensions(self) -> list[str]:
        return [] if self.args.no_ext else list(self.args.ext or [])

    def _spawn(self) -> int:
        # On Windows npm installs Pi as pi.cmd, and CreateProcess cannot execute a
        # batch file directly - it must go through cmd.exe.
        argv0 = self.args.pi_cmd.split()
        resolved = shutil.which(argv0[0])
        if resolved is None:
            print(f"  ! cannot find {argv0[0]} on PATH", file=sys.stderr)
            return 127
        launcher: list[str] = []
        if os.name == "nt" and resolved.lower().endswith((".cmd", ".bat")):
            launcher = [os.environ.get("COMSPEC", "cmd.exe"), "/c"]
        cmd = launcher + [resolved] + argv0[1:] + [
            "--provider", self.args.provider,
            "--model", self.args.model,
            "--session-dir", self.args.session_dir,
            "--mode", "rpc",
        ]
        for ext in self._extensions():
            cmd += ["-e", ext]

        print(f"  spawning: {cmd}")
        env = dict(os.environ)
        for pair in self.args.env or []:
            k, _, v = pair.partition("=")
            env[k] = v

        self.proc = subprocess.Popen(
            cmd, cwd=self.args.cwd, env=env,
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            text=True, encoding="utf-8", errors="replace", bufsize=1,
        )
        threading.Thread(target=self._read_stdout, daemon=True).start()
        threading.Thread(target=self._read_stderr, daemon=True).start()
        return 0

    def _wait(self, flag: threading.Event, timeout: float, what: str) -> bool:
        deadline = time.monotonic() + timeout
        while not flag.is_set():
            if self._dead.is_set():
                print(f"  ! pi exited before {what}")
                return False
            if time.monotonic() > deadline:
                print(f"  ! no {what} within {timeout}s")
                return False
            time.sleep(0.05)
        return True

    def run(self) -> int:
        rc = self._spawn()
        if rc:
            return rc

        # The start probe goes first, before anything else can perturb the order.
        self.send({"id": PROBE_ID, "type": "get_state"})
        self._wait(self.probe_answered, self.args.probe_timeout, "start probe response")

        for i, prompt in enumerate(self.args.prompt or [], start=1):
            self.settled.clear()
            print(f"  prompt {i}: {prompt}")
            self.send({"id": f"req-{i}", "type": "prompt", "message": prompt})
            if not self._wait(self.settled, self.args.timeout, "agent_settled"):
                break

        rc = self._shutdown()
        self._write_summary(rc)
        self._report()
        return rc

    def _shutdown(self) -> int:
        assert self.proc is not None
        try:
            if self.proc.stdin and not self.proc.stdin.closed:
                self.proc.stdin.close()
        except OSError:
            pass
        try:
            self.proc.wait(timeout=15)
        except subprocess.TimeoutExpired:
            print("  . pi did not exit - terminating")
            self.proc.terminate()
            try:
                self.proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.proc.kill()
        with self._file_lock:
            self._raw.close()
            self._frames.close()
        return self.proc.returncode if self.proc.returncode is not None else -1

    def _verdict(self) -> dict[str, Any]:
        """Did the announcement arrive before Start returned?"""
        if self.ready_seq is None:
            answer = "no announcement arrived at all"
        elif self.probe_seq is None:
            answer = "announcement arrived, but the start probe was never answered"
        elif self.ready_seq < self.probe_seq:
            answer = "yes - the announcement preceded the start probe response"
        else:
            answer = "no - the announcement arrived after the start probe response"
        return {
            "question": "does the Gate announce itself before Start returns?",
            "start_is": f"the response to the first command ({PROBE_ID}, get_state)",
            "ready_seq": self.ready_seq,
            "probe_response_seq": self.probe_seq,
            "answer": answer,
        }

    def _ungated(self) -> list[dict[str, Any]]:
        """Tool calls that started and were never held. Counted at the end because
        tool_execution_start fires before the Gate resolves, so a call is only
        ungated once the run is over without a request for it."""
        return [t for t in self.tool_starts if t.get("toolCallId") not in self._gated_ids]

    def _write_summary(self, rc: int) -> None:
        kinds_seen = Counter(d["kind"] for d in self.decisions)
        summary = {
            "label": self.label,
            "captured_at": _now(),
            "pi_cmd": self.args.pi_cmd,
            "extensions": self._extensions(),
            "provider": self.args.provider,
            "model": self.args.model,
            "cwd": self.args.cwd,
            "prompts": self.args.prompt or [],
            "deny_kinds": sorted(self.args.deny_kind),
            "exit_code": rc,
            "elapsed_seconds": round(time.monotonic() - self._t0, 2),
            "announcement": self._verdict(),
            "gate_decisions": self.decisions,
            "kinds_gated": {k: kinds_seen.get(k, 0) for k in KINDS},
            "tool_starts": len(self.tool_starts),
            "ungated_tool_calls": self._ungated(),
            "extension_errors": self.extension_errors,
            "probe_response": self.probe_response,
            "event_types": dict(self.event_types),
        }
        with open(self.summary_path, "w", encoding="utf-8", newline="\n") as fh:
            json.dump(summary, fh, indent=2, ensure_ascii=False)
            fh.write("\n")

    def _report(self) -> None:
        print(f"  frames:  {self.frames_path}")
        print(f"  summary: {self.summary_path}")
        print(f"  verdict: {self._verdict()['answer']}")
        print(f"  gated:   {len(self.decisions)}   ungated: {len(self._ungated())}")


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__,
                                formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--pi-cmd", default="pi", help="how to invoke Pi (default: pi)")
    p.add_argument("--cwd", required=True, help="working directory for the session")
    p.add_argument("--outdir", required=True, help="directory to write artefacts into")
    p.add_argument("--label", required=True, help="filename prefix")
    p.add_argument("--session-dir", required=True, help="Pi --session-dir")
    p.add_argument("--provider", default="lmstudio")
    p.add_argument("--model", required=True)
    p.add_argument("--ext", action="append", metavar="PATH",
                   help="extension to load, dispatch-gate.ts first (repeatable)")
    p.add_argument("--no-ext", action="store_true",
                   help="control run: load no extension, which is what a failed load looks like")
    p.add_argument("--prompt", action="append", metavar="TEXT",
                   help="prompt to send once the previous one settles (repeatable)")
    p.add_argument("--deny-kind", action="append", default=[], choices=list(KINDS),
                   help="answer deny for this ToolKind (repeatable)")
    p.add_argument("--env", action="append", metavar="KEY=VALUE",
                   help="environment variable for the Pi process (repeatable)")
    p.add_argument("--probe-timeout", type=float, default=60.0,
                   help="seconds to wait for the start probe response (default 60)")
    p.add_argument("--timeout", type=float, default=300.0,
                   help="seconds to wait for each agent_settled (default 300)")
    args = p.parse_args()

    if not args.ext and not args.no_ext:
        p.error("one of --ext or --no-ext is required")

    os.makedirs(args.outdir, exist_ok=True)
    os.makedirs(args.cwd, exist_ok=True)
    os.makedirs(args.session_dir, exist_ok=True)

    return PiGateCapture(args).run()


if __name__ == "__main__":
    sys.exit(main())
