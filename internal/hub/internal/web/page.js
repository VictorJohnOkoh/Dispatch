// The live half of the page. The Hub drew the transcript already, so this file
// only applies what arrives after that paint: an Event becomes a row, a Delta adds
// text to a row that is already there.
//
// The page loads fold.js and render.js before this one. Those two are the Client's
// own copy of what the Hub does on the server and they touch no page, so this file
// is the wiring, and the wiring is the only part that needs a browser to run.

const list = document.getElementById("transcript");
const stateLine = document.getElementById("state");
const host = list.dataset.host;
const session = list.dataset.session;

// events is this Session's own Events by Sequence Number, which is what the fold
// reads. It is keyed rather than a list, so an Event that arrives twice, once on
// the stream and once from a refetch, folds once.
const events = new Map();

// order is those Sequence Numbers, ascending. The fold reads Events in Seq order
// and the page draws rows in it, so the order is kept as they arrive rather than
// sorted again on every Event.
const order = [];

// generation counts the times this page has thrown away what it held. A load that
// started before a resync belongs to a page that no longer exists, so it stops
// rather than putting discarded rows back.
let generation = 0;

// rows is every row on the page by Sequence Number, so a Delta finds its message
// without searching, and a replayed Event replaces its row instead of doubling it.
const rows = new Map();

// held is how much text each of those rows holds. A Delta that carries on from
// exactly that much is appended, and only one that does not has to read the
// message back out of the DOM. Without it, a message that arrives in a thousand
// Deltas is copied whole a thousand times.
const held = new Map();

// The first paint's rows are adopted, and its Events are read from the page
// beside them. The rows are what a person reads and the payloads are what the
// fold reads, and the page carries both so that nothing has to be fetched again
// to know what is already on screen.
for (const el of list.children) {
  const seq = Number(el.dataset.seq);
  order.push(seq);
  rows.set(seq, el);
  remember(seq, el);
}
for (const e of JSON.parse(document.getElementById("events").textContent || "[]")) {
  events.set(e.seq, { kind: e.kind, payload: e.payload });
}

// remember records what one row's message holds, or forgets a row that has no
// message to add to.
function remember(seq, el) {
  const text = el.querySelector(".text");
  if (text) held.set(seq, text.textContent.length);
  else held.delete(seq);
}

// The stream resumes at the Cursor the first paint was drawn at. It goes in the
// query because an EventSource sets no headers; on every reconnect after this the
// browser sends Last-Event-ID itself, and the Hub prefers that.
const url = "/v1/events?from=" + encodeURIComponent(list.dataset.cursor);
const stream = new EventSource(url);

stream.addEventListener("event", (frame) => {
  const f = JSON.parse(frame.data);
  // Every Host's Events arrive on this one stream. The Session on screen is drawn
  // from them; every other Session on every other Host is a rail row, and what it
  // gets from an Event is that it has changed.
  if (f.host !== host || f.session !== session) {
    redrawRail();
    return;
  }
  apply(f);
  refold();
  redrawRail();
});

// rail is the nav the server drew. The browser redraws it when the merged stream
// says something changed, and it asks the Hub for the answer rather than folding
// another Session's State out of the Events it happens to have seen: the browser
// holds no history for a Session it is not drawing, and a fold over the tail of
// one would be a guess.
const rail = document.getElementById("rail");
let redrawing = false;
let again = false;

async function redrawRail() {
  // One redraw at a time, and one more if the stream spoke while it ran. A busy
  // Host would otherwise put a read in flight for every Delta of every message.
  if (redrawing) {
    again = true;
    return;
  }
  redrawing = true;
  try {
    const resp = await fetch(`/rail/${encodeURIComponent(host)}/${encodeURIComponent(session)}`);
    if (resp.ok) drawRail((await resp.json()).rail ?? []);
  } finally {
    redrawing = false;
    if (again) {
      again = false;
      redrawRail();
    }
  }
}

// drawRail replaces the rail's rows, matching page.html's shape element for
// element, the way render() matches its rows.
function drawRail(entries) {
  const drawn = [];
  for (const e of entries) {
    const row = document.createElement(e.Session ? "a" : "p");
    row.className = "rrow" + (e.On ? " on" : "") + (e.Answering ? "" : " stale") + (e.Session ? "" : " empty");
    row.dataset.host = e.Host;
    if (e.Session) {
      row.dataset.sessionRow = e.Session;
      row.href = `/hosts/${encodeURIComponent(e.Host)}/sessions/${encodeURIComponent(e.Session)}`;
      row.append(node("b", "", e.Cwd));
    }
    row.append(node("span", "meta", e.Host));
    if (e.Session) {
      const state = node("span", "pill", e.SessionState);
      state.dataset.sessionState = e.SessionState;
      row.append(state);
    }
    const answering = node("span", "pill host", e.Answering ? "answering" : "not answering");
    answering.dataset.hostAnswering = String(e.Answering);
    row.append(answering);
    drawn.push(row);
  }
  rail.replaceChildren(...drawn);
}

