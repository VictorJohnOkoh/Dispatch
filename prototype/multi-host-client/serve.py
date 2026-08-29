#!/usr/bin/env python
"""PROTOTYPE - throwaway. Serves the multi-Host Client prototype.

    python prototype/multi-host-client/serve.py     ->  http://127.0.0.1:8765/?variant=A

Server-rendered HTML and a little vanilla JS, faking all data. No Daemon, no Hub,
no persistence. The stream at /stream replays a fixed script so the interesting
Frames arrive while you watch: Deltas into an open message, a PromptCompleted, an
ApprovalRequested on a Host you are not looking at, and a Host that drops to
Connecting and comes back.
"""

import json
import os
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, urlparse

sys.path.insert(0, str(Path(__file__).parent))

import render
import world as w

HERE = Path(__file__).parent
PORT = int(os.environ.get("PORT", 8765))

FULL = ("Renamed the handler to HandleEventStream and updated both callers. "
        "The test still names the old symbol, so I will fix that next.")

# what the stream sends, and how long after the Client attaches
SCRIPT = [
    (0.4, "delta", {"seq": 9425, "n": 0, "text": "Renamed the handler "}),
    (1.2, "delta", {"seq": 9425, "n": 1, "text": "to HandleEventStream and "}),
    (2.0, "delta", {"seq": 9425, "n": 2, "text": "updated both callers. "}),
    (2.8, "delta", {"seq": 9425, "n": 3, "text": FULL, "final": True}),
    (3.6, "event", {"seq": 9426, "session": "s-7f3a2c", "host": "desktop", "at": "14:37",
                    "kind": "PromptCompleted",
                    "payload": {"stopReason": "end_turn", "usage": {"in": 8120, "out": 412}}}),
    (6.0, "event", {"seq": 620, "session": "s-4d10", "host": "shed", "at": "14:37",
                    "kind": "ToolCallRequested",
                    "payload": {"toolCallId": "tc-9", "name": "bash", "toolKind": "execute",
                                "title": "run rm -rf build/", "args": {"cmd": "rm -rf build/"}}}),
    (6.2, "event", {"seq": 621, "session": "s-4d10", "host": "shed", "at": "14:37",
                    "kind": "ApprovalRequested",
                    "payload": {"toolCallId": "tc-9", "title": "run rm -rf build/",
                                "detail": "in ~/src/notes"}}),
    (14.0, "host", {"host": "desktop", "state": "Connecting", "cause": None}),
    (20.0, "host", {"host": "desktop", "state": "Ready", "cause": None}),
]

# Frames a command produced, waiting for the open stream to pick them up
injected = []


def frame(name, data):
    body = f"event: {name}\ndata: {json.dumps(data)}\n\n"
    return body.encode()


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def do_GET(self):
        u = urlparse(self.path)
        if u.path == "/stream":
            return self.stream()
        if u.path.startswith("/static/"):
            return self.static(u.path[8:])
        if u.path != "/":
            return self.send_error(404)
        q = parse_qs(u.query)
        variant = q.get("variant", ["A"])[0].upper()
        if variant not in render.RENDER:
            variant = "A"
        focus = q.get("focus", ["s-7f3a2c"])[0]
        body = render.page(variant, render.RENDER[variant](focus))
        self.reply(200, "text/html; charset=utf-8", body.encode())

    def do_POST(self):
        # every command answers only that it was accepted. The answer arrives as Events.
        if "/approvals" in self.path:
            n = int(self.headers.get("content-length", 0))
            body = json.loads(self.rfile.read(n) or "{}")
            decision = body.get("decision", "refused")
            injected.append(("event", {
                "seq": 622, "session": "s-4d10", "host": "shed", "at": "14:38",
                "kind": "ApprovalDecided",
                "payload": {"toolCallId": "tc-9", "decision": decision, "by": "user"}}))
            injected.append(("event", {
                "seq": 623, "session": "s-4d10", "host": "shed", "at": "14:38",
                "kind": "ToolCallEnded",
                "payload": {"toolCallId": "tc-9",
                            "outcome": "ok" if decision == "allowed" else "refused",
                            "content": "build/ removed" if decision == "allowed" else "refused"}}))
        self.reply(202, "application/json", b"{}")

    def static(self, name):
        f = HERE / "static" / name
        if not f.is_file():
            return self.send_error(404)
        kind = "text/css" if name.endswith(".css") else "text/javascript"
        self.reply(200, kind + "; charset=utf-8", f.read_bytes())

    def reply(self, code, kind, body):
        self.send_response(code)
        self.send_header("content-type", kind)
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def stream(self):
        self.send_response(200)
        self.send_header("content-type", "text/event-stream")
        self.send_header("cache-control", "no-store")
        self.end_headers()
        start = time.monotonic()
        self.wfile.write(frame("hello", {"version": 1, "logIds": {"desktop": "d-1", "shed": "s-1"}}))
        sent, beat = 0, 0.0
        try:
            while True:
                t = time.monotonic() - start
                while sent < len(SCRIPT) and SCRIPT[sent][0] <= t:
                    _, name, data = SCRIPT[sent]
                    self.wfile.write(frame(name, data))
                    sent += 1
                while injected:
                    name, data = injected.pop(0)
                    self.wfile.write(frame(name, data))
                if t - beat > 10:
                    beat = t
                    self.wfile.write(b": keepalive\n\n")
                self.wfile.flush()
                time.sleep(0.1)
        except (BrokenPipeError, ConnectionAbortedError, ConnectionResetError):
            pass


class Server(ThreadingHTTPServer):
    # a browser dropping a stream is normal here, not a traceback
    def handle_error(self, request, addr):
        if not isinstance(sys.exc_info()[1], OSError):
            super().handle_error(request, addr)


if __name__ == "__main__":
    live = len(w.live_sessions())
    print(f"PROTOTYPE  http://127.0.0.1:{PORT}/?variant=A   ({live} live Sessions faked)")
    print("variants: A queue, B machine room, C desk.  Arrow keys switch.")
    Server(("127.0.0.1", PORT), Handler).serve_forever()
