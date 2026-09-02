// Enough of a browser to run page.js. It is the small surface page.js actually
// touches and nothing else, so what a test drives here is the real file rather
// than a copy of it.
//
// A test that needed more than this would be a test of a browser.

class El {
  constructor(tag) {
    this.tag = tag;
    this.className = "";
    this.dataset = {};
    this.children = [];
    this.parent = null;
    this._text = "";
  }

  get textContent() {
    if (this.children.length === 0) return this._text;
    return this.children.map((c) => c.textContent).join("");
  }

  set textContent(v) {
    this.children = [];
    this._text = String(v);
  }

  append(...parts) {
    for (const p of parts) {
      if (typeof p === "string") this._text += p;
      else {
        p.parent = this;
        this.children.push(p);
      }
    }
  }

  replaceChildren(...parts) {
    this.children = [];
    this._text = "";
    this.append(...parts);
  }

  replaceWith(el) {
    const at = this.parent.children.indexOf(this);
    el.parent = this.parent;
    this.parent.children[at] = el;
  }

  // querySelector answers the one selector page.js asks for, which is a class.
  querySelector(selector) {
    const want = selector.replace(".", "");
    for (const c of this.children) {
      if (c.className === want) return c;
      const found = c.querySelector(selector);
      if (found) return found;
    }
    return null;
  }
}

// The page as page.html leaves it: a transcript with the Host, the Session and
// the Cursor on it, a state line, and a body to stamp the Host State on.
const transcript = new El("ol");
transcript.dataset.host = "desk";
transcript.dataset.session = "s-1";
transcript.dataset.cursor = "desk:0";

const stateLine = new El("p");
const body = new El("body");

globalThis.document = {
  getElementById: (id) => (id === "transcript" ? transcript : id === "state" ? stateLine : null),
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

// fetch answers whatever the test put in served, and records what was asked for.
globalThis.served = [];
globalThis.fetched = [];
globalThis.fetch = async (url) => {
  fetched.push(url);
  const body = served.shift() ?? { events: [] };
  return { ok: true, json: async () => body };
};

globalThis.dom = { transcript, stateLine, body };
