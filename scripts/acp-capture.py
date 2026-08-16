#!/usr/bin/env python3
"""Drive an ACP agent (``hermes acp``) and record every frame on the wire.

The Agent Client Protocol is JSON-RPC 2.0 over **newline-delimited JSON** on the
agent's stdin/stdout — no ``Content-Length`` framing. Confirmed against
``agent_client_protocol`` 0.9.0 (``acp/connection.py`` uses ``readline()`` +
``json.loads``).

This is a *client*, not a pipe. ACP is bidirectional: the agent calls back into
the client for permission decisions and file reads. ``cat cmds.jsonl | hermes
acp`` cannot answer those and will hang or fail — which is exactly why this
exists as a program rather than a stage in the shell wizard.

Deliberately dependency-free stdlib, and it hand-rolls the protocol rather than
importing ``acp``, so what lands in the capture is the raw wire text as sent and
received — not a library's re-serialisation of it. The capture is the artefact;
everything else here is scaffolding.

Outputs, into ``--outdir``:

    <label>-raw.log        every frame, ``>>>`` sent / ``<<<`` received
    <label>-frames.jsonl   {ts, dir, frame} — the same traffic, parsed
    <label>-stderr.log     the agent's stderr
    <label>-summary.json   exit code, stopReason, usage, event-kind tallies
"""

from __future__ import annotations

import argparse
import json
import os
import queue
import shlex
import subprocess
import sys
import threading
import time
from collections import Counter
from datetime import datetime, timezone
from typing import Any

PROTOCOL_VERSION = 1  # acp.meta.PROTOCOL_VERSION


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()


