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

	"github.com/VictorJohnOkoh/Dispatch/internal/eventlog"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
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
	// a Client that attached between two beats has a Vendor row to draw. The Cursor
	// starts where the log stands, because this connection starts at the live edge
	// and nothing below that is sent on it.
	out := &sse{w: w, flush: flush, at: cursor{at: latest, seen: latest}}
	out.frame(protocol.FrameHello, "", protocol.Hello{Protocol: protocol.Version, Latest: latest})
	views, beat := d.vendors.Watch()
	out.frame(protocol.FrameVendors, "", vendorsBody{views})

	keepalive := time.NewTicker(d.keepalive)
	defer keepalive.Stop()

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
			out.fromLog(f)
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
	if asked == "" {
		return true
	}
	if n, err := strconv.Atoi(asked); err == nil && slices.Contains(protocol.ServedVersions[:], n) {
		return true
	}
	refuse(w, protocol.StatusUpgradeRequired, protocol.Refusal{
		Reason: protocol.ReasonProtocol,
		Detail: fmt.Sprintf("this Daemon does not speak protocol %q", asked),
		Speaks: protocol.ServedVersions[:],
	})
	return false
}

// vendorsBody is the vendors frame. It names its list vendors, as the answer to
// GET /v1/models does, so the two are read the same way.
type vendorsBody struct {
	Vendors []VendorView `json:"vendors"`
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

// sse writes Frames to one connection and holds the Cursor it stamps them with.
// The first write that fails is kept and every call after it does nothing, so the
// loop above checks in one place.
type sse struct {
	w     io.Writer
	flush http.Flusher
	at    cursor
	err   error
}

// fromLog writes one Frame the Event log fanned out, as an event or as a delta.
func (s *sse) fromLog(f eventlog.Frame) {
	switch {
	case f.Event != nil:
		s.frame(protocol.FrameEvent, stamp(s.at.event(f.Event.Seq, f.Open)), f.Event)
	case f.Delta != nil:
		s.frame(protocol.FrameDelta, stamp(s.at.delta(f.Delta)), f.Delta)
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

// stamp spells a Cursor for the id: line, and spells nothing when the Cursor did
// not move.
func stamp(at protocol.Cursor, moved bool) string {
	if !moved {
		return ""
	}
	return at.String()
}
