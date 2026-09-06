package web

import "testing"

// A Session's name starts as the last part of its work directory, because that is
// the part a person recognises. A Session with no directory keeps its id, which is
// a name nobody chose and better than an empty heading.
func TestASessionIsNamedAfterItsWorkDirectory(t *testing.T) {
	cases := []struct {
		cwd  string
		id   string
		want string
	}{
		{"/home/victor/capstone", "s-1", "capstone"},
		{"/home/victor/capstone/", "s-1", "capstone"},
		{`C:\Users\Victor\Capstone`, "s-1", "Capstone"},
		{"capstone", "s-1", "capstone"},
		{"/", "s-1", "s-1"},
		{"", "s-1", "s-1"},
	}
	for _, c := range cases {
		if got := sessionName(c.cwd, c.id); got != c.want {
			t.Errorf("sessionName(%q, %q) = %q, want %q", c.cwd, c.id, got, c.want)
		}
	}
}

// The page draws the name the rail holds. A Host that did not answer this read has
// no entry there, and the id is the only name left.
func TestAPageForASilentHostFallsBackToTheID(t *testing.T) {
	rail := []entry{{Host: "desk", Session: "s-1", Name: "capstone"}}
	if got := named(rail, "desk", "s-1"); got != "capstone" {
		t.Errorf("named = %q", got)
	}
	if got := named(rail, "attic", "s-9"); got != "s-9" {
		t.Errorf("named = %q, want the id", got)
	}
}
