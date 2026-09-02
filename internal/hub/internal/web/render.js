// The row every Event draws as, which is render.go's draw() written again in JS.
// The Hub draws the first paint on the server and the browser draws everything
// after it, so the two must agree, and they are named the same for that reason.
//
// Nothing here touches the page. It is the half of the Client that can be tested
// without one.

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

// deltaText is one Delta applied to the text a row already holds. It answers
// either the whole text the row should hold now or the text to append to it, and
// how long the row is once that is done, which is what the next Delta counts on.
//
// A Delta says how much text stood before it. One that carries on from exactly
// that much appends, which is every Delta of a message that nothing went wrong
// with, and touches only the new text. One that does not is a Delta this page
// dropped, and the answer is to rewrite from where this one says the text stood.
// The final Delta carries the whole text and replaces it, so a page that dropped
// one repairs itself at the end whatever else happened.
function deltaText(current, held, d) {
  if (d.final) return { text: d.text, held: d.text.length };
  // The whole string is not built here, because building it is the copy this
  // answer exists to avoid: the caller appends the new text and nothing else.
  if (d.n === held) return { append: d.text, held: held + d.text.length };
  const text = current.slice(0, d.n) + d.text;
  return { text, held: text.length };
}
