"""PROTOTYPE - throwaway. Three variants of the multi-Host Client, server-rendered.

Three variants on one route, switchable with ?variant=, answering one question:
what is the primary object the user is looking at?

  A  queue        - the Session is primary. One list of every Session, all Hosts.
  B  machine room - the Host is primary. A card per Host, Sessions inside it.
  C  desk         - the conversation is primary. One transcript, everything else a rail.

Each variant owns its own layout. Nothing is shared but the CSS tokens, the
switcher and the small helpers that shape data.
"""

from html import escape as esc

import world as w

VARIANTS = {"A": "Queue", "B": "Machine room", "C": "Desk"}

STATE_DOT = {"Ready": "ready", "Connecting": "connecting", "Down": "down",
             "Incompatible": "incompatible"}


def page(variant, body):
    return f"""<!doctype html>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Dispatch prototype - {VARIANTS[variant]}</title>
<link rel="stylesheet" href="/static/proto.css">
<body class="v{variant}">
{body}
{switcher(variant)}
<script>window.VARIANT = "{variant}";</script>
<script src="/static/proto.js"></script>
</body>"""


def switcher(variant):
    keys = list(VARIANTS)
    i = keys.index(variant)
    prev, nxt = keys[i - 1], keys[(i + 1) % len(keys)]
    return (f'<nav class="switcher"><a href="?variant={prev}">&larr;</a>'
            f'<span>{variant} &mdash; {VARIANTS[variant]}</span>'
            f'<a href="?variant={nxt}">&rarr;</a></nav>')


# pairs every ToolCallRequested with its approval and its end
def tool_calls(events):
    calls = {}
    for e in events:
        p = e["payload"]
        cid = p.get("toolCallId")
        if not cid:
            continue
        c = calls.setdefault(cid, {"asked": None, "decided": None, "ended": None})
        if e["kind"] == "ToolCallRequested":
            c["req"] = e
        elif e["kind"] == "ApprovalRequested":
            c["asked"] = e
        elif e["kind"] == "ApprovalDecided":
            c["decided"] = e
        elif e["kind"] == "ToolCallEnded":
            c["ended"] = e
    return calls


# one word for what a Tool Call is doing now, and the class that colours it
def call_status(c):
    if c["ended"]:
        outcome = c["ended"]["payload"]["outcome"]
        return {"ok": ("done", "ok"), "error": ("failed", "bad"),
                "refused": ("refused", "bad"),
                "unknown": ("no result reported", "unknown")}[outcome]
    if c["asked"] and not c["decided"]:
        return ("waiting for you", "asking")
    return ("running", "running")


def host_line(h):
    cls, words = w.host_wording(h)
    return cls, words


def presence_dot(h):
    return f'<i class="dot {STATE_DOT[h["state"]]}" data-host-dot="{h["id"]}"></i>'


def stale_stamp(s):
    return f'<span class="stamp">as of {w.host(s["host"])["seen"]}</span>'


def session_head(s):
    sp = w.spec(s)
    return sp["cwd"], f'{sp["harness"]} &middot; {esc(sp["model"])}'


def state_pill(s):
    st, reason = w.state_of(s["events"])
    label = f"{st} &middot; {reason}" if reason else st
    return (f'<span class="pill {st.lower()}" data-session-state="{s["id"]}">'
            f'{label}</span>')


def sort_key(s):
    order = {"Asking": 0, "Working": 1, "Starting": 2, "Idle": 3, "Ended": 4}
    return (1 if w.is_stale(s) else 0, order[w.state_of(s["events"])[0]])


# ---------------------------------------------------------------- variant A

