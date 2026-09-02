package web

import (
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

type renderFixture struct {
	Cases []struct {
		Name    string          `json:"name"`
		Kind    event.Kind      `json:"kind"`
		Payload json.RawMessage `json:"payload"`
		Row     renderedRow     `json:"row"`
	} `json:"cases"`
}

type renderedRow struct {
	Title      string `json:"title"`
	Text       string `json:"text"`
	Detail     string `json:"detail"`
	Appendable bool   `json:"appendable"`
}

func TestGoAndJavaScriptRenderTheSharedFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/render.json")
	if err != nil {
		t.Fatalf("the shared renderer fixture: %v", err)
	}
	var fixture renderFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("the shared renderer fixture: %v", err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("the shared renderer fixture holds no cases")
	}

	for _, c := range fixture.Cases {
		e, err := event.Decode(1, "s-1", 0, c.Kind, c.Payload)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		got := draw(e)
		goRow := renderedRow{got.Title, got.Text, got.Detail, got.Appendable}
		if !reflect.DeepEqual(goRow, c.Row) {
			t.Errorf("Go %s drew %+v, want %+v", c.Name, goRow, c.Row)
		}
	}

	node := findNode(t)
	source, err := os.ReadFile("render.js")
	if err != nil {
		t.Fatal(err)
	}
	program := `const fs = require("fs");
` + string(source) + `
const fixture = JSON.parse(fs.readFileSync(process.env.FIXTURE, "utf8"));
const rows = fixture.cases.map((c) => {
  const r = draw(c.kind, c.payload);
  return {title: r.title ?? "", text: r.text ?? "", detail: r.detail ?? "", appendable: r.appendable ?? false};
});
console.log(JSON.stringify(rows));`
	cmd := exec.Command(node, "-e", program)
	cmd.Env = append(os.Environ(), "FIXTURE=testdata/render.json")
	said, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, said)
	}
	var jsRows []renderedRow
	if err := json.Unmarshal(said, &jsRows); err != nil {
		t.Fatalf("node said %q: %v", said, err)
	}
	for i, c := range fixture.Cases {
		if !reflect.DeepEqual(jsRows[i], c.Row) {
			t.Errorf("JavaScript %s drew %+v, want %+v", c.Name, jsRows[i], c.Row)
		}
	}
}

// The page carries its Events in a script element, so a Harness that wrote
// </script> into a message must not be able to close it. encoding/json escapes
// <, > and & even inside a payload it is passing through, and this is the test
// that says so rather than the comment.
func TestTheEmbeddedEventsCannotCloseTheScriptTag(t *testing.T) {
	said := string(payloads([]protocol.Event{{
		Seq: 1, Session: "s-1", Kind: "AssistantMessage",
		Payload: json.RawMessage(`{"text":"</script><img src=x onerror=alert(1)>","complete":true}`),
	}}))

	if strings.Contains(said, "</script") || strings.Contains(said, "<img") {
		t.Fatalf("the page would carry %s", said)
	}
	// And it is still the same JSON once a browser has parsed it.
	var back []struct {
		Payload struct {
			Text string `json:"text"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(said), &back); err != nil {
		t.Fatalf("the browser could not read it: %v", err)
	}
	if back[0].Payload.Text != `</script><img src=x onerror=alert(1)>` {
		t.Errorf("the text came back as %q", back[0].Payload.Text)
	}
}
