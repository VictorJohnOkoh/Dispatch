#!/usr/bin/env python3
"""Drive Pi in ``--mode rpc`` and record every frame on the wire.

Pi's RPC mode is bidirectional: an extension can call ``ctx.ui.confirm`` /
``ctx.ui.select``, which surfaces as an ``extension_ui_request`` on stdout and
blocks until the client sends a matching ``extension_ui_response`` on stdin.
``cat cmds.jsonl | pi --mode rpc`` cannot answer those — and worse, closing
stdin makes Pi exit mid-turn (5 events instead of 54). That is why this is a
program rather than a stage in the shell wizard.

The counterpart to ``acp-capture.py``. Same artefacts, same conventions:

    <label>-raw.log       every line, prefixed >>> (to Pi) or <<< (from Pi)
    <label>-frames.jsonl  {ts, dir, frame} per line
    <label>-stderr.log    Pi's stderr verbatim
    <label>-summary.json  event histogram, UI requests, timings

Stdin stays open until ``agent_settled`` arrives, then closes — which is what a
real supervisor does, rather than guessing a sleep duration.
"""

from __future__ import annotations

import argparse
import json
import os
import queue
import shutil
import subprocess
import sys
import threading
import time
from collections import Counter
from datetime import datetime, timezone
from typing import Any


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()