class AcpCapture:
    """One ACP conversation, recorded.

    Owns the agent subprocess, a reader thread per output stream, and the
    JSON-RPC id bookkeeping. Agent-to-client requests are answered from the
    reader thread; ``_send_lock`` is what makes that safe alongside the main
    thread issuing its own requests.
    """

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

        self._next_id = 0
        self._pending: dict[int, queue.Queue] = {}
        self._pending_lock = threading.Lock()

        # Tallies for the summary. The sessionUpdate histogram is the number
        # the Event model actually gets designed against.
        self.update_kinds: Counter[str] = Counter()
        self.agent_methods: Counter[str] = Counter()
        self.permission_requests: list[dict[str, Any]] = []
        self.stop_reason: str | None = None
        self.usage: Any = None
        self.session_id: str | None = None
        self.init_result: Any = None
        self.new_session_result: Any = None

        self.proc: subprocess.Popen | None = None
        self._dead = threading.Event()
        self._stdin_closed = False

    # ── plumbing ──────────────────────────────────────────────────────────

    def _record(self, direction: str, text: str, frame: Any) -> None:
        with self._file_lock:
            arrow = ">>>" if direction == "out" else "<<<"
            self._raw.write(f"{arrow} {text}\n")
            self._raw.flush()
            json.dump({"ts": _now(), "dir": direction, "frame": frame}, self._frames)
            self._frames.write("\n")
            self._frames.flush()

    def _write_frame(self, frame: dict[str, Any]) -> None:
        text = json.dumps(frame, ensure_ascii=False, separators=(",", ":"))
        if self._stdin_closed:
            # Deliberate: --close-stdin-after-prompt trades the ability to
            # answer callbacks for a terminal tool that does not deadlock.
            print(f"  ! cannot send {frame.get('method') or 'response'} - stdin already closed")
            self._record("out", "<<dropped: stdin closed>> " + text, {"_dropped": True, "frame": frame})
            return
        with self._send_lock:
            assert self.proc is not None and self.proc.stdin is not None
            try:
                self.proc.stdin.write(text + "\n")
                self.proc.stdin.flush()
            except (BrokenPipeError, OSError) as exc:
                print(f"  ! agent stdin closed ({exc}) - it probably died", file=sys.stderr)
                self._dead.set()
                return
        self._record("out", text, frame)

    def send_request(self, method: str, params: Any) -> int:
        """Write a request and return its id, without waiting."""
        with self._pending_lock:
            self._next_id += 1
            rid = self._next_id
            self._pending[rid] = queue.Queue(maxsize=1)
        self._write_frame({"jsonrpc": "2.0", "id": rid, "method": method, "params": params})
        return rid

    def close_stdin(self) -> None:
        """Close the agent's stdin while continuing to read its stdout.

        Hermes v0.19.0 on Windows deadlocks its terminal tool until this
        happens — the sandbox shell it spawns appears to inherit the ACP stdin
        pipe and block reading it. Measured: tool duration tracked the client's
        timeout exactly (272.7s/280, 418.8s/420, 898.6s/900, 118.8s/120), which
        is the client's teardown unblocking it rather than slow setup.

        The cost is that no further client-to-agent traffic is possible, so
        ``session/request_permission`` and ``fs/*`` callbacks can no longer be
        answered. Only use this for prompts that do not need them.
        """
        assert self.proc is not None
        if self.proc.stdin and not self.proc.stdin.closed:
            with self._send_lock:
                self.proc.stdin.close()
            self._stdin_closed = True
            print("  . stdin closed (terminal-tool deadlock workaround)")

    def await_response(self, rid: int, method: str, timeout: float) -> Any:
        with self._pending_lock:
            box = self._pending[rid]
        deadline = time.monotonic() + timeout
        while True:
            if self._dead.is_set():
                raise RuntimeError(f"{method}: agent exited before responding")
            try:
                msg = box.get(timeout=0.5)
                break
            except queue.Empty:
                if time.monotonic() > deadline:
                    raise RuntimeError(f"{method}: no response within {timeout}s")

        if "error" in msg:
            raise RuntimeError(f"{method} failed: {json.dumps(msg['error'], ensure_ascii=False)}")
        return msg.get("result")

    def request(self, method: str, params: Any, timeout: float) -> Any:
        """Send a request and block for its response."""
        return self.await_response(self.send_request(method, params), method, timeout)

    def _respond(self, rid: Any, result: Any) -> None:
        self._write_frame({"jsonrpc": "2.0", "id": rid, "result": result})

    def _respond_error(self, rid: Any, code: int, message: str) -> None:
        self._write_frame({"jsonrpc": "2.0", "id": rid, "error": {"code": code, "message": message}})

    # ── agent → client ────────────────────────────────────────────────────

    def _handle_agent_request(self, msg: dict[str, Any]) -> None:
        """Answer a call the agent made into us.

        Every branch here is a capture target in its own right: the shape of
        ``session/request_permission`` is the approval payload, and whether
        ``fs/*`` or ``terminal/*`` are used at all tells us how much of the
        tool surface the agent delegates to its client rather than performing
        itself.
        """
        rid = msg.get("id")
        method = msg.get("method", "")
        params = msg.get("params") or {}
        self.agent_methods[method] += 1

        if method == "session/request_permission":
            self.permission_requests.append(params)
            options = params.get("options") or []
            choice = None
            if self.args.permission == "allow":
                # Prefer a one-shot allow over a blanket one so the capture
                # shows the gate firing per call rather than being disabled.
                for want in ("allow_once", "allow_always"):
                    for opt in options:
                        if opt.get("kind") == want:
                            choice = opt
                            break
                    if choice:
                        break
                if choice is None and options:
                    choice = options[0]

            if choice is not None:
                print(f"  . permission requested -> allowing ({choice.get('name')})")
                self._respond(rid, {"outcome": {"outcome": "selected", "optionId": choice["optionId"]}})
            else:
                print("  . permission requested -> denying")
                self._respond(rid, {"outcome": {"outcome": "cancelled"}})

            if self.args.close_stdin_on_permission and not self._stdin_closed:
                # The sandbox that deadlocks is only spawned once approval is
                # granted, so this is the last moment at which closing stdin
                # both answers the callback and pre-empts the hang.
                print("  . permission answered - closing stdin to release the sandbox")
                self.close_stdin()
            return

        if method == "fs/read_text_file":
            path = params.get("path")
            try:
                with open(path, encoding="utf-8") as fh:
                    content = fh.read()
                line = params.get("line")
                limit = params.get("limit")
                if line is not None or limit is not None:
                    lines = content.splitlines(keepends=True)
                    start = max(0, (line or 1) - 1)
                    content = "".join(lines[start:start + limit] if limit else lines[start:])
                self._respond(rid, {"content": content})
            except OSError as exc:
                self._respond_error(rid, -32603, f"read failed: {exc}")
            return

        if method == "fs/write_text_file":
            try:
                path = params["path"]
                os.makedirs(os.path.dirname(path) or ".", exist_ok=True)
                with open(path, "w", encoding="utf-8") as fh:
                    fh.write(params.get("content", ""))
                self._respond(rid, {})
            except OSError as exc:
                self._respond_error(rid, -32603, f"write failed: {exc}")
            return

        # Anything else is a genuine finding — record it and decline honestly
        # rather than inventing a response.
        print(f"  ! unhandled agent request: {method}")
        self._respond_error(rid, -32601, f"method not supported by acp-capture: {method}")

    def _handle_notification(self, msg: dict[str, Any]) -> None:
        method = msg.get("method", "")
        self.agent_methods[method] += 1
        if method == "session/update":
            update = (msg.get("params") or {}).get("update") or {}
            kind = update.get("sessionUpdate", "<none>")
            self.update_kinds[kind] += 1
            if kind == "tool_call" and self.args.close_stdin_on_tool_call and not self._stdin_closed:
                # The deadlock only bites once a tool actually starts, so hold
                # stdin open until then: everything up to and including the
                # tool_call payload is captured over a live connection, and
                # only the tail is traded away.
                print("  . tool_call seen - closing stdin to release the terminal tool")
                self.close_stdin()

    # ── reader threads ────────────────────────────────────────────────────

    def _read_stdout(self) -> None:
        assert self.proc is not None and self.proc.stdout is not None
        for line in self.proc.stdout:
            text = line.rstrip("\r\n")
            if not text.strip():
                continue
            try:
                msg = json.loads(text)
            except json.JSONDecodeError:
                # Not JSON on an ndjson channel: a real protocol violation,
                # worth keeping verbatim.
                self._record("in", text, {"_unparseable": True, "text": text})
                continue

            self._record("in", text, msg)

            if isinstance(msg, dict) and "id" in msg and "method" not in msg:
                with self._pending_lock:
                    box = self._pending.pop(msg["id"], None)
                if box is not None:
                    box.put(msg)
                continue

            if isinstance(msg, dict) and "method" in msg:
                if "id" in msg:
                    self._handle_agent_request(msg)
                else:
                    self._handle_notification(msg)

        self._dead.set()

    def _read_stderr(self) -> None:
        assert self.proc is not None and self.proc.stderr is not None
        with open(self.stderr_path, "w", encoding="utf-8", newline="\n") as fh:
            for line in self.proc.stderr:
                fh.write(line)
                fh.flush()

    # ── the conversation ──────────────────────────────────────────────────

    def run(self) -> int:
        cmd = shlex.split(self.args.agent_cmd, posix=False) if os.name == "nt" else shlex.split(self.args.agent_cmd)
        print(f"  spawning: {cmd}")
        print(f"  cwd:      {self.args.cwd}")

        env = dict(os.environ)
        env.setdefault("PYTHONUNBUFFERED", "1")
        for item in self.args.env or []:
            key, _, value = item.partition("=")
            env[key] = value
            print(f"  env:      {key}={value}")

        self.proc = subprocess.Popen(
            cmd,
            cwd=self.args.cwd,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            encoding="utf-8",
            errors="replace",
            bufsize=1,
            env=env,
        )

        threading.Thread(target=self._read_stdout, daemon=True).start()
        threading.Thread(target=self._read_stderr, daemon=True).start()

        exit_code = 0
        try:
            caps: dict[str, Any] = {
                "fs": {
                    "readTextFile": self.args.fs,
                    "writeTextFile": self.args.fs,
                },
                "terminal": self.args.terminal,
            }
            self.init_result = self.request(
                "initialize",
                {
                    "protocolVersion": PROTOCOL_VERSION,
                    "clientCapabilities": caps,
                    "clientInfo": {"name": "acp-capture", "version": "1"},
                },
                timeout=self.args.handshake_timeout,
            )
            print(f"  initialize ok - agent protocolVersion "
                  f"{self.init_result.get('protocolVersion') if isinstance(self.init_result, dict) else '?'}")

            self.new_session_result = self.request(
                "session/new",
                {"cwd": os.path.abspath(self.args.cwd), "mcpServers": []},
                timeout=self.args.handshake_timeout,
            )
            self.session_id = (self.new_session_result or {}).get("sessionId")
            print(f"  session/new ok - sessionId {self.session_id}")

            print(f"  session/prompt -> waiting up to {self.args.timeout}s")
            rid = self.send_request(
                "session/prompt",
                {
                    "sessionId": self.session_id,
                    "prompt": [{"type": "text", "text": self.args.prompt}],
                },
            )
            if self.args.close_stdin_after_prompt:
                self.close_stdin()
            result = self.await_response(rid, "session/prompt", timeout=self.args.timeout)
            if isinstance(result, dict):
                self.stop_reason = result.get("stopReason")
                self.usage = result.get("usage")
            print(f"  prompt returned - stopReason={self.stop_reason}")

        except RuntimeError as exc:
            print(f"  ! {exc}")
            exit_code = 1
        finally:
            self._shutdown()

        self._write_summary(exit_code)
        return exit_code

    def _shutdown(self) -> None:
        if self.proc is None:
            return
        try:
            if self.session_id and not self._stdin_closed:
                # Best-effort: not all agents implement it, and a failure here
                # must not lose the capture.
                try:
                    self.request("session/close", {"sessionId": self.session_id}, timeout=10)
                except RuntimeError:
                    pass
            if self.proc.stdin and not self._stdin_closed:
                self.proc.stdin.close()
                self._stdin_closed = True
        except OSError:
            pass

        try:
            self.proc.wait(timeout=15)
        except subprocess.TimeoutExpired:
            print("  . agent did not exit - terminating")
            self.proc.terminate()
            try:
                self.proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.proc.kill()

        with self._file_lock:
            self._raw.close()
            self._frames.close()

    def _write_summary(self, exit_code: int) -> None:
        summary = {
            "label": self.label,
            "captured_at": _now(),
            "agent_cmd": self.args.agent_cmd,
            "cwd": os.path.abspath(self.args.cwd),
            "prompt": self.args.prompt,
            "client_capabilities": {"fs": self.args.fs, "terminal": self.args.terminal},
            "permission_policy": self.args.permission,
            "close_stdin_after_prompt": self.args.close_stdin_after_prompt,
            "close_stdin_on_tool_call": self.args.close_stdin_on_tool_call,
            "close_stdin_on_permission": self.args.close_stdin_on_permission,
            "capture_exit_code": exit_code,
            "agent_exit_code": self.proc.returncode if self.proc else None,
            "session_id": self.session_id,
            "stop_reason": self.stop_reason,
            "usage": self.usage,
            "initialize_result": self.init_result,
            "new_session_result": self.new_session_result,
            "session_update_kinds": dict(self.update_kinds),
            "agent_methods": dict(self.agent_methods),
            "permission_request_count": len(self.permission_requests),
            "permission_requests": self.permission_requests,
        }
        with open(self.summary_path, "w", encoding="utf-8", newline="\n") as fh:
            json.dump(summary, fh, indent=2, ensure_ascii=False)
            fh.write("\n")


