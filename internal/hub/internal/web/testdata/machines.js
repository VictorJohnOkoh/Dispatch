// Enough of a browser to run hosts.js, and one card per Host the way hosts.html
// leaves them. The El class comes from dom.js, which is loaded first.

const cards = new Map();

function makeCard(host) {
  const section = new El("section");
  section.className = "card";
  section.dataset.host = host;
  section.dataset.hostState = "Ready";

  const pill = new El("span");
  pill.className = "pill";
  pill.dataset.hostState = "Ready";
  pill.textContent = "Ready";
  section.append(pill);

  const row = new El("ul");
  row.className = "vendors";
  row.dataset.vendors = host;
  row.append(node2("li", "meta waiting", "waiting for this Host's Vendors"));
  section.append(row);

  cards.set(host, { section, pill, row });
  return section;
}

function node2(tag, className, text) {
  const el = new El(tag);
  el.className = className;
  el.textContent = text;
  return el;
}

makeCard("desk");
makeCard("attic");

// querySelector answers the two attribute selectors hosts.js builds.
globalThis.document = {
  querySelector: (selector) => {
    const vendors = selector.match(/^\[data-vendors="(.*)"\]$/);
    if (vendors) return cards.get(vendors[1])?.row ?? null;
    const host = selector.match(/^\[data-host="(.*)"\]$/);
    if (host) return cards.get(host[1])?.section ?? null;
    return null;
  },
  createElement: (tag) => new El(tag),
};

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
globalThis.cards = cards;