class PiRpcCapture:
    """One Pi RPC session, recorded."""

    def __init__(self, args: argparse.Namespace) -> None:
        self.args = args
        self.label = args.label
        self.outdir = args.outdir

        self.raw_path = os.path.join(self.outdir, f"{self.label}-raw.log")
        self.frames_path = os.path.join(self.outdir, f"{self.label}-frames.jsonl")
        self.stderr_path = os.path.join(self.outdir, f"{self.label}-stderr.log")
        self.summary_path = os.path.join(self.outdir, f"{self.label}-summary.json")

        self._raw = open(self.raw_path, "w", encoding="utf-8", newline="\n")
        self._frames = open(self.frames_path, "w", encoding="utf-8", newline="\n")
        self._file_lock = threading.Lock()
        self._send_lock = threading.Lock()

        self.event_types: Counter[str] = Counter()
        self.ui_requests: list[dict[str, Any]] = []
        self.tool_calls: list[dict[str, Any]] = []
        self.responses: list[dict[str, Any]] = []
        self.settled = threading.Event()

        self.proc: subprocess.Popen | None = None
        self._dead = threading.Event()
        self._t0 = time.monotonic()

    # ── plumbing ──────────────────────────────────────────────────────────

    def _record(self, direction: str, text: str, frame: Any) -> None:
        with self._file_lock:
            arrow = ">>>" if direction == "out" else "<<<"
            self._raw.write(f"{arrow} {text}\n")
            self._raw.flush()
            json.dump({"ts": _now(), "dir": direction, "frame": frame}, self._frames)
            self._frames.write("\n")
            self._frames.flush()

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

    # ── pi → client ───────────────────────────────────────────────────────

    def _handle_ui_request(self, msg: dict[str, Any]) -> None:
        """Answer an extension dialog.

        The whole point of the capture: this is Pi's approval payload, the
        counterpart to Hermes' session/request_permission.
        """
        method = msg.get("method", "")
        rid = msg.get("id")
        self.ui_requests.append(msg)

        # Fire-and-forget methods expect no reply at all. Answering them would
        # desynchronise the id space.
        if method in {"notify", "setStatus", "setWidget", "setTitle", "set_editor_text"}:
            print(f"  . extension_ui_request/{method} (fire-and-forget, not answering)")
            return

        decision = self.args.decision
        if method == "confirm":
            reply = {"type": "extension_ui_response", "id": rid,
                     "confirmed": decision == "allow"}
        elif method == "select":
            options = msg.get("options") or []
            # The gate's options are ["Yes", "No"]; pick by position rather than
            # by label so this works for any two-option gate.
            idx = 0 if decision == "allow" else (1 if len(options) > 1 else 0)
            reply = {"type": "extension_ui_response", "id": rid,
                     "value": options[idx] if options else None}
        elif method == "input":
            reply = {"type": "extension_ui_response", "id": rid, "value": self.args.input_text}
        else:
            print(f"  ! unhandled ui method: {method} - cancelling")
            reply = {"type": "extension_ui_response", "id": rid, "cancelled": True}

        print(f"  . extension_ui_request/{method} -> {decision}")
        self.send(reply)

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

            self._record("in", text, msg)
            if not isinstance(msg, dict):
                continue

            mtype = msg.get("type", "<none>")
            self.event_types[mtype] += 1

            if mtype == "extension_ui_request":
                self._handle_ui_request(msg)
            elif mtype == "response":
                self.responses.append(msg)
                if not msg.get("success", True):
                    print(f"  ! command rejected: {msg.get('error')}")
            elif mtype == "tool_execution_start":
                self.tool_calls.append(msg)
                print(f"  . tool_execution_start: {msg.get('toolName')} {json.dumps(msg.get('args'))[:80]}")
            elif mtype == "agent_settled":
                print("  . agent_settled")
                self.settled.set()

        self._dead.set()

    def _read_stderr(self) -> None:
        assert self.proc is not None and self.proc.stderr is not None
        with open(self.stderr_path, "w", encoding="utf-8", newline="\n") as fh:
            for line in self.proc.stderr:
                fh.write(line)
                fh.flush()

    # ── the conversation ──────────────────────────────────────────────────

    def run(self) -> int:
        # On Windows npm installs Pi as pi.cmd, and CreateProcess cannot execute
        # a batch file directly — it must go through cmd.exe. Resolving with
        # which() also keeps the arguments as a list, so paths with spaces
        # survive without shell=True and its quoting hazards.
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
        for ext in self.args.ext or []:
            cmd += ["-e", ext]

        print(f"  spawning: {cmd}")
        print(f"  cwd:      {self.args.cwd}")
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

        self.send({"id": "req-1", "type": "prompt", "message": self.args.prompt})
        print(f"  prompt sent -> waiting up to {self.args.timeout}s for agent_settled")

        deadline = time.monotonic() + self.args.timeout
        while not self.settled.is_set():
            if self._dead.is_set():
                print("  ! pi exited before settling")
                break
            if time.monotonic() > deadline:
                print(f"  ! no agent_settled within {self.args.timeout}s")
                break
            time.sleep(0.2)

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

    def _write_summary(self, rc: int) -> None:
        summary = {
            "label": self.label,
            "captured_at": _now(),
            "pi_cmd": self.args.pi_cmd,
            "extensions": self.args.ext or [],
            "provider": self.args.provider,
            "model": self.args.model,
            "cwd": self.args.cwd,
            "prompt": self.args.prompt,
            "decision_policy": self.args.decision,
            "exit_code": rc,
            "settled": self.settled.is_set(),
            "elapsed_seconds": round(time.monotonic() - self._t0, 2),
            "event_types": dict(self.event_types),
            "command_responses": self.responses,
            "ui_request_count": len(self.ui_requests),
            "ui_requests": self.ui_requests,
            "tool_calls": self.tool_calls,
        }
        with open(self.summary_path, "w", encoding="utf-8", newline="\n") as fh:
            json.dump(summary, fh, indent=2, ensure_ascii=False)
            fh.write("\n")

    def _report(self) -> None:
        print(f"  frames:  {self.frames_path}")
        print(f"  summary: {self.summary_path}")
        print("  event types:")
        for k, v in self.event_types.most_common():
            print(f"    {v:5d}  {k}")


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
    p.add_argument("--prompt", required=True)
    p.add_argument("-e", "--ext", action="append", metavar="PATH",
                   help="extension to load (repeatable)")
    p.add_argument("--env", action="append", metavar="KEY=VALUE",
                   help="environment variable for the Pi process (repeatable)")
    p.add_argument("--decision", choices=["allow", "deny"], default="allow",
                   help="how to answer extension dialogs (default allow)")
    p.add_argument("--input-text", default="capture",
                   help="value returned for ui.input dialogs")
    p.add_argument("--timeout", type=float, default=300.0,
                   help="seconds to wait for agent_settled (default 300)")
    args = p.parse_args()

    os.makedirs(args.outdir, exist_ok=True)
    os.makedirs(args.cwd, exist_ok=True)
    os.makedirs(args.session_dir, exist_ok=True)

    return PiRpcCapture(args).run()


if __name__ == "__main__":
    sys.exit(main())