def main() -> int:
    p = argparse.ArgumentParser(description="Record an ACP conversation with a coding agent.")
    p.add_argument("--agent-cmd", default="hermes acp", help='command that speaks ACP on stdio (default: "hermes acp")')
    p.add_argument("--cwd", required=True, help="working directory for the agent session")
    p.add_argument("--prompt", required=True, help="the user turn to send")
    p.add_argument("--outdir", required=True, help="directory to write artefacts into")
    p.add_argument("--label", required=True, help="filename prefix, e.g. plain / tool")
    p.add_argument("--close-stdin-on-tool-call", action="store_true",
                   help="hold stdin open until the first tool_call notification, then close it. "
                        "Releases the Hermes Windows terminal-tool deadlock while still capturing "
                        "every frame up to and including the tool_call payload.")
    p.add_argument("--close-stdin-on-permission", action="store_true",
                   help="close stdin immediately after answering the first "
                        "session/request_permission. For the edit path, where approval arrives "
                        "after tool_call and the sandbox only spawns once it is granted — "
                        "--close-stdin-on-tool-call would starve that callback.")
    p.add_argument("--close-stdin-after-prompt", action="store_true",
                   help="close the agent's stdin immediately after sending session/prompt. "
                        "Works around the Hermes v0.19.0 Windows terminal-tool deadlock, at the "
                        "cost of being unable to answer session/request_permission or fs/* callbacks.")
    p.add_argument("--env", action="append", metavar="KEY=VALUE",
                   help="set an environment variable for the agent process (repeatable)")
    p.add_argument("--timeout", type=float, default=600.0,
                   help="seconds to wait for session/prompt (default 600 — Hermes' terminal "
                        "sandbox setup alone can take minutes on a cold first tool call)")
    p.add_argument("--handshake-timeout", type=float, default=60.0, help="seconds for initialize and session/new")
    p.add_argument("--permission", choices=["allow", "deny"], default="allow",
                   help="how to answer session/request_permission (default allow)")
    p.add_argument("--fs", action="store_true", default=True, help="advertise fs client capability (default on)")
    p.add_argument("--no-fs", dest="fs", action="store_false", help="do not advertise fs capability")
    p.add_argument("--terminal", action="store_true", default=False,
                   help="advertise terminal client capability (default off — the agent then runs commands itself)")
    args = p.parse_args()

    os.makedirs(args.outdir, exist_ok=True)
    os.makedirs(args.cwd, exist_ok=True)

    cap = AcpCapture(args)
    rc = cap.run()

    print(f"  frames:  {cap.frames_path}")
    print(f"  raw:     {cap.raw_path}")
    print(f"  summary: {cap.summary_path}")
    if cap.update_kinds:
        print("  session/update kinds:")
        for kind, n in cap.update_kinds.most_common():
            print(f"    {n:5}  {kind}")
    else:
        print("  session/update kinds: none seen")
    return rc


if __name__ == "__main__":
    sys.exit(main())
