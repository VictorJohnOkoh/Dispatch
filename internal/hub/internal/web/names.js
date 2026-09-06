// The name a user gives a Session, kept in this browser and nowhere else. No Host
// holds it, no Event carries it, and another browser sees the work directory the
// server drew. See names.go for why.
//
// Every page that prints a Session name marks the element with the Host and the
// Session, so one file serves all three of them.

const nameStore = "dispatch.name.";

// givenName is what to print: the name typed here, or the one the server drew.
function givenName(host, session, drawn) {
  try {
    return localStorage.getItem(nameStore + host + "/" + session) || drawn;
  } catch {
    // A browser that refuses storage is a browser with no renames, not a broken
    // page.
    return drawn;
  }
}

// renameSession keeps one name, and an empty one gives the Session its work
// directory back.
function renameSession(host, session, given) {
  try {
    if (given) localStorage.setItem(nameStore + host + "/" + session, given);
    else localStorage.removeItem(nameStore + host + "/" + session);
  } catch {}
}

// drawNames puts the kept names on a page the server drew with the directories.
function drawNames(root) {
  for (const el of (root ?? document).querySelectorAll("[data-name-host]")) {
    el.textContent = givenName(el.dataset.nameHost, el.dataset.nameSession, el.textContent);
  }
}

drawNames();
