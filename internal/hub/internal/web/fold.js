// The Client's fold. It is session.Fold written again in JS, because the Client
// applies live Events itself rather than asking the Hub what a Session is doing.
//
// This is the only duplicated logic in the design. The two are kept honest by one
// file, internal/session/testdata/fold.json, which they are both tested against.

// FOLD_RULES is what each Kind does to the fold, one entry per Kind that does
// anything. It is a table rather than a switch so that the Kinds this fold handles
// can be read off it: a test asks Object.keys for them, and a Kind that is named
// but does nothing cannot hide in it.
const FOLD_RULES = {
  SessionReady: (v) => {
    v.ready = true;
  },
  PromptSubmitted: (v) => {
    v.prompting = true;
  },
  PromptCompleted: (v) => {
    v.prompting = false;
  },
  ApprovalRequested: (v, p) => v.held.push(p.toolCallId),
  ApprovalDecided: (v, p) => {
    v.held = v.held.filter((id) => id !== p.toolCallId);
  },
  ToolCallRequested: (v, p) => v.calls.push(p.toolCallId),
  ToolCallEnded: (v, p) => {
    v.calls = v.calls.filter((id) => id !== p.toolCallId);
  },
  // SessionEnded is terminal and last, so it stops the fold rather than changing
  // it. Nothing after it is read and nothing is left open.
  SessionEnded: (v, p) => {
    v.ended = p.reason ?? "";
  },
};

// FOLD_IGNORES is every Kind that changes no state. It is written out rather than
// implied, because a Kind that changes nothing is a decision somebody made, and a
// Kind in neither list is one nobody has looked at.
const FOLD_IGNORES = [
  "SessionStarted",
  "ApprovalPolicySet",
  "Reasoning",
  "AssistantMessage",
  "Error",
  "HubAttached",
  "HubDetached",
  "DaemonStarted",
];

// foldSession derives a Session's State from that Session's own Events, in Seq
// order. It answers the end reason with Ended and nothing with any other state.
//
// A list with no SessionStarted folds to Starting, which is what the Session will
// be the moment its first Event lands.
function foldSession(events) {
  const view = { ready: false, prompting: false, held: [], calls: [], ended: null };

  for (const e of events) {
    const rule = FOLD_RULES[e.kind];
    if (!rule) continue;
    rule(view, e.payload ?? {});
    if (view.ended !== null) return { state: "Ended", reason: view.ended, held: [], calls: [] };
  }

  let state = "Starting";
  if (view.held.length > 0) state = "Asking";
  else if (view.prompting) state = "Working";
  else if (view.ready) state = "Idle";
  return { state, reason: "", held: view.held, calls: view.calls };
}
