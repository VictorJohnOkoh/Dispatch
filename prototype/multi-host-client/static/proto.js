// PROTOTYPE - throwaway. Applies the Frames the fake stream sends, and switches variants.

const applied = {};   // seq -> how many Deltas this Client has applied
const asking = {};    // session -> the open question

const all = (sel) => document.querySelectorAll(sel);
const one = (sel) => document.querySelector(sel);

function setState(session, label, cls) {
  all(`[data-session-state="${session}"]`).forEach((el) => {
    el.textContent = label;
    el.className = "pill " + cls;
  });
}

// A Delta lands only when its N matches what this Client already applied.
// The final Delta carries the whole text and replaces, so a dropped one repairs itself.
function delta(d) {
  const el = one(`[data-event-text="${d.seq}"]`);
  if (!el) return;
  const seen = applied[d.seq] || 0;
  if (d.final) {
    el.textContent = d.text;
    el.parentElement.querySelector(".caret")?.remove();
    applied[d.seq] = seen + 1;
    return;
  }
  if (d.n !== seen) return;
  el.textContent += d.text;
  applied[d.seq] = seen + 1;
}

// the box that refuses a Prompt goes when the reason for it goes: the Session left
// Working, or the Host came back. An Ended Session keeps its box either way.
function reopenComposer(hostCameBack) {
  const box = one(".composer.off");
  if (!box) return;
  const reason = hostCameBack ? "Reconnecting" : "Working";
  if (box.textContent.indexOf(reason) < 0) return;
  box.className = "composer";
  box.innerHTML = '<input placeholder="Send a Prompt"><button>Send</button>';
}

// the same mapping the server renders, so a live outcome is coloured like a replayed one
const OUTCOME = {
  ok: ["done", "ok"], error: ["failed", "bad"],
  refused: ["refused", "bad"], unknown: ["no result reported", "unknown"],
};

function callRow(id, words, cls) {
  const el = one(`[data-call="${id}"]`);
  if (!el) return;
  el.className = el.className.replace(/(running|asking|ok|bad|unknown)/, cls);
  const st = el.querySelector(".st");
  if (st) st.textContent = words;
}

function event(e) {
  const p = e.payload;
  if (e.kind === "PromptCompleted") {
    setState(e.session, "Idle", "idle");
    reopenComposer();
  } else if (e.kind === "ApprovalRequested") {
    asking[e.session] = p;
    setState(e.session, "Asking", "asking");
    demand(e.host, e.session, p);
  } else if (e.kind === "ApprovalDecided") {
    delete asking[e.session];
    setState(e.session, "Working", "working");
    callRow(p.toolCallId, p.decision + " by " + p.by, p.decision === "allowed" ? "running" : "bad");
    clearDemand(e.host, e.session);
  } else if (e.kind === "ToolCallEnded") {
    const [words, cls] = OUTCOME[p.outcome];
    callRow(p.toolCallId, words, cls);
    const res = one(`[data-call="${p.toolCallId}"] .result`);
    if (res) res.textContent = p.content;
  }
  countTitle();
}

// A Host State change reaches every Session that lives on that Host, because
// Working on a Ready Host and Working on a Host that stopped answering are not
// the same row.
function host(f) {
  const key = f.state === "Down" ? "Down/" + f.cause : f.state;
  all(`[data-host-state="${f.host}"]`).forEach((el) => (el.textContent = WORDING[key]));
  all(`[data-host-dot="${f.host}"]`).forEach((el) => {
    el.className = "dot " + f.state.toLowerCase();
  });
  const gone = f.state === "Down" || f.state === "Incompatible";
  one(`[data-host-card="${f.host}"]`)?.classList.remove("ready", "connecting", "down",
    "incompatible");
  one(`[data-host-card="${f.host}"]`)?.classList.add(f.state.toLowerCase());
  all(`[data-host="${f.host}"]`).forEach((row) => {
    row.classList.toggle("reconnecting", f.state === "Connecting");
    row.classList.toggle("stale", gone);
    if (row.classList.contains("focus")) closeComposer(f.state, gone);
  });
}

// the composer follows the Host, not only the Session
function closeComposer(state, gone) {
  const box = one("[data-composer]");
  if (!box) return;
  if (state === "Ready") return reopenComposer(true);
  box.className = "composer off";
  box.textContent = gone
    ? "This Host is not answering. You are looking at history."
    : "Reconnecting to this Host. Nothing is lost, and the gap replays.";
}

// Each variant demands attention its own way. This is the whole disagreement.
function demand(hostId, session, p) {
  const bar = document.createElement("div");
  bar.className = "askbar";
  bar.innerHTML =
    `<b>${hostId}</b> wants to <b>${p.title}</b> <span>${p.detail}</span>` +
    `<button data-yes>Allow</button><button data-no>Deny</button>` +
    `<a href="?variant=${VARIANT}&focus=${session}">Open</a>`;
  bar.querySelector("[data-yes]").onclick = () => decide(hostId, session, "allowed");
  bar.querySelector("[data-no]").onclick = () => decide(hostId, session, "refused");

  if (VARIANT === "A") {
    // the queue reorders: the Session that is waiting jumps to the top
    const row = one(`[data-session-row="${session}"]`);
    row.classList.add("asking");
    row.parentElement.prepend(row);
    row.querySelector(".approval").append(bar);
  } else if (VARIANT === "B") {
    // every Host card is already on screen, so the card marks itself
    const card = one(`[data-host-card="${hostId}"]`);
    card.classList.add("asking");
    card.querySelector("[data-host-badge]").textContent = "1 waiting";
    one(`[data-approval="${session}"]`).append(bar);
  } else {
    // the other Sessions are off screen, so it has to come to the front
    const toast = one("#toast");
    toast.append(bar);
    toast.classList.add("toast");
  }
}

function clearDemand(hostId, session) {
  all(`[data-approval="${session}"]`).forEach((el) => el.replaceChildren());
  one(`[data-session-row="${session}"]`)?.classList.remove("asking");
  one("#toast")?.replaceChildren();
  const card = one(`[data-host-card="${hostId}"]`);
  if (card) {
    card.classList.remove("asking");
    const b = card.querySelector("[data-host-badge]");
    if (b) b.textContent = "";
  }
}

function decide(hostId, session, decision) {
  fetch(`/v1/hosts/${hostId}/sessions/${session}/approvals`, {
    method: "POST",
    body: JSON.stringify({ toolCallId: asking[session].toolCallId, decision }),
  });
}

function countTitle() {
  const n = Object.keys(asking).length;
  document.title = (n ? `(${n}) ` : "") + document.title.replace(/^\(\d+\) /, "");
}

const es = new EventSource("/stream");
es.addEventListener("delta", (m) => delta(JSON.parse(m.data)));
es.addEventListener("event", (m) => event(JSON.parse(m.data)));
es.addEventListener("host", (m) => host(JSON.parse(m.data)));

addEventListener("keydown", (e) => {
  if (/INPUT|TEXTAREA/.test(document.activeElement.tagName)) return;
  const keys = ["A", "B", "C"];
  const i = keys.indexOf(VARIANT);
  if (e.key === "ArrowLeft") location.search = setVariant(keys[(i + 2) % 3]);
  if (e.key === "ArrowRight") location.search = setVariant(keys[(i + 1) % 3]);
});

function setVariant(v) {
  const q = new URLSearchParams(location.search);
  q.set("variant", v);
  return "?" + q;
}

one(".new")?.addEventListener("click", () => one("#wiz").showModal());