def variant_a(focus):
    hosts = w.HOSTS
    ready = sum(1 for h in hosts if h["state"] == "Ready")
    bad = [h for h in hosts if h["state"] not in ("Ready", "Connecting")]
    strip = "".join(
        f'<span class="hchip" data-host-card="{h["id"]}">{presence_dot(h)}{h["label"]}'
        f'<em data-host-state="{h["id"]}">{host_line(h)[1]}</em></span>' for h in hosts)

    rows = []
    for s in sorted(w.SESSIONS, key=sort_key):
        h = w.host(s["host"])
        cwd, spec = session_head(s)
        st = w.state_of(s["events"])[0]
        gist = "the Hub stopped listening here"
        for e in reversed(s["events"]):
            p = e["payload"]
            said = p.get("text") or p.get("title") or p.get("message")
            if said:
                gist = said
                break
        rows.append(f"""
<article class="qrow{' stale' if w.is_stale(s) else ''}{' focus' if s['id'] == focus else ''}"
         data-session-row="{s['id']}" data-host="{h['id']}">
  <header onclick="location.search='?variant=A&amp;focus={s['id']}'">
    {state_pill(s)}
    <b>{esc(cwd)}</b>
    <span class="meta">{spec}</span>
    <span class="hchip small">{presence_dot(h)}{h['label']}</span>
    {stale_stamp(s) if w.is_stale(s) else ''}
  </header>
  <p class="gist">{esc(gist[:110])}</p>
  <div class="approval" data-approval="{s['id']}"></div>
  {steps(s) if s['id'] == focus else ''}
</article>""")

    warn = ""
    if bad:
        warn = ('<p class="hostwarn">' + " &middot; ".join(
            f'<b>{h["label"]}</b> {host_line(h)[1]}' for h in bad) + "</p>")

    return f"""
<header class="topbar">
  <h1>Sessions</h1>
  <span class="count">{len(w.live_sessions())} live on {ready} of {len(hosts)} Hosts</span>
  <div class="hosts">{strip}</div>
</header>
{warn}
<main class="queue">{''.join(rows)}</main>
{start_defaults()}
"""


# A renders a turn as a step list: a spine, with Tool Calls indented under it
def steps(s):
    if w.state_of(s["events"])[0] == "Ended":
        e = s["events"][-1]
        return f'<div class="steps"><div class="step end">Ended &middot; {e["payload"]["reason"]}</div></div>'
    ev = w.last_prompt(s["events"])
    calls = tool_calls(s["events"])
    out = []
    for e in ev:
        k, p = e["kind"], e["payload"]
        if k == "PromptSubmitted":
            out.append(f'<div class="step you"><span class="at">{e["at"]}</span>'
                       f'<b>You</b> {esc(p["text"])}</div>')
        elif k == "Reasoning":
            out.append(f'<details class="step think"><summary>thought for a moment</summary>'
                       f'{esc(p["text"])}</details>')
        elif k == "AssistantMessage":
            open_mark = "" if p["complete"] else '<i class="caret"></i>'
            out.append(f'<div class="step said"><span class="at">{e["at"]}</span>'
                       f'<span data-event-text="{e["seq"]}">{esc(p["text"])}</span>{open_mark}</div>')
        elif k == "ToolCallRequested":
            c = calls[p["toolCallId"]]
            words, cls = call_status(c)
            arg = " ".join(f"{k2}={v}" for k2, v in p["args"].items())
            ended = c["ended"]["payload"]["content"] if c["ended"] else ""
            by = ""
            if c["decided"]:
                by = f' <em>{c["decided"]["payload"]["by"]}</em>'
            elif not c["asked"]:
                by = ' <em>no Gate</em>'
            out.append(f"""
  <details class="step tool {cls}" data-call="{p['toolCallId']}">
    <summary><span class="kind">{p['toolKind']}</span> {esc(p['title'])}
      <span class="st">{words}</span>{by}</summary>
    <code>{esc(arg)}</code>
    <code class="result">{esc(ended)}</code>
  </details>""")
        elif k == "PromptCompleted":
            u = p["usage"]
            out.append(f'<div class="step done">{p["stopReason"]} &middot; '
                       f'{u["in"]} in, {u["out"]} out</div>')
        elif k == "Error":
            out.append(f'<div class="step err">{p["code"]}: {esc(p["message"])}</div>')
        elif k == "HubDetached":
            out.append('<div class="step gap">the Hub stopped listening here</div>')
    box = composer(s)
    return f'<div class="steps">{"".join(out)}</div>{box}'


def composer(s):
    st = w.state_of(s["events"])[0]
    if w.is_stale(s):
        return ('<div class="composer off">This Host is not answering. '
                "You are looking at history.</div>")
    if st == "Ended":
        return '<div class="composer off">This Session ended. Its history stays.</div>'
    if st in ("Working", "Asking"):
        return ('<div class="composer off">Working &mdash; a second Prompt is refused'
                ' <button>Interrupt</button></div>')
    return '<div class="composer"><input placeholder="Send a Prompt"><button>Send</button></div>'


# A resolves the four choices as defaults with an override
def start_defaults():
    opts = "".join(f'<option>{h["label"]}</option>' for h in w.HOSTS if h["state"] == "Ready")
    models = "".join(f'<option>{v} &middot; {m}</option>' for v, m in w.MODELS["desktop"])
    harn = "".join(f"<option>{h}</option>" for h in w.HARNESSES)
    return f"""
<aside class="starter">
  <button class="go">New Session &middot; Desktop &middot; qwen3.5-9b &middot; OpenCode</button>
  <details><summary>change</summary>
    <select>{opts}</select><select>{models}</select><select>{harn}</select>
    <input value="~/src/dispatch">
  </details>
</aside>"""


