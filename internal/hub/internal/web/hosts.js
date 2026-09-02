// The live half of the Hosts view. The server drew one card per configured Host;
// this fills the Vendor rows from the stream and moves a card when its Host State
// changes.
//
// The Vendor row is never drawn on the server, and that is the point. A Resident
// list is pushed rather than fetched because it is worthless when old, and the
// same frame carries whether the Vendor answered, so a Vendor that stops
// answering empties its row rather than leaving a remembered list behind.

const stream = new EventSource("/v1/events");

stream.addEventListener("vendors", (frame) => {
  const f = JSON.parse(frame.data);
  const row = document.querySelector(`[data-vendors="${cssEscape(f.host)}"]`);
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

// A host frame is the Hub's own, and the only Frame it originates. Connecting
// keeps its content at full strength with a mark, because the blink is usually
// over before it becomes Down; only Down dims and stamps.
stream.addEventListener("host", (frame) => {
  const f = JSON.parse(frame.data);
  const card = document.querySelector(`[data-host="${cssEscape(f.host)}"]`);
  if (!card) return;
  card.dataset.hostState = f.state;
  const pill = card.querySelector(".pill");
  if (pill) {
    pill.dataset.hostState = f.state;
    pill.textContent = f.cause ? `${f.state} ${f.cause}` : f.state;
  }
});

function node(tag, className, text) {
  const el = document.createElement(tag);
  el.className = className;
  el.textContent = text ?? "";
  return el;
}

// cssEscape keeps a Host id out of the selector's grammar. Host ids are narrow,
// and a selector built from one is still a selector built from data.
function cssEscape(value) {
  return String(value).replace(/["\\]/g, "\\$&");
}
