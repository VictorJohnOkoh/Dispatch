package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/eventlog"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// The page GET /v1/sessions/{session}/events serves when the request asks for no
// size, and the largest it will serve whatever the request asks for.
const (
	defaultPage = 200
	maxPage     = 1000
)

// streamEvents serves this Host's Event stream. It subscribes to the log first and
// reads anything else second, so nothing written in between is missed.
//
// Last-Event-ID resumes a stream, and serving a resume is not here yet: this
// connection starts at the live edge. hello carries no log identity for the same
// reason, because nothing can compare one until a Cursor is served.
func (d *Daemon) streamEvents(w http.ResponseWriter, r *http.Request) {
	if !d.speaks(w, r) {
		return
	}
	flush, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "this server cannot stream", http.StatusInternalServerError)
		return
	}

	frames, stop := d.events.Subscribe()
	defer stop()

	latest, err := d.events.Latest()
	if err != nil {
		http.Error(w, "the Event log could not be read", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	// hello is first on every connection, and the whole Vendor state follows it, so
	// a Client that attached between two beats has a Vendor row to draw.
	out := &sse{w: w, flush: flush}
	out.frame(protocol.FrameHello, "", protocol.Hello{Protocol: protocol.Version, Latest: latest})
	views, beat := d.vendors.Watch()
	out.frame(protocol.FrameVendors, "", vendorsBody{views})

	keepalive := time.NewTicker(d.keepalive)
	defer keepalive.Stop()

	var at cursor
	for out.err == nil {
		select {
		case <-r.Context().Done():
			return
		case f, open := <-frames:
			if !open {
				// The log dropped this subscriber for falling behind. Ending the
				// connection costs one reconnect, which is what a Cursor is for.
				return
			}
			out.log(&at, f)
		case <-beat:
			views, beat = d.vendors.Watch()
			out.frame(protocol.FrameVendors, "", vendorsBody{views})
		case <-keepalive.C:
			out.keepalive()
		}
	}
	d.log.Debug("an Event stream failed to write", "err", out.err)
}

// speaks is the Handshake. A caller that names a version this Daemon cannot serve
// is refused here, and one that names none is served, because curl names none.
func (d *Daemon) speaks(w http.ResponseWriter, r *http.Request) bool {
	asked := r.Header.Get(protocol.VersionHeader)
	if asked == "" || slices.Contains(protocol.ServedVersions[:], atoi(asked)) {
		return true
	}
	refuse(w, protocol.StatusUpgradeRequired, protocol.Refusal{
		Reason: protocol.ReasonProtocol,
		Detail: fmt.Sprintf("this Daemon does not speak protocol %q", asked),
		Speaks: protocol.ServedVersions[:],
	})
	return false
}

// atoi is the version header as a number, and 0 for anything that is not one. No
// build serves version 0, so an unreadable header is refused with the rest.
func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// vendorsBody is the vendors frame. It names its list exactly as GET /v1/models
// does, so the two answers about Vendors have one shape.
type vendorsBody struct {
	Vendors []VendorView `json:"vendors"`
}

// sessionEvents pages one Session's Events, oldest first. It reads the log rather
// than the registry, because the rows are already the wire shape and a Session
// that ended long ago is still in the file.
func (d *Daemon) sessionEvents(w http.ResponseWriter, r *http.Request) {
	id := event.SessionID(r.PathValue("session"))
	if !d.sessions.known(id) {
		refuse(w, protocol.StatusNoSession, protocol.Refusal{
			Reason: protocol.ReasonUnknownSession,
			Detail: fmt.Sprintf("this Host has no Session %q", id),
		})
		return
	}

	after, err := number(r, "after", 0)
	if err != nil {
		refuse(w, protocol.StatusUnprocessable, protocol.Refusal{Reason: protocol.ReasonMalformed, Detail: err.Error()})
		return
	}
	limit, err := number(r, "limit", defaultPage)
	if err != nil {
		refuse(w, protocol.StatusUnprocessable, protocol.Refusal{Reason: protocol.ReasonMalformed, Detail: err.Error()})
		return
	}

	page, err := d.events.SessionPage(id, after, min(int(limit), maxPage))
	if err != nil {
		http.Error(w, "the Event log could not be read", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Events []protocol.Event `json:"events"`
	}{page})
}

// number reads one query parameter, or its default when the request omits it.
func number(r *http.Request, name string, missing uint64) (uint64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return missing, nil
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not a number", name, raw)
	}
	return n, nil
}

// cursor is where this connection may resume: the highest Sequence Number below
// every open appendable Event. It is not the last Seq sent, and that lag is what
// makes an unfinished message replay whole.
//
// open holds the Seqs of the messages still taking text. They arrive in order and
// there are rarely two, so it is a slice and the oldest is the first.
type cursor struct {
	at   uint64
	seen uint64
	open []uint64
}

// event takes one Event and reports the Cursor to stamp on it, or false when the
// Event does not advance it.
func (c *cursor) event(seq uint64, opens bool) (protocol.Cursor, bool) {
	c.seen = seq
	if opens {
		c.open = append(c.open, seq)
	}
	return c.settle()
}

// delta takes one Delta. Only the final one moves anything: it is the moment the
// log holds the message whole, so the Cursor may pass it.
func (c *cursor) delta(d *protocol.Delta) (protocol.Cursor, bool) {
	if !d.Final {
		return 0, false
	}
	c.open = slices.DeleteFunc(c.open, func(seq uint64) bool { return seq == d.Seq })
	return c.settle()
}

// settle moves the Cursor to just below the oldest open message, or to the highest
// Seq seen when none is open, and reports whether it moved.
func (c *cursor) settle() (protocol.Cursor, bool) {
	to := c.seen
	if len(c.open) > 0 {
		to = c.open[0] - 1
	}
	if to <= c.at {
		return 0, false
	}
	c.at = to
	return protocol.Cursor(to), true
}

// sse writes Frames to one connection. The first write that fails is kept and
// every call after it does nothing, so the loop above checks in one place.
type sse struct {
	w     io.Writer
	flush http.Flusher
	err   error
}

// log writes one Frame the Event log fanned out, as an event or as a delta.
func (s *sse) log(at *cursor, f eventlog.Frame) {
	switch {
	case f.Event != nil:
		s.frame(protocol.FrameEvent, id(at.event(f.Event.Seq, f.Open)), f.Event)
	case f.Delta != nil:
		s.frame(protocol.FrameDelta, id(at.delta(f.Delta)), f.Delta)
	}
}

// frame writes one named Frame and flushes it. An empty id writes no id: line,
// which is every Frame that advances no Cursor.
func (s *sse) frame(name protocol.Frame, id string, payload any) {
	if s.err != nil {
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		s.err = fmt.Errorf("daemon: %s frame: %w", name, err)
		return
	}

	var b bytes.Buffer
	if id != "" {
		fmt.Fprintf(&b, "id: %s\n", id)
	}
	fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", name, body)
	s.write(b.Bytes())
}

func (s *sse) keepalive() { s.write([]byte(protocol.Keepalive)) }

func (s *sse) write(b []byte) {
	if s.err != nil {
		return
	}
	if _, err := s.w.Write(b); err != nil {
		s.err = err
		return
	}
	s.flush.Flush()
}

// id spells a Cursor for the id: line, and spells nothing when the Cursor did not
// move.
func id(at protocol.Cursor, moved bool) string {
	if !moved {
		return ""
	}
	return at.String()
}
