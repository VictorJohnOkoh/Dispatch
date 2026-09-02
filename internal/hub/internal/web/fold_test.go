package web

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/session"
)

// The fold exists twice, so it is tested twice against one file. This is the JS
// half: it runs fold.js under node against internal/session/testdata/fold.json,
// the same cases session.Fold is tested with, and checks the two agree.
//
// A machine with no node skips rather than fails. The Go half of this pair runs
// everywhere and the fixture is checked in, so a skip here loses the agreement
// check and nothing else.

// foldFixture is the shared file, and the two folds' whole specification.
const foldFixture = "../../../session/testdata/fold.json"

type foldCase struct {
	Name   string          `json:"name"`
	Events []recordedEvent `json:"events"`
	State  string          `json:"state"`
	Reason string          `json:"reason"`
}

type recordedEvent struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

func readFixture(t *testing.T) []foldCase {
	t.Helper()
	raw, err := os.ReadFile(foldFixture)
	if err != nil {
		t.Fatalf("the shared fixture: %v", err)
	}
	var file struct {
		Cases []foldCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("the shared fixture: %v", err)
	}
	if len(file.Cases) == 0 {
		t.Fatal("the shared fixture holds no cases")
	}
	return file.Cases
}

// The two folds agree on every case in the file. A case added there is answered
// by both or by neither.
func TestTheJSFoldAgreesWithSessionFold(t *testing.T) {
	node := findNode(t)
	cases := readFixture(t)

	var got []struct {
		State  string `json:"state"`
		Reason string `json:"reason"`
	}
	run(t, node, driver, &got)
	if len(got) != len(cases) {
		t.Fatalf("the JS fold answered %d cases and the file holds %d", len(got), len(cases))
	}

	for i, c := range cases {
		if got[i].State != c.State || got[i].Reason != c.Reason {
			t.Errorf("%s: the JS fold says %s %q and the file says %s %q",
				c.Name, got[i].State, got[i].Reason, c.State, c.Reason)
		}
		// And the Go fold, read from the same file rather than trusted to have been
		// checked elsewhere, because the point of this test is that the two agree.
		state, reason := session.Fold(decode(t, c))
		if state.String() != c.State || string(reason) != c.Reason {
			t.Errorf("%s: session.Fold says %s %q and the file says %s %q",
				c.Name, state, reason, c.State, c.Reason)
		}
	}
}

// Every Kind the file uses is one the JS fold was told about, either as a Kind it
// reads or as one it passes over on purpose. A fixture that gains a Kind nobody
// handled would otherwise pass by doing nothing.
func TestTheJSFoldNamesEveryKindTheFixtureUses(t *testing.T) {
	node := findNode(t)

	var known struct {
		Reads   []string `json:"reads"`
		Ignores []string `json:"ignores"`
	}
	run(t, node, kindsDriver, &known)

	for _, c := range readFixture(t) {
		for _, e := range c.Events {
			if !slices.Contains(known.Reads, e.Kind) && !slices.Contains(known.Ignores, e.Kind) {
				t.Errorf("%s: the fixture uses %s and the JS fold has never heard of it", c.Name, e.Kind)
			}
		}
	}
}

// decode turns one fixture case into the Events session.Fold takes.
func decode(t *testing.T, c foldCase) []event.Event {
	t.Helper()
	out := make([]event.Event, 0, len(c.Events))
	for _, e := range c.Events {
		decoded, err := event.Decode(0, "s-7f3a2c", 0, event.Kind(e.Kind), e.Payload)
		if err != nil {
			t.Fatalf("%s: %s: %v", c.Name, e.Kind, err)
		}
		out = append(out, decoded)
	}
	return out
}

// driver folds every case in the file and prints the answers as JSON, in order.
const driver = `
const fixture = JSON.parse(fs.readFileSync(process.env.FIXTURE, "utf8"));
const out = fixture.cases.map((c) => {
  const { state, reason } = foldSession(c.events);
  return { state, reason };
});
console.log(JSON.stringify(out));
`

// kindsDriver prints the Kinds the fold was told about.
const kindsDriver = `console.log(JSON.stringify({reads: FOLD_KINDS, ignores: FOLD_IGNORES}));`

// run evaluates fold.js and then one driver under node, and decodes what it
// printed. fold.js is loaded as source rather than imported, because the browser
// loads it as a plain script and this test has to run the same file the page does.
func run(t *testing.T, node, script string, into any) {
	t.Helper()
	source, err := os.ReadFile("fold.js")
	if err != nil {
		t.Fatalf("fold.js: %v", err)
	}
	program := "const fs = require('fs');\n" + string(source) + "\n" + script

	cmd := exec.Command(node, "-e", program)
	cmd.Env = append(os.Environ(), "FIXTURE="+filepath.FromSlash(foldFixture))
	said, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, said)
	}
	if err := json.Unmarshal(said, into); err != nil {
		t.Fatalf("node said %q: %v", said, err)
	}
}

func findNode(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on this machine, so the JS half of the fold is not checked here")
	}
	return node
}