// apply puts one Event on the page, replacing the row it already had rather than
// doubling it, so a replayed Event costs a redrawn row and nothing else. A row is
// put where its Sequence Number belongs, because a refetch that lands behind a
// live Event would otherwise draw the transcript out of order.
function apply(f) {
  const el = render(f.seq, f.kind, draw(f.kind, f.payload));
  const old = rows.get(f.seq);
  if (old) {
    old.replaceWith(el);
  } else {
    const i = at(f.seq);
    order.splice(i, 0, f.seq);
    if (i === order.length - 1) list.append(el);
    else rows.get(order[i + 1]).before(el);
  }
  events.set(f.seq, { kind: f.kind, payload: f.payload });
  rows.set(f.seq, el);
  remember(f.seq, el);
}

// at is where one Sequence Number belongs in order. Events arrive in order almost
// always, so the scan runs from the end and stops at once.
function at(seq) {
  let i = order.length;
  while (i > 0 && order[i - 1] > seq) i--;
  return i;
}

// refold derives the Session State from the Events this page holds, which is the
// Client folding rather than asking the Hub what the Session is doing.

function refold() {
  const view = foldSession(order.map((seq) => events.get(seq)));
  stateLine.dataset.state = view.state;
  stateLine.textContent = view.reason ? `${view.state} ${view.reason}` : view.state;
}

// load reads this Session whole and applies it. Only a resync calls it: the first
// paint carries its own Events, so nothing is fetched to draw a page that is
// already drawn.
//
// It pages until a read answers with nothing rather than until one answers short,
// so a Host that serves smaller pages than this asks for costs a round trip and
// never a lost Event.
async function load(mine) {
  const path = `/v1/hosts/${encodeURIComponent(host)}/sessions/${encodeURIComponent(session)}/events`;
  for (let after = 0; mine === generation; ) {
    const resp = await fetch(`${path}?after=${after}`);
    // A read that failed leaves the page holding what it had. Answering a resync
    // with a blank transcript would read as a Session with no Events in it.
    if (!resp.ok) return;
    const body = await resp.json();
    const page = body.events ?? [];
    if (mine !== generation) return;
    for (const e of page) apply(e);
    refold();
    if (page.length === 0) return;
    after = page[page.length - 1].seq;
  }
}

stream.addEventListener("delta", (frame) => {
  const f = JSON.parse(frame.data);
  if (f.host !== host) return;
  // A Delta carries no Session id, so the row it belongs to is the answer: a
  // Sequence Number this page has no row for is another Session's message.
  const text = rows.get(f.seq)?.querySelector(".text");
  if (!text) return;
  const next = deltaText(text.textContent, held.get(f.seq), f);
  // Appending touches only the new text, which is what keeps a message arriving
  // in a thousand Deltas from being copied whole a thousand times.
  if (next.append !== undefined) text.append(next.append);
  else text.textContent = next.text;
  held.set(f.seq, next.held);
});

// A resync says this page's Cursor is outside the log, or that the log it came
// from has been replaced. The answer is to discard what this page holds for that
// Host and refetch it. The stream stays open, so every other Host keeps streaming
// through it.
stream.addEventListener("resync", (frame) => {
  const f = JSON.parse(frame.data);
  if (f.host && f.host !== host) return;
  generation++;
  rows.clear();
  held.clear();
  events.clear();
  order.length = 0;
  list.replaceChildren();
  load(generation);
});

// A host frame is the Hub's view of one Host. A Host that is not Ready is never
// hidden: the page says so and goes on showing what it last knew.
stream.addEventListener("host", (frame) => {
  const f = JSON.parse(frame.data);
  if (f.host !== host) return;
  document.body.dataset.hostState = f.state;
  document.body.dataset.hostCause = f.cause ?? "";
});

// A vendors frame carries a Host's Vendor catalogue and its reachability. This
// page is one Session and draws no Vendor row, so there is nothing here to do
// with one. The Hosts view is what reads it.
stream.addEventListener("vendors", () => {});

// render builds one row, matching page.html's shape element for element.
function render(seq, kind, r) {
  const el = document.createElement("li");
  el.className = r.inset ? "row inset" : "row";
  el.dataset.seq = seq;
  el.dataset.kind = kind;
  if (r.tone) el.dataset.tone = r.tone;
  el.append(node("p", "title", r.title));
  if (r.text || r.appendable) el.append(node("pre", "text", r.text));
  if (r.detail) el.append(node("p", "detail", r.detail));
  return el;
}

function node(tag, className, text) {
  const el = document.createElement(tag);
  el.className = className;
  el.textContent = text ?? "";
  return el;
}