# ---------------------------------------------------------------- variant B

def variant_b(focus):
    cards = []
    for h in w.HOSTS:
        cls, words = host_line(h)
        ss = w.sessions_on(h["id"])
        vend = "".join(
            f'<li>{v["name"]} <span>{", ".join(v["resident"]) or "nothing resident"}</span></li>'
            for v in h["vendors"]) or "<li>no Vendor answered</li>"
        rows = "".join(f"""
    <tr class="{'stale' if w.is_stale(s) else ''}{' focus' if s['id'] == focus else ''}"
        data-session-row="{s['id']}" onclick="location.search='?variant=B&amp;focus={s['id']}'">
      <td>{state_pill(s)}</td><td>{esc(w.spec(s)["cwd"])}</td>
      <td class="meta">{w.spec(s)["harness"]}</td>
      <td class="meta">{esc(w.spec(s)["model"])}</td>
    </tr>
    <tr class="approvalrow"><td colspan="4"><div class="approval"
        data-approval="{s['id']}"></div></td></tr>""" for s in ss)
        stamp = ""
        if h["state"] != "Ready":
            stamp = (f'<div class="staleband">last true at {h["seen"]}.'
                     f'{" Sessions may still be running." if ss else ""}</div>')
        cards.append(f"""
<section class="hcard {cls}" data-host-card="{h['id']}">
  <header>{presence_dot(h)}<h2>{h['label']}</h2>
    <span class="badge" data-host-badge="{h['id']}"></span>
    <span class="hstate" data-host-state="{h['id']}">{words}</span></header>
  <p class="addr">{h['addr']}</p>
  <ul class="vendors">{vend}</ul>
  {stamp}
  <table class="sessions">{rows or '<tr><td colspan=4 class=meta>no Sessions</td></tr>'}</table>
  {start_form(h)}
</section>""")

    drawer = ops(w.session(focus)) if focus else '<p class="meta">Pick a Session.</p>'
    return f"""
<header class="topbar"><h1>Hosts</h1></header>
<main class="room">{''.join(cards)}</main>
<aside class="drawer">{drawer}</aside>
"""


# B renders a turn as an operations table with the text between the rows
def ops(s):
    calls = tool_calls(s["events"])
    out = []
    for e in w.last_prompt(s["events"]):
        k, p = e["kind"], e["payload"]
        if k == "PromptSubmitted":
            out.append(f'<p class="you">{esc(p["text"])}</p>')
        elif k == "AssistantMessage":
            out.append(f'<p class="said" data-event-text="{e["seq"]}">{esc(p["text"])}</p>')
        elif k == "Reasoning":
            out.append(f'<details class="think"><summary>reasoning</summary>{esc(p["text"])}</details>')
        elif k == "ToolCallRequested":
            c = calls[p["toolCallId"]]
            words, cls = call_status(c)
            res = c["ended"]["payload"]["content"] if c["ended"] else ""
            gate = "auto" if c["decided"] and c["decided"]["payload"]["by"] == "policy" else (
                "you" if c["decided"] else ("no Gate" if not c["asked"] else "held"))
            out.append(f"""
<table class="ops"><tr class="{cls}" data-call="{p['toolCallId']}">
  <td class="kind">{p['toolKind']}</td><td>{esc(p['title'])}</td>
  <td class="gate">{gate}</td><td class="st">{words}</td><td class="res">{esc(res)}</td>
</tr></table>""")
        elif k == "PromptCompleted":
            out.append(f'<p class="meta">{p["stopReason"]} &middot; {p["usage"]["out"]} out</p>')
        elif k == "Error":
            out.append(f'<p class="err">{p["code"]}: {esc(p["message"])}</p>')
        elif k == "HubDetached":
            out.append('<p class="gap">the Hub stopped listening here</p>')
    h = w.host(s["host"])
    return (f'<header class="dhead">{presence_dot(h)}<b>{h["label"]}</b> '
            f'{esc(w.spec(s)["cwd"])} {state_pill(s)}</header>'
            f'<div class="opsbody">{"".join(out)}</div>{composer(s)}')


# B resolves the four choices as a dense form, with the Host already chosen
def start_form(h):
    if h["state"] != "Ready":
        return f'<div class="startform off">Cannot start: {host_line(h)[1].lower()}</div>'
    models = "".join(f'<option>{v} &middot; {m}</option>' for v, m in w.MODELS[h["id"]])
    harn = "".join(f"<option>{x}</option>" for x in w.HARNESSES)
    return (f'<div class="startform"><select>{models}</select>'
            f'<select>{harn}</select><button>Start</button></div>')


