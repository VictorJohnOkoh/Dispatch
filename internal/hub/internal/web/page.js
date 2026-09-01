// The live half of the page. The Hub drew the transcript already, so this file
// only applies what arrives after that paint: an Event becomes a row, a Delta adds
// text to a row that is already there.
//
// draw() below is page.js's copy of render.go's draw(). The two must agree, and
// they are next to each other for that reason.

const list = document.getElementById("transcript");
const host = list.dataset.host;
const session = list.dataset.session;

// rows is every row on the page by Sequence Number, so a Delta finds its message
// without searching, and a replayed Event replaces its row instead of doubling it.
const rows = new Map();
for (const el of list.children) rows.set(Number(el.dataset.seq), el);

// The stream resumes at the Cursor the first paint was drawn at. It goes in the
// query because an EventSource sets no headers; on every reconnect after this the
// browser sends Last-Event-ID itself, and the Hub prefers that.
const url = "/v1/events?from=" + encodeURIComponent(list.dataset.cursor);
const stream = new EventSource(url);

stream.addEventListener("event", (frame) => {
  const f = JSON.parse(frame.data);
  if (f.host !== host || f.session !== session) return;
  const el = render(f.seq, f.kind, draw(f.kind, f.payload));
  const old = rows.get(f.seq);
  if (old) old.replaceWith(el);
  else list.append(el);
  rows.set(f.seq, el);
});

stream.addEventListener("delta", (frame) => {
  const f = JSON.parse(frame.data);
  if (f.host !== host) return;
  // A Delta carries no Session id, so the row it belongs to is the answer: a
  // Sequence Number this page has no row for is another Session's message.
  const text = rows.get(f.seq)?.querySelector(".text");
  if (!text) return;
  // The final Delta carries the whole text and replaces, so a page that dropped
  // one repairs itself here.
  text.textContent = f.final ? f.text : text.textContent.slice(0, f.n) + f.text;
});

// A resync says this page's Cursor is outside the log. The answer is to discard
// what it holds and refetch, and a reload is both.
stream.addEventListener("resync", () => location.reload());

// render builds one row, matching page.html's shape element for element.
function render(seq, kind, r) {
  const el = document.createElement("li");
  el.className = "row";
  el.dataset.seq = seq;
  el.dataset.kind = kind;
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

// draw is render.go's draw(). A Kind this build has never heard of falls to the
// default and draws as a neutral row carrying its payload.
function draw(kind, p) {
  switch (kind) {
    case "SessionStarted":
      return { title: "Session started", detail: `${p.harness} on ${p.model} via ${p.vendor}, in ${p.cwd}` };
    case "SessionReady":
      return { title: "Session ready", detail: p.model };
    case "ApprovalPolicySet":
      return { title: `Approval policy, set by ${p.setBy}`, detail: policyLine(p.policy) };
    case "PromptSubmitted":
      return { title: "Prompt", text: p.text };
    case "Reasoning":
      return { title: "Reasoning", text: p.text, appendable: true };
    case "AssistantMessage":
      return { title: "Assistant", text: p.text, appendable: true };
    case "ToolCallRequested":
      // A call with no arguments carries none, the way render.go's does. Spelling
      // them "null" would read as an argument whose value is null.
      return { title: `Tool call: ${p.name}`, detail: `${p.title} ${p.args ? JSON.stringify(p.args) : ""}`.trim() };
    case "ApprovalRequested":
      return { title: `Approval requested: ${p.title}`, detail: p.detail };
    case "ApprovalDecided":
      return { title: `Approval ${p.decision}`, detail: `decided by ${p.by}` };
    case "ToolCallEnded":
      return { title: `Tool call ${p.outcome}`, text: p.content };
    case "PromptCompleted":
      return {
        title: `Prompt completed: ${p.stopReason}`,
        detail: `${p.usage.input} in, ${p.usage.output} out, ${p.usage.total} total`,
      };
    case "Error":
      return { title: `Error: ${p.code}`, detail: p.message };
    case "SessionEnded":
      return { title: `Session ended: ${p.reason}` };
    case "HubAttached":
      return { title: "The Hub attached" };
    case "HubDetached":
      return { title: "The Hub detached" };
    case "DaemonStarted":
      return { title: "The Daemon started" };
    default:
      return { title: kind, detail: JSON.stringify(p) };
  }
}

// policyLine spells all five slots, because the Approval Policy is always all five
// set and a line naming three of them would read as the other two being off.
function policyLine(policy) {
  return ["read", "edit", "execute", "fetch", "other"].map((k) => `${k} ${policy[k]}`).join(", ");
}
