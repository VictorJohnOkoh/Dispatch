package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// Seven Frames. Five are the Daemon's, the sixth is the keepalive, which has no
// event: name because it is an SSE comment, and the seventh is the one the Hub
// originates rather than forwards.
func TestTheSevenFrameTypes(t *testing.T) {
	daemon := []Frame{FrameHello, FrameEvent, FrameDelta, FrameVendors, FrameResync}
	for _, f := range daemon {
		if f == "" {
			t.Error("a Daemon Frame has no name")
		}
		if f.OriginatedByHub() {
			t.Errorf("%s is the Daemon's, so the Hub forwards it rather than making it", f)
		}
	}

	if !FrameHost.OriginatedByHub() {
		t.Error("host is the one Frame the Hub originates")
	}

	// The keepalive is written out whole, because there is no event: line to name.
	if !strings.HasPrefix(Keepalive, ":") || !strings.HasSuffix(Keepalive, "\n\n") {
		t.Errorf("the keepalive is %q, want an SSE comment", Keepalive)
	}
	if KeepaliveInterval.Seconds() != 10 {
		t.Errorf("the beat is %s, want 10s", KeepaliveInterval)
	}
}

// Ten endpoints on the Daemon's leg. The Client's are the same under one more path
// segment, and that segment is the whole difference between the two legs.
func TestTheTenRoutes(t *testing.T) {
	seen := make(map[string]bool, len(Routes))
	for _, route := range Routes {
		method, path, ok := strings.Cut(route, " ")
		if !ok {
			t.Fatalf("%q is not a method and a path", route)
		}
		if method != "GET" && method != "POST" {
			t.Errorf("%q uses %s, and the command set has only GET and POST", route, method)
		}
		if !strings.HasPrefix(path, "/v1/") {
			t.Errorf("%q is not under /v1", route)
		}
		if seen[route] {
			t.Errorf("%q is listed twice", route)
		}
		seen[route] = true
	}

	if len(seen) != 10 {
		t.Errorf("Routes holds %d endpoints, want 10", len(seen))
	}
	if seen[ListHosts] {
		t.Error("GET /v1/hosts is the eleventh, and only the Hub answers it")
	}
}

// Stop is a command rather than a DELETE, because a stopped Session keeps its
// history. A method name that says deleted while the thing stays is a lie.
func TestStopIsNotADelete(t *testing.T) {
	if !strings.HasPrefix(StopSession, "POST ") {
		t.Errorf("stop is %q, want a POST", StopSession)
	}
}

func TestOnHostNamesAHost(t *testing.T) {
	cases := []struct {
		route, want string
	}{
		{StreamEvents, "GET /v1/hosts/{host}/events"},
		{StartSession, "POST /v1/hosts/{host}/sessions"},
		{SubmitPrompt, "POST /v1/hosts/{host}/sessions/{session}/prompts"},
		{ListModels, "GET /v1/hosts/{host}/models"},
	}
	for _, c := range cases {
		if got := OnHost(c.route); got != c.want {
			t.Errorf("OnHost(%q) = %q, want %q", c.route, got, c.want)
		}
	}

	// Every one of the ten maps to a Client route, and no two collide.
	seen := make(map[string]bool, len(Routes))
	for _, route := range Routes {
		client := OnHost(route)
		if !strings.Contains(client, "{host}") {
			t.Errorf("%q lost the Host", client)
		}
		if seen[client] {
			t.Errorf("%q is the Client route for two endpoints", client)
		}
		seen[client] = true
	}
}

// The Daemon's envelope has no room for a Host. encoding/json drops a field the
// target does not have, so a Hub that told a Daemon about a peer would be sending
// bytes into a type that cannot hold them.
func TestTheDaemonsEnvelopeCannotCarryAHost(t *testing.T) {
	var e Event
	wire := `{"seq":9412,"session":"s-7f3a2c","at":1756412093118000,"kind":"PromptSubmitted","payload":{"text":"rename the handler"},"host":"desktop"}`
	if err := json.Unmarshal([]byte(wire), &e); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if e.Seq != 9412 || e.Kind != "PromptSubmitted" {
		t.Fatalf("decoded %+v", e)
	}

	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if strings.Contains(string(b), "host") {
		t.Errorf("an Event came back carrying a Host: %s", b)
	}

	// The payload survives byte for byte, which is what lets the Hub forward a Kind
	// it has never heard of.
	if got := string(e.Payload); got != `{"text":"rename the handler"}` {
		t.Errorf("payload is %s, want it verbatim", got)
	}
}

// A HostFrame is a Frame that carries an Event, not an Event with one more field,
// and it flattens on the wire because ADR 0009 put host beside the other five.
func TestHostFrameFlattens(t *testing.T) {
	f := HostFrame{
		Event: Event{Seq: 9412, Session: "s-7f3a2c", At: 1756412093118000, Kind: "PromptSubmitted", Payload: json.RawMessage(`{}`)},
		Host:  "desktop",
	}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var flat map[string]json.RawMessage
	if err := json.Unmarshal(b, &flat); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, field := range []string{"host", "seq", "session", "at", "kind", "payload"} {
		if _, ok := flat[field]; !ok {
			t.Errorf("no %s field in %s", field, b)
		}
	}
	if len(flat) != 6 {
		t.Errorf("%s has %d fields, want 6", b, len(flat))
	}
}

// The Handshake names one version, and the Daemon answers with the set it serves.
func TestTheHandshake(t *testing.T) {
	if Version != 1 {
		t.Errorf("this build speaks %d, want 1", Version)
	}
	if len(ServedVersions) != 1 || ServedVersions[0] != Version {
		t.Errorf("a Daemon serves %v, want just {%d}", ServedVersions, Version)
	}

	b, err := json.Marshal(Hello{Protocol: Version, LogID: "3f9c2a71", Latest: 9420})
	if err != nil {
		t.Fatalf("encode hello: %v", err)
	}
	if got, want := string(b), `{"protocol":1,"logId":"3f9c2a71","latest":9420}`; got != want {
		t.Errorf("hello is %s, want %s", got, want)
	}
}

// The refusal body is the same shape every time, and each case fills only the
// field it owns.
func TestRefusalBodies(t *testing.T) {
	cases := []struct {
		name    string
		refusal Refusal
		want    string
	}{
		{
			"admission names the Session holding the slot",
			Refusal{Reason: ReasonAdmission, Detail: "one Session at a time on this Host", Blocking: []string{"s-7f3a2c"}},
			`{"reason":"admission","detail":"one Session at a time on this Host","blocking":["s-7f3a2c"]}`,
		},
		{
			"the Handshake names what it does speak",
			Refusal{Reason: ReasonProtocol, Speaks: []int{2}},
			`{"reason":"protocol","speaks":[2]}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := json.Marshal(c.refusal)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if got := string(b); got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

// A Delta is text for an Event the log already holds. It carries no Seq of its
// own: the one it names is the Event's.
func TestDeltaCarriesItsEventsSeq(t *testing.T) {
	b, err := json.Marshal(Delta{Seq: 9413, N: 41, Text: "the whole message", Final: true})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if got, want := string(b), `{"seq":9413,"n":41,"text":"the whole message","final":true}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}

	// An open Delta says nothing about being final, because most of them are not.
	b, err = json.Marshal(Delta{Seq: 9413, N: 0, Text: "the "})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if strings.Contains(string(b), "final") {
		t.Errorf("got %s, want no final field", b)
	}
}
