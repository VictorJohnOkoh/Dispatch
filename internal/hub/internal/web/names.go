package web

import "strings"

// A Session has no name. The Daemon gives it an id and a work directory, and the
// id is the one the whole protocol uses. The name on screen is the Client's own:
// the last part of the work directory to start with, and whatever the user types
// over it after that.
//
// A rename never leaves the browser. Nothing on a Host holds it, no Event carries
// it, and a second browser sees the work directory again. That keeps the name a
// way to read the list and not a second identity for a Session.

// sessionName is what one Session is called before the browser renames it.
func sessionName(cwd, id string) string {
	dir := strings.TrimRight(cwd, `/\`)
	if cut := strings.LastIndexAny(dir, `/\`); cut >= 0 {
		dir = dir[cut+1:]
	}
	if dir == "" {
		return id
	}
	return dir
}

// named is what the rail calls one Session. A Host that did not answer this read
// has no entry there, and the id is the only name left to draw.
func named(rail []entry, host, id string) string {
	for _, e := range rail {
		if e.Host == host && e.Session == id {
			return e.Name
		}
	}
	return id
}
