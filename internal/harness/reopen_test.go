package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/VictorJohnOkoh/Dispatch/internal/vendors"
)

func TestOpenCodeLoadsHistoryWithoutWritingReplay(t *testing.T) {
	x := newTransport(t)
	out := newRecording()
	x.play(func() {
		raw, ok := x.next()
		if !ok {
			return
		}
		id := idOf(raw)
		x.feed([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":1}}`, *id)))
		raw, ok = x.next()
		if !ok {
			return
		}
		var call struct {
			Method string `json:"method"`
			Params struct {
				Session string `json:"sessionId"`
			} `json:"params"`
		}
		json.Unmarshal(raw, &call)
		if call.Method != "session/load" || call.Params.Session != "native-old" {
			t.Errorf("load: %s", raw)
		}
		x.feed([]byte(`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"old reply"}}}}`))
		id = idOf(raw)
		x.feed([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"configOptions":[{"id":"model","currentValue":"capstone/test-model"}]}}`, *id)))
	})
	ref := ""
	run, err := NewOpenCode().Start(t.Context(), SessionSpec{Model: "test-model", Reference: "native-old", Spawn: x.spawn(), Files: out, Remember: func(s string) { ref = s }}, out)
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close()
	if ref != "native-old" {
		t.Fatalf("reference: %q", ref)
	}
	if got := out.said(); len(got) != 0 {
		t.Fatalf("load replay wrote: %v", got)
	}
}

func TestPiLoadsItsSessionFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(file, []byte("history"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"sessionFile": file, "model": map[string]string{"id": "model", "baseUrl": "http://localhost:1234/v1"}})
	frames := []recorded{
		{Dir: "out", Frame: json.RawMessage(`{"type":"get_state"}`)},
		{Dir: "in", Frame: announcement(t)},
		{Dir: "in", Frame: json.RawMessage(fmt.Sprintf(`{"id":%q,"type":"response","command":"get_state","success":true,"data":%s}`, startProbe, data))},
	}
	script := startPiScript(t, frames, "")
	spawn := script.spawn()
	reference := ""
	run, err := piAdapter(t).Start(t.Context(), SessionSpec{Model: "model", Vendor: vendors.Endpoint{Base: "http://localhost:1234"}, Reference: file, Remember: func(s string) { reference = s }, Spawn: func(ctx context.Context, l Launch) (Pipes, error) {
		i := slices.Index(l.Args, "--session")
		if i < 0 || i+1 >= len(l.Args) || l.Args[i+1] != file {
			t.Errorf("launch: %v", l.Args)
		}
		return spawn(ctx, l)
	}}, newRecording())
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close()
	if reference != file {
		t.Fatalf("reference: %q", reference)
	}
}
