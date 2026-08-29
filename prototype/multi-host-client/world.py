"""PROTOTYPE — throwaway. Fake data for the multi-Host Client, plus the one piece
of real logic: folding a Session's Events into a Session State (ADR 0008)."""

NOW = "14:37"

HOSTS = [
    {
        "id": "desktop",
        "label": "Desktop",
        "addr": "victor@desktop.lan",
        "state": "Ready",
        "cause": None,
        "seen": NOW,
        "vendors": [
            {"name": "llama-swap", "resident": ["capstone/qwen3.5-9b"]},
            {"name": "Ollama", "resident": []},
        ],
    },
    {
        "id": "shed",
        "label": "Shed box",
        "addr": "victor@shed.lan",
        "state": "Ready",
        "cause": None,
        "seen": NOW,
        "vendors": [
            {"name": "LM Studio", "resident": ["glm-4-9b"]},
        ],
    },
    {
        "id": "laptop",
        "label": "Laptop",
        "addr": "victor@laptop.lan",
        "state": "Down",
        "cause": "no-daemon",
        "seen": "14:02",
        "vendors": [
            {"name": "Ollama", "resident": ["llama3.1:8b"]},
        ],
    },
    {
        "id": "garage",
        "label": "Garage rig",
        "addr": "victor@garage.lan",
        "state": "Connecting",
        "cause": None,
        "seen": "14:35",
        "vendors": [
            {"name": "llama-swap", "resident": ["capstone/devstral-24b"]},
        ],
    },
    {
        "id": "oldbox",
        "label": "Old box",
        "addr": "victor@oldbox.lan",
        "state": "Incompatible",
        "cause": None,
        "seen": "09:14",
        "vendors": [],
    },
]

# What the Client says, against what the Hub holds. ADR 0004: the wording and the
# state deliberately disagree, and the translation lives here.
HOST_WORDING = {
    "Ready": ("ready", "Ready"),
    "Connecting": ("connecting", "Reconnecting"),
    "Down/unreachable": ("down", "Machine is off or off the network"),
    "Down/no-daemon": ("down", "Machine is up, Daemon is not running"),
    "Incompatible": ("incompatible", "Daemon speaks protocol 1, Hub needs 2"),
}


def host_wording(host):
    key = host["state"]
    if key == "Down":
        key = "Down/" + host["cause"]
    return HOST_WORDING[key]


MODELS = {
    "desktop": [("llama-swap", "capstone/qwen3.5-9b"), ("llama-swap", "capstone/devstral-24b"),
                ("Ollama", "llama3.1:8b")],
    "shed": [("LM Studio", "glm-4-9b"), ("LM Studio", "qwen2.5-coder-7b")],
    "laptop": [("Ollama", "llama3.1:8b")],
    "garage": [("llama-swap", "capstone/devstral-24b")],
    "oldbox": [],
}

HARNESSES = ["OpenCode", "Pi", "passthrough"]


def ev(seq, session, at, kind, **payload):
    return {"seq": seq, "session": session, "at": at, "kind": kind, "payload": payload}


POLICY = {"read": "auto", "edit": "wait", "execute": "wait", "fetch": "wait", "other": "wait"}

