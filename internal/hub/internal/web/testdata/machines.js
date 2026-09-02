// The Hosts view as hosts.html leaves it, over the element in el.js: one card per
// configured Host, each with the Vendor row the server never fills.

const drawn = new Map();

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
  row.append(part("li", "meta waiting", "waiting for this Host's Vendors"));
  section.append(row);

  drawn.set(host, { section, pill, row });
  return section;
}

function part(tag, className, text) {
  const el = new El(tag);
  el.className = className;
  el.textContent = text;
  return el;
}

makeCard("desk");
makeCard("attic");

// querySelectorAll answers the two constant selectors hosts.js reads its page
// with. Neither is built from data, so neither can be broken by an id.
globalThis.document = {
  querySelectorAll: (selector) => {
    if (selector === "[data-vendors]") return [...drawn.values()].map((c) => c.row);
    if (selector === "[data-host]") return [...drawn.values()].map((c) => c.section);
    return [];
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
// The test reads the page through this. It is not called cards, because hosts.js
// declares one of its own and the two would shadow.
globalThis.page = drawn;
