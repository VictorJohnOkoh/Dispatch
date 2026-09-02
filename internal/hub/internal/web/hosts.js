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

const stream = new EventSource("/v1/events?from=" + encodeURIComponent(at()));

function at() {
  return drawnPage ? drawnPage.dataset.cursor : "";
}

// trueAt is when each card's content was last true, which is what a Stale stamp
// carries. Both times it can hold come from the Hub: the page was drawn at one,
// and a host frame says when the Hub last heard from that Host. The browser's own
// clock is never one of them, because it is not the clock the other stamps on the
// page were made on.
const trueAt = new Map();
for (const [host, card] of cards) {
  if (card.dataset.hostState === "Ready" && drawnPage) trueAt.set(host, drawnPage.dataset.drawn);
}

stream.addEventListener("vendors", (frame) => {
  const f = JSON.parse(frame.data);
  const row = rows.get(f.host);
  if (!row) return;

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

function node(tag, className, text) {
  const el = document.createElement(tag);
  el.className = className;
  el.textContent = text ?? "";
  return el;
}