# desktop / s-7f3a2c - the Session in focus. OpenCode, mid-Prompt, message still open.
S_FOCUS = [
    ev(9410, "s-7f3a2c", "14:31", "SessionStarted", harness="OpenCode", vendor="llama-swap",
       model="capstone/qwen3.5-9b", cwd="~/src/dispatch"),
    ev(9411, "s-7f3a2c", "14:31", "ApprovalPolicySet", setBy="user", **POLICY),
    ev(9412, "s-7f3a2c", "14:31", "SessionReady", model="capstone/qwen3.5-9b"),
    ev(9413, "s-7f3a2c", "14:33", "PromptSubmitted", text="rename the handler and update its callers"),
    ev(9414, "s-7f3a2c", "14:33", "Reasoning", complete=True,
       text="The handler is HandleEvents in hub/handler.go. Two callers: serve.go and the test. "
            "Rename to HandleEventStream, then fix both call sites."),
    ev(9415, "s-7f3a2c", "14:33", "AssistantMessage", complete=True,
       text="I will rename it and update the two callers."),
    ev(9416, "s-7f3a2c", "14:33", "ToolCallRequested", toolCallId="tc-1", name="read",
       toolKind="read", title="read src/hub/handler.go", args={"path": "src/hub/handler.go"}),
    ev(9417, "s-7f3a2c", "14:33", "ToolCallEnded", toolCallId="tc-1", outcome="ok",
       content="118 lines"),
    ev(9418, "s-7f3a2c", "14:34", "ToolCallRequested", toolCallId="tc-2", name="edit",
       toolKind="edit", title="edit src/hub/handler.go",
       args={"path": "src/hub/handler.go", "replace": "HandleEvents -> HandleEventStream"}),
    ev(9419, "s-7f3a2c", "14:34", "ApprovalRequested", toolCallId="tc-2",
       title="edit src/hub/handler.go", detail="1 line"),
    ev(9420, "s-7f3a2c", "14:34", "ApprovalDecided", toolCallId="tc-2", decision="allowed", by="user"),
    ev(9421, "s-7f3a2c", "14:34", "ToolCallEnded", toolCallId="tc-2", outcome="ok",
       content="1 line changed"),
    ev(9422, "s-7f3a2c", "14:35", "ToolCallRequested", toolCallId="tc-3", name="edit",
       toolKind="edit", title="edit src/hub/serve.go",
       args={"path": "src/hub/serve.go", "replace": "HandleEvents -> HandleEventStream"}),
    ev(9423, "s-7f3a2c", "14:35", "ApprovalDecided", toolCallId="tc-3", decision="allowed",
       by="policy"),
    ev(9424, "s-7f3a2c", "14:35", "ToolCallEnded", toolCallId="tc-3", outcome="ok",
       content="1 line changed"),
    ev(9425, "s-7f3a2c", "14:36", "AssistantMessage", complete=False, text=""),
]

# shed / s-4d10 - Pi, Working. The live ApprovalRequested lands here, on the Host
# the user is not looking at.
S_SHED = [
    ev(612, "s-4d10", "14:20", "SessionStarted", harness="Pi", vendor="LM Studio",
       model="glm-4-9b", cwd="~/src/notes"),
    ev(613, "s-4d10", "14:20", "ApprovalPolicySet", setBy="user", **POLICY),
    ev(614, "s-4d10", "14:20", "SessionReady", model="glm-4-9b"),
    ev(615, "s-4d10", "14:28", "PromptSubmitted", text="clean the build directory and rebuild"),
    ev(616, "s-4d10", "14:28", "AssistantMessage", complete=True,
       text="I will check what is in build/ first."),
    ev(617, "s-4d10", "14:28", "ToolCallRequested", toolCallId="tc-7", name="read",
       toolKind="read", title="read build/manifest.txt", args={"path": "build/manifest.txt"}),
    ev(618, "s-4d10", "14:29", "ToolCallEnded", toolCallId="tc-7", outcome="unknown",
       content="no result reported"),
    ev(619, "s-4d10", "14:36", "AssistantMessage", complete=True,
       text="build/ holds stale objects. I will remove it and rebuild."),
]

# laptop / s-22ae - was Working when the Hub lost the Host. History stays, and it
# may still be running. This is Stale content, stamped.
S_STALE = [
    ev(41, "s-22ae", "13:55", "SessionStarted", harness="OpenCode", vendor="Ollama",
       model="llama3.1:8b", cwd="~/src/dispatch"),
    ev(42, "s-22ae", "13:55", "ApprovalPolicySet", setBy="user", **POLICY),
    ev(43, "s-22ae", "13:55", "SessionReady", model="llama3.1:8b"),
    ev(44, "s-22ae", "14:00", "PromptSubmitted", text="write the retention test"),
    ev(45, "s-22ae", "14:01", "AssistantMessage", complete=True,
       text="Writing a test that fills the log past the flush threshold."),
    ev(46, "s-22ae", "14:02", "ToolCallRequested", toolCallId="tc-4", name="edit",
       toolKind="edit", title="edit log_test.go", args={"path": "src/daemon/log_test.go"}),
    ev(47, "s-22ae", "14:02", "ApprovalDecided", toolCallId="tc-4", decision="allowed", by="policy"),
    ev(48, "s-22ae", "14:03", "HubDetached"),
]

