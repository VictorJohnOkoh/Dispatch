// The live half of the Hosts view. The server drew one card per configured Host;
// this fills the Vendor rows from the stream and moves a card when its Host State
// changes.
//
// The Vendor row is never drawn on the server, and that is the point. A Resident
// list is pushed rather than fetched because it is worthless when old, and the
// same frame carries whether the Vendor answered, so a Vendor that stops
// answering empties its row rather than leaving a remembered list behind.

// The page's cards and their Vendor rows, read once from the page by the two
// selectors this file holds. Neither is built from a Host id: a selector made
// from data is a selector an id can break, and one that throws stops every frame
// after it.
const rows = byHost("[data-vendors]", (el) => el.dataset.vendors);
const cards = byHost("[data-host]", (el) => el.dataset.host);

function byHost(selector, id) {
  const found = new Map();
  for (const el of document.querySelectorAll(selector)) found.set(id(el), el);
  return found;
}

// The stream resumes where the page was drawn, so the Hosts view is sent what
// happens next rather than every Event every Host has ever written.
const stream = new EventSource("/v1/events?from=" + encodeURIComponent(at()));

function at() {
  const cursor = document.querySelectorAll("[data-cursor]")[0];
  return cursor ? cursor.dataset.cursor : "";
}

stream.addEventListener("vendors", (frame) => {
  const f = JSON.parse(frame.data);
  const row = rows.get(f.host);
  if (!row) return;
  // Precedence: Host State, then the HTTP status, then an Event, then the
  // operational log. A user looking at a Down Host is not also told that its
  // Vendor stopped answering, because the Vendor not answering is what a Host
  // that is not there looks like from here.
  if (down(f.host)) return;

  const drawn = [];
  for (const v of f.vendors ?? []) {
    const line = document.createElement("li");
    line.dataset.vendor = v.kind;
    line.dataset.reachable = String(!!v.reachable);
    line.append(node("b", "", `${v.kind} ${v.base}`));
    if (!v.reachable) {
      // Not a Host State and not an Event. The row says the Vendor is not
      // answering and holds no Models, because a list nobody can confirm is a
      // list that has stopped being true.
      line.append(node("span", "meta", "not answering"));
    } else if ((v.resident ?? []).length === 0) {
      line.append(node("span", "meta", "nothing resident"));
    } else {
      for (const m of v.resident) line.append(node("span", "pill", m.modelId));
    }
    drawn.push(line);
  }
  if (drawn.length === 0) drawn.push(node("li", "meta", "this Host serves no Vendors"));
  row.replaceChildren(...drawn);
});

// A host frame is the Hub's own, and the only Frame it originates. Nothing sends
// one yet: the Hub starts reporting Host State when it tracks presence, and what
// each of the four looks like is this view's to decide before then.
//
// Connecting keeps its content at full strength with a mark, because the blink is
// usually over before it becomes Down. Only Down dims and stamps.
stream.addEventListener("host", (frame) => {
  const f = JSON.parse(frame.data);
  const card = cards.get(f.host);
  if (!card) return;
  card.dataset.hostState = f.state;
  const pill = card.querySelector(".pill");
  if (pill) {
    pill.dataset.hostState = f.state;
    pill.textContent = f.cause ? `${f.state} ${f.cause}` : f.state;
  }
  // A Host that has gone says one thing about itself, not two. Whatever its
  // Vendors were last reported as is what a machine nobody can reach knows about
  // them, which is nothing.
  const row = rows.get(f.host);
  if (row && down(f.host)) {
    row.replaceChildren(node("li", "meta", "this Host is not answering, so what it serves is not known"));
  }
});

function down(host) {
  return cards.get(host)?.dataset.hostState === "Down";
}

function node(tag, className, text) {
  const el = document.createElement(tag);
  el.className = className;
  el.textContent = text ?? "";
  return el;
}
