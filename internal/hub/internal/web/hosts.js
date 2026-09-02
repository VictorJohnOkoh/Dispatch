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

// What the Hub stamped the page with: the Cursor it was drawn at, and when. The
// stream resumes from the Cursor, so the Hosts view is sent what happens next
// rather than every Event every Host has ever written.
const drawnPage = document.querySelectorAll("[data-cursor]")[0];

const stream = new EventSource("/v1/events?from=" + encodeURIComponent(stamped("cursor")));

// stamped is one of the things the Hub wrote on the page: the Cursor it was drawn
// at, when that was, and the protocol version it requires.
function stamped(name) {
  return drawnPage ? drawnPage.dataset[name] : "";
}

// trueAt is when each card's content was last true, which is what a Stale stamp
// carries. Both times it can hold come from the Hub: the page was drawn at one,
// and a host frame says when the Hub last heard from that Host. The browser's own
// clock is never one of them, because it is not the clock the other stamps on the
// page were made on.
const trueAt = new Map();
for (const [host, card] of cards) {
  if (card.dataset.hostState === "Ready" && drawnPage) trueAt.set(host, stamped("drawn"));
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

// A host frame is the Hub's own, and the only Frame it originates. What each of
// the four states looks like is this view's to decide.
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
    pill.textContent = says(f);
  }
  // A Host that has gone says one thing about itself, not two. Whatever its
  // Vendors were last reported as is what a machine nobody can reach knows about
  // them, which is nothing.
  const row = rows.get(f.host);
  if (row && down(f.host)) {
    row.replaceChildren(node("li", "meta", "this Host is not answering, so what it serves is not known"));
  }
  if (f.since) trueAt.set(f.host, f.since);
  if (f.state !== "Down") unstamp(card);
  else if (trueAt.has(f.host)) stamp(card, trueAt.get(f.host));
});

// stamp says how old this card is, and unstamp takes the saying away when the card
// is current again. A Host that goes Down while this page is open is the same Stale
// case the server draws on a page opened after it went down, and it reads the same.
//
// A Down Host this page has no time for keeps whatever stamp the server gave it,
// because an empty stamp would be a card that claims to be current.
function stamp(card, since) {
  const had = card.querySelector(".stamp");
  if (had) {
    had.textContent = `last answered at ${since}`;
    return;
  }
  const who = card.querySelector(".who");
  if (who) who.append(node("span", "stamp", `last answered at ${since}`));
}

function unstamp(card) {
  const had = card.querySelector(".stamp");
  if (had) had.remove();
}

// says is what the pill reads. An Incompatible Host names both versions, because
// the user fixes it by updating one of the two machines and has to know which. A
// Host that named none still leaves the Hub's half, which is the half that says
// what to update to.
function says(f) {
  if (f.state !== "Incompatible") return f.cause ? `${f.state} ${f.cause}` : f.state;
  const host = f.speaks ? `this Host speaks ${f.speaks.join(", ")}` : "this Host speaks another";
  return `Incompatible: this Hub speaks ${stamped("protocol")}, ${host}`;
}

function down(host) {
  return cards.get(host)?.dataset.hostState === "Down";
}

function node(tag, className, text) {
  const el = document.createElement(tag);
  el.className = className;
  el.textContent = text ?? "";
  return el;
}
