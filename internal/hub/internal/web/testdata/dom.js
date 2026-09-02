// The Session page as page.html leaves it, over the element in el.js.

// row is one row as page.html left it, so the adoption loop at the top of page.js
// has something to adopt. That loop is the seam between the server's paint and
// the browser's, and a stub with an empty page never runs it.
function row(seq, kind, title, text) {
  const el = new El("li");
  el.className = "row";
  el.dataset.seq = String(seq);
  el.dataset.kind = kind;
  const t = new El("p");
  t.className = "title";
  t.textContent = title;
  el.append(t);
  if (text !== undefined) {
    const body = new El("pre");
    body.className = "text";
    body.textContent = text;
    el.append(body);
  }
  return el;
}

// The page as page.html leaves it: a transcript with the Host, the Session and
// the Cursor on it, a state line the server already folded, and a body to stamp
// the Host State on.
const transcript = new El("ol");
transcript.dataset.host = "desk";
transcript.dataset.session = "s-1";
transcript.dataset.cursor = "desk:0";

// One row the server already drew, so the adoption loop at the top of page.js has
// something to adopt.
transcript.append(row(1, "SessionStarted", "Session started"));

// The Events the first paint carried, beside the row it drew from them.
const embedded = new El("script");
embedded.textContent = JSON.stringify([
  { seq: 1, kind: "SessionStarted", payload: { harness: "opencode", model: "m", vendor: "ollama", cwd: "/w" } },
]);

const stateElement = new El("p");
stateElement.dataset.sessionState = "Starting";
stateElement.textContent = "Starting";

// The rail's rows, which page.js redraws whole.
const railNav = new El("div");
const hostStateElement = new El("span");
const hostCauseElement = new El("span");
const hostMarkElement = new El("span");
const staleElement = new El("span");
const vendorsElement = new El("ul");

const body = new El("body");

globalThis.document = {
  getElementById: (id) => ({
    transcript,
    state: stateElement,
    events: embedded,
    rail: railNav,
    "host-state": hostStateElement,
    "host-cause": hostCauseElement,
    "host-mark": hostMarkElement,
    stale: staleElement,
    vendors: vendorsElement,
  })[id] ?? null,
  createElement: (tag) => new El(tag),
  body,
};

// EventSource, as one page holds one of. The test dispatches frames into it.
class EventSource {
  constructor(url) {
    this.url = url;
    this.listeners = new Map();
    globalThis.opened = this;
  }
  addEventListener(name, fn) {
    this.listeners.set(name, fn);
  }
  send(name, payload) {
    const fn = this.listeners.get(name);
    if (fn) fn({ data: JSON.stringify(payload) });
  }
}
globalThis.EventSource = EventSource;

// fetch answers the pages the test put in served, keyed by the after= it was
// asked for, and records every URL. A read with no page for it answers the way
// the Hub answers a Session it cannot read.
globalThis.served = new Map();
globalThis.fetched = [];
globalThis.fetch = async (url) => {
  fetched.push(url);
  if (url.startsWith("/rail/")) {
    return { ok: true, json: async () => ({ rail: globalThis.railAnswer ?? [] }) };
  }
  const after = new URL(url, "http://hub").searchParams.get("after") ?? "0";
  const page = served.get(after);
  if (page === undefined) return { ok: true, json: async () => ({ events: [] }) };
  if (page === "refused") return { ok: false, json: async () => ({}) };
  return { ok: true, json: async () => ({ events: page }) };
};

globalThis.dom = {
  transcript,
  stateElement,
  hostStateElement,
  hostCauseElement,
  hostMarkElement,
  staleElement,
  vendorsElement,
  embedded,
  rail: railNav,
  body,
  row,
};
