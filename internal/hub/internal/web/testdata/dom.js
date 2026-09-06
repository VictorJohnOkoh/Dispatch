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
// The Host half of the pair, as page.html leaves it: a pill that already carries a
// state, so a page.js that only wrote the text would still read as Connecting.
const hostStateElement = new El("span");
hostStateElement.className = "pill host";
hostStateElement.dataset.hostState = "Connecting";
hostStateElement.textContent = "Connecting";
const hostCauseElement = new El("span");
const hostMarkElement = new El("span");
const staleElement = new El("span");
// What is serving the Session, as page.html leaves it: the Vendor named by the
// address the Event carried, which a vendors frame swaps for the Vendor's kind.
const servingHarnessElement = new El("span");
const servingModelElement = new El("span");
const servingVendorElement = new El("span");
servingVendorElement.dataset.base = "http://127.0.0.1:11434";
servingVendorElement.textContent = "http://127.0.0.1:11434";

// The three commands, as page.html leaves them. The prompt box holds the text and
// the composer carries the line that says why one did not land.
const promptElement = new El("textarea");
const sendElement = new El("button");
sendElement.dataset.mode = "send";
const boxElement = new El("div");
boxElement.append(promptElement, sendElement);
const sendRowElement = new El("div");
sendRowElement.append(boxElement);
const pairElement = new El("p");
const stopElement = new El("button");
const interruptElement = new El("button");
pairElement.append(interruptElement, stopElement);

// The heading, which is the Session's name and the one place it is renamed. The
// server drew the work directory into it.
const nameElement = new El("h1");
nameElement.dataset.drawn = "w";
nameElement.textContent = "w";

// The toast rack, empty until a question from another Session raises one.
const toastRack = new El("div");

// Questions that were open when page.html was drawn.
const approvals = new El("script");
approvals.textContent = "[]";

const body = new El("body");

// The whole page, so a sweep for the elements that print a Session name has
// somewhere to start.
const page = new El("div");
page.append(nameElement, railNav);

globalThis.document = {
  getElementById: (id) => ({
    transcript,
    state: stateElement,
    events: embedded,
    approvals,
    rail: railNav,
    toasts: toastRack,
    name: nameElement,
    "host-state": hostStateElement,
    "host-cause": hostCauseElement,
    "host-mark": hostMarkElement,
    stale: staleElement,
    "serving-harness": servingHarnessElement,
    "serving-model": servingModelElement,
    "serving-vendor": servingVendorElement,
    prompt: promptElement,
    composer: sendRowElement,
    send: sendElement,
    stop: stopElement,
    interrupt: interruptElement,
  })[id] ?? null,
  createElement: (tag) => new El(tag),
  querySelectorAll: (selector) => page.querySelectorAll(selector),
  body,
};

// The window the transcript is read in. The page follows the text as it arrives,
// so a test can put the reader at the end or part way up and see whether it does.
// The page is as tall as the transcript is long, so text arriving makes it grow
// the way it does in a browser.
document.documentElement = {
  get scrollHeight() {
    return 400 + transcript.textContent.length * 10;
  },
};
globalThis.window = {
  innerHeight: 400,
  scrollY: 0,
  scrollTo: (_, y) => {
    globalThis.window.scrollY = y - globalThis.window.innerHeight;
  },
};

// The browser's own store, which is where a rename lives and the only place it
// ever lives.
const kept = new Map();
globalThis.localStorage = {
  getItem: (key) => kept.get(key) ?? null,
  setItem: (key, value) => kept.set(key, String(value)),
  removeItem: (key) => kept.delete(key),
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
globalThis.posted = [];
globalThis.fetch = async (url, options) => {
  if (options?.method === "POST") {
    posted.push({ url, body: options.body });
    const answer = globalThis.postAnswer ?? {};
    if (answer === "unreachable") throw new Error("no route to that Host");
    // The test's own answer wins, so one that carries a Refusal body keeps it.
    return { ok: true, status: 202, json: async () => ({}), ...answer };
  }
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
  servingHarnessElement,
  servingModelElement,
  servingVendorElement,
  embedded,
  rail: railNav,
  toasts: toastRack,
  name: nameElement,
  kept,
  body,
  row,
  promptBox: promptElement,
  sendButton: sendElement,
  sendRow: sendRowElement,
  stopButton: stopElement,
  interruptButton: interruptElement,
  pair: pairElement,
};
