package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// frame is one Frame a test read off a stream. A keepalive is a comment, so it
// carries no name and no data, which is how a test tells it from the five.
type frame struct {
	id      string
	name    string
	data    string
	comment string
}

// reader reads Frames off one open stream, one at a time, and fails rather than
// hanging when nothing arrives.
type reader struct {
	body  *bufio.Scanner
	close func()
}

// stream opens GET /v1/events against a live server and reads Frames off it. The
// keepalive beat is shortened, because a test may not wait ten seconds for one.
func (h *host) stream(t *testing.T) (*httptest.Server, *reader) {
	t.Helper()
	h.keepalive = 20 * time.Millisecond
	srv := httptest.NewServer(h.handler())
	t.Cleanup(srv.Close)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(srv.URL + "/v1/events")
	if err != nil {
		t.Fatalf("GET /v1/events: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content type %q", got)
	}
	return srv, &reader{body: bufio.NewScanner(resp.Body), close: func() { resp.Body.Close() }}
}

// next reads one Frame. A blank line ends a Frame, which is the whole of SSE
// framing.
func (r *reader) next(t *testing.T) frame {
	t.Helper()
	var f frame
	for {
		if !r.body.Scan() {
			t.Fatalf("the stream ended: %v", r.body.Err())
		}
		line := r.body.Text()
		switch {
		case line == "":
			return f
		case strings.HasPrefix(line, ":"):
			f.comment = strings.TrimSpace(line[1:])
		case strings.HasPrefix(line, "id:"):
			f.id = strings.TrimSpace(line[3:])
		case strings.HasPrefix(line, "event:"):
			f.name = strings.TrimSpace(line[6:])
		case strings.HasPrefix(line, "data:"):
			f.data = strings.TrimSpace(line[5:])
		default:
			t.Fatalf("line %q is not SSE", line)
		}
	}
}

// nextNamed reads until a Frame of that name arrives, so a test watching for
// Events is not tripped by a keepalive or a Vendor beat.
func (r *reader) nextNamed(t *testing.T, name protocol.Frame) frame {
	t.Helper()
	for {
		if f := r.next(t); f.name == string(name) {
			return f
		}
	}
}

func (f frame) decode(t *testing.T, into any) {
	t.Helper()
	if err := json.Unmarshal([]byte(f.data), into); err != nil {
		t.Fatalf("%s frame: %v: %s", f.name, err, f.data)
	}
}

func TestStreamOpensWithHelloAndHoldsTheConnection(t *testing.T) {
	h := newHost(t)
	_, r := h.stream(t)

	f := r.next(t)
	if f.name != string(protocol.FrameHello) {
		t.Fatalf("first Frame is %q, want hello", f.name)
	}
	if f.id != "" {
		t.Errorf("hello carries id %q, and it advances no Cursor", f.id)
	}
	var hello protocol.Hello
	f.decode(t, &hello)
	if hello.Protocol != protocol.Version {
		t.Errorf("hello = %+v", hello)
	}

	// The connection is still open, so the next Frame arrives on it.
	if next := r.next(t); next.name != string(protocol.FrameVendors) {
		t.Errorf("second Frame is %q, want vendors", next.name)
	}
}

// A Session started on another connection appears on this one.
func TestASessionStartedElsewhereArrivesAsEventFrames(t *testing.T) {
	h := newHost(t)
	srv, r := h.stream(t)
	r.nextNamed(t, protocol.FrameHello)

	resp, err := http.Post(srv.URL+"/v1/sessions", "application/json", strings.NewReader(startBody))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != protocol.StatusStarted {
		t.Fatalf("start: status %d", resp.StatusCode)
	}

	for _, want := range []event.Kind{event.KindSessionStarted, event.KindSessionReady} {
		f := r.nextNamed(t, protocol.FrameEvent)
		var e protocol.Event
		f.decode(t, &e)
		if e.Kind != string(want) {
			t.Fatalf("Kind = %q, want %s", e.Kind, want)
		}
		if f.id != strconv.FormatUint(e.Seq, 10) {
			t.Errorf("id = %q, want %d: neither Event leaves a message open", f.id, e.Seq)
		}
	}
}

