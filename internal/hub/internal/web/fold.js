// The Client's fold. It is session.Fold written again in JS, because the Client
// applies live Events itself rather than asking the Hub what a Session is doing.
//
// This is the only duplicated logic in the design. The two are kept honest by one
// file, internal/session/testdata/fold.json, which they are both tested against.
// A case added there is a case both have to answer, and a Kind added there that
// FOLD_KINDS does not name fails the JS test on its own.

// FOLD_KINDS is every Event Kind this fold reads. It is declared rather than
// implied by the switch, so a fixture that gains a Kind nobody here handles is a
// failure rather than a silent pass: every Kind not in this list changes nothing,
// and that has to be a decision somebody made.
const FOLD_KINDS = [
  "SessionEnded",
  "SessionReady",
  "PromptSubmitted",
  "PromptCompleted",
  "ApprovalRequested",
  "ApprovalDecided",
  "ToolCallRequested",
  "ToolCallEnded",
];

// FOLD_IGNORES is every other Kind, named for the same reason: a Kind that
// changes no state is a thing this fold was told about and decided to pass over.
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
  let ready = false;
  let prompting = false;
  let held = [];
  let calls = [];

  for (const e of events) {
    const p = e.payload ?? {};
    switch (e.kind) {
      case "SessionEnded":
        // Terminal and last. Nothing after it is read, and nothing is left open.
        return { state: "Ended", reason: p.reason ?? "", held: [], calls: [] };
      case "SessionReady":
        ready = true;
        break;
      case "PromptSubmitted":
        prompting = true;
        break;
      case "PromptCompleted":
        prompting = false;
        break;
      case "ApprovalRequested":
        held.push(p.toolCallId);
        break;
      case "ApprovalDecided":
        held = held.filter((id) => id !== p.toolCallId);
        break;
      case "ToolCallRequested":
        calls.push(p.toolCallId);
        break;
      case "ToolCallEnded":
        calls = calls.filter((id) => id !== p.toolCallId);
        break;
    }
  }

  let state = "Starting";
  if (held.length > 0) state = "Asking";
  else if (prompting) state = "Working";
  else if (ready) state = "Idle";
  return { state, reason: "", held, calls };
}