# ---------------------------------------------------------------- variant C

def variant_c(focus):
    s = w.session(focus)
    h = w.host(s["host"])
    rail = []
    for o in sorted(w.SESSIONS, key=sort_key):
        oh = w.host(o["host"])
        rail.append(f"""
  <a class="rrow{' on' if o['id'] == focus else ''}{' stale' if w.is_stale(o) else ''}"
     href="?variant=C&amp;focus={o['id']}" data-session-row="{o['id']}">
    {presence_dot(oh)}<b>{esc(w.spec(o)["cwd"])}</b>
    <span class="meta">{oh['label']}</span>{state_pill(o)}</a>""")

    band = ""
    if w.is_stale(s):
        band = (f'<div class="band">{h["label"]}: {host_line(h)[1].lower()} since '
                f'{h["seen"]}. This Session may still be running. You see history.</div>')

    return f"""
<nav class="rail"><h1>Dispatch</h1>{''.join(rail)}<button class="new">New Session</button></nav>
<main class="desk">
  <header class="dhead">{presence_dot(h)}<b>{h['label']}</b>
    <span data-host-state="{h['id']}">{host_line(h)[1]}</span>
    <span class="meta">{esc(w.spec(s)["cwd"])} &middot; {w.spec(s)["harness"]}
    &middot; {esc(w.spec(s)["model"])}</span>{state_pill(s)}</header>
  {band}
  <div class="transcript">{turn(s)}</div>
  {composer(s)}
</main>
<div class="approval" id="toast"></div>
{wizard()}
"""


# C renders a turn as a transcript with the Tool Calls inset inside the turn
def turn(s):
    calls = tool_calls(s["events"])
    out = []
    for e in s["events"]:
        k, p = e["kind"], e["payload"]
        if k == "SessionStarted":
            out.append(f'<div class="sys">Session started on {p["vendor"]} &middot; {p["model"]}</div>')
        elif k == "PromptSubmitted":
            out.append(f'<div class="bubble you">{esc(p["text"])}</div>')
        elif k == "Reasoning":
            out.append(f'<details class="inset think"><summary>thought</summary>'
                       f'{esc(p["text"])}</details>')
        elif k == "AssistantMessage":
            open_mark = "" if p["complete"] else '<i class="caret"></i>'
            out.append(f'<div class="bubble said"><span data-event-text="{e["seq"]}">'
                       f'{esc(p["text"])}</span>{open_mark}</div>')
        elif k == "ToolCallRequested":
            c = calls[p["toolCallId"]]
            words, cls = call_status(c)
            res = c["ended"]["payload"]["content"] if c["ended"] else ""
            ask = ""
            if c["asked"]:
                d = c["decided"]["payload"] if c["decided"] else None
                ask = (f'<div class="askline">asked &rarr; {d["decision"]} by {d["by"]}</div>'
                       if d else '<div class="askline">waiting for your answer</div>')
            out.append(f"""
<details class="inset call {cls}" data-call="{p['toolCallId']}" open>
  <summary><span class="kind">{p['toolKind']}</span> {esc(p['title'])}
    <span class="st">{words}</span></summary>
  {ask}<code>{esc(str(p['args']))}</code>
  <code class="result">{esc(res)}</code>
</details>""")
        elif k == "PromptCompleted":
            out.append(f'<div class="sys">{p["stopReason"]} &middot; {p["usage"]["in"]} in, '
                       f'{p["usage"]["out"]} out</div>')
        elif k == "Error":
            out.append(f'<div class="sys err">{p["code"]}: {esc(p["message"])}</div>')
        elif k == "SessionEnded":
            out.append(f'<div class="sys">Session ended &middot; {p["reason"]}</div>')
        elif k == "HubDetached":
            out.append('<div class="sys gap">the Hub stopped listening here</div>')
    return "".join(out)


# C resolves the four choices as a wizard
def wizard():
    hosts = "".join(
        f'<button class="wopt"{" disabled" if h["state"] != "Ready" else ""}>'
        f'{h["label"]}<em>{host_line(h)[1]}</em></button>' for h in w.HOSTS)
    return f"""
<dialog class="wiz" id="wiz">
  <ol class="wsteps"><li class="on">Host</li><li>Model</li><li>Harness</li><li>Policy</li></ol>
  <h2>Which machine?</h2>
  <div class="wopts">{hosts}</div>
  <footer><button onclick="wiz.close()">Cancel</button>
    <button class="go">Next</button></footer>
</dialog>"""


RENDER = {"A": variant_a, "B": variant_b, "C": variant_c}