func TestKeepaliveArrivesOnTheBeatAndAdvancesNoCursor(t *testing.T) {
	h := newHost(t)
	_, r := h.stream(t)

	for range 4 {
		f := r.next(t)
		if f.comment == "" {
			continue
		}
		if f.comment != "keepalive" || f.id != "" || f.name != "" {
			t.Fatalf("comment Frame = %+v", f)
		}
		return
	}
	t.Fatal("no keepalive arrived on four Frames")
}

// The first vendors Frame carries the whole current state, and a Vendor that stops
// answering empties its row rather than leaving it stale.
func TestVendorsFramesCarryThePollAndEmptyAnUnreachableRow(t *testing.T) {
	h := newHost(t)
	_, r := h.stream(t)

	var body struct{ Vendors []VendorView }
	r.nextNamed(t, protocol.FrameVendors).decode(t, &body)
	if len(body.Vendors) != 1 || !body.Vendors[0].Reachable || len(body.Vendors[0].Resident) != 1 {
		t.Fatalf("first vendors Frame = %+v", body.Vendors)
	}

	h.vendor.err = errors.New("connection refused")
	h.vendors.pollAll(t.Context())

	r.nextNamed(t, protocol.FrameVendors).decode(t, &body)
	if len(body.Vendors) != 1 || body.Vendors[0].Reachable || len(body.Vendors[0].Resident) != 0 {
		t.Fatalf("after the failed beat = %+v", body.Vendors)
	}
	if body.Vendors[0].Base == "" {
		t.Error("the row lost the Vendor it is about")
	}
}

// An Event that leaves a message open carries no id, and neither does a Delta
// until the final one. That lag is what makes the message replay whole.
func TestAnOpenMessageHoldsTheCursorUntilItsFinalDelta(t *testing.T) {
	h := newHost(t)
	_, r := h.stream(t)

	s := &Session{id: "s-open", cancel: func() {}}
	h.sessions.add(s)
	k := &sink{d: h.Daemon, s: s}
	k.Message("half a", false)
	k.Message(" message", false)
	k.Message(" that ended", true)

	if f := r.nextNamed(t, protocol.FrameEvent); f.id != "" {
		t.Errorf("the Event that opens a message carries id %q", f.id)
	}
	if first := r.nextNamed(t, protocol.FrameDelta); first.id != "" {
		t.Errorf("a Delta that is not final carries id %q", first.id)
	}

	final := r.nextNamed(t, protocol.FrameDelta)
	if final.id != "1" {
		t.Errorf("the final Delta carries id %q, want 1", final.id)
	}
	var delta protocol.Delta
	final.decode(t, &delta)
	if !delta.Final || delta.Text != "half a message that ended" {
		t.Errorf("final Delta = %+v", delta)
	}
}

// An Event is committed before it is sent, so a reader that dies the moment a
// Frame arrives still finds that Event in the file.
func TestAnEventIsInTheFileBeforeItIsOnTheWire(t *testing.T) {
	h := newHost(t)
	srv, r := h.stream(t)
	r.nextNamed(t, protocol.FrameHello)

	resp, err := http.Post(srv.URL+"/v1/sessions", "application/json", strings.NewReader(startBody))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer resp.Body.Close()

	r.nextNamed(t, protocol.FrameEvent)
	r.close()

	if got := h.kinds(t); len(got) == 0 || got[0] != "SessionStarted" {
		t.Fatalf("the file holds %v", got)
	}
}

// The Cursor is the highest Seq below every open message, so an Event written
// while one is open does not move it either.
func TestCursorHoldsBelowTheOldestOpenMessage(t *testing.T) {
	var c cursor

	if at, moved := c.event(1, false); !moved || at != 1 {
		t.Fatalf("a closed Event = %d, %v, want 1, true", at, moved)
	}
	if at, moved := c.event(2, true); moved {
		t.Fatalf("an Event that opens a message moved the Cursor to %d", at)
	}
	if at, moved := c.event(3, false); moved {
		t.Fatalf("an Event behind an open message moved the Cursor to %d", at)
	}
	if at, moved := c.delta(&protocol.Delta{Seq: 2}); moved {
		t.Fatalf("a Delta that is not final moved the Cursor to %d", at)
	}
	if at, moved := c.delta(&protocol.Delta{Seq: 2, Final: true}); !moved || at != 3 {
		t.Fatalf("the final Delta = %d, %v, want 3, true", at, moved)
	}
}