# desktop / s-91b0 - passthrough. No tools, so no Approval Policy.
S_PASS = [
    ev(9350, "s-91b0", "14:05", "SessionStarted", harness="passthrough", vendor="Ollama",
       model="llama3.1:8b", cwd="~"),
    ev(9351, "s-91b0", "14:05", "SessionReady", model="llama3.1:8b"),
    ev(9352, "s-91b0", "14:06", "PromptSubmitted", text="what does SO_REUSEPORT do"),
    ev(9353, "s-91b0", "14:06", "AssistantMessage", complete=True,
       text="It lets several sockets bind the same port, and the kernel spreads accepts across them."),
    ev(9354, "s-91b0", "14:06", "PromptCompleted", stopReason="end_turn",
       usage={"in": 44, "out": 210}),
]

# desktop / s-6612 - Ended{failed}. The launch never reported ready.
S_DEAD = [
    ev(9300, "s-6612", "13:40", "SessionStarted", harness="OpenCode", vendor="llama-swap",
       model="capstone/devstral-24b", cwd="~/src/dispatch"),
    ev(9301, "s-6612", "13:41", "Error", code="launch_timeout",
       message="the Harness did not report ready in 90s"),
    ev(9302, "s-6612", "13:41", "SessionEnded", reason="failed"),
]

# garage / s-88c1 - the Host is Connecting, so this is the last display, held
S_HELD = [
    ev(210, "s-88c1", "14:12", "SessionStarted", harness="OpenCode", vendor="llama-swap",
       model="capstone/devstral-24b", cwd="~/src/dispatch"),
    ev(211, "s-88c1", "14:12", "ApprovalPolicySet", setBy="user", **POLICY),
    ev(212, "s-88c1", "14:12", "SessionReady", model="capstone/devstral-24b"),
    ev(213, "s-88c1", "14:30", "PromptSubmitted", text="explain the admission rule"),
    ev(214, "s-88c1", "14:31", "AssistantMessage", complete=True,
       text="Admission runs before the Session exists, so a refusal writes no Event."),
    ev(215, "s-88c1", "14:31", "PromptCompleted", stopReason="end_turn",
       usage={"in": 190, "out": 88}),
]

SESSIONS = [
    {"id": "s-7f3a2c", "host": "desktop", "events": S_FOCUS},
    {"id": "s-88c1", "host": "garage", "events": S_HELD},
    {"id": "s-4d10", "host": "shed", "events": S_SHED},
    {"id": "s-22ae", "host": "laptop", "events": S_STALE},
    {"id": "s-91b0", "host": "desktop", "events": S_PASS},
    {"id": "s-6612", "host": "desktop", "events": S_DEAD},
]


# folds a Session's Events into its State (ADR 0008)
def state_of(events):
    ready = prompt_open = False
    open_questions = set()
    for e in events:
        k, p = e["kind"], e["payload"]
        if k == "SessionEnded":
            return "Ended", p["reason"]
        if k == "SessionReady":
            ready = True
        elif k == "PromptSubmitted":
            prompt_open = True
        elif k == "PromptCompleted":
            prompt_open = False
        elif k == "ApprovalRequested":
            open_questions.add(p["toolCallId"])
        elif k == "ApprovalDecided":
            open_questions.discard(p["toolCallId"])
    if open_questions:
        return "Asking", None
    if not ready:
        return "Starting", None
    return ("Working" if prompt_open else "Idle"), None


# the SessionStarted payload, which is where a Session's fixed facts live
def spec(s):
    return s["events"][0]["payload"]


def host(host_id):
    return next(h for h in HOSTS if h["id"] == host_id)


def session(session_id):
    return next(s for s in SESSIONS if s["id"] == session_id)


def sessions_on(host_id):
    return [s for s in SESSIONS if s["host"] == host_id]


def live_sessions():
    return [s for s in SESSIONS if state_of(s["events"])[0] != "Ended"]


# a Session on a Host that is Down is shown as it last was, dimmed and stamped
def is_stale(s):
    return host(s["host"])["state"] in ("Down", "Incompatible")


# Connecting holds the last display with a mark instead of dimming it (ADR 0004),
# because the blink is usually over before seven seconds
def is_reconnecting(s):
    return host(s["host"])["state"] == "Connecting"


# the last Prompt and everything the Session did because of it
def last_prompt(events):
    start = 0
    for i, e in enumerate(events):
        if e["kind"] == "PromptSubmitted":
            start = i
    return events[start:]
