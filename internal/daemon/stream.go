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

// replayPage is how many Events one read of a replay holds. A log is never pruned,
// so a Cursor from last month is a long read, and it is paged rather than loaded.
const replayPage = 500

// streamEvents serves this Host's Event stream. It subscribes to the log first and
// reads anything else second, so nothing written in between is missed.
//
// Last-Event-ID resumes a stream. A Cursor this log cannot serve is answered with
// a resync Frame rather than an HTTP error, because the stream's liveness is the
// Host's presence: failing the request would tell the user their machine is
// unreachable when the log was merely replaced.
func (d *Daemon) streamEvents(w http.ResponseWriter, r *http.Request) {
	if !d.speaks(w, r) {
		return
	}
	flush, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "this server cannot stream", http.StatusInternalServerError)
		return
	}

	from, resuming, err := resumeAt(r)
	if err != nil {
		refuse(w, protocol.StatusUnprocessable, protocol.Refusal{
			Reason: protocol.ReasonMalformed, Detail: err.Error(),
		})
		return
	}

	frames, stop := d.events.Subscribe()
	defer stop()

	// One view of the log, so the replay below and the Cursor it ends on describe
	// the same moment. Everything above it arrives on the subscription instead.
	at := d.events.Resume()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	// hello is first on every connection. The Cursor starts where a reader that has
	// read all of this view stands, which lags an open message, so a reconnect from
	// it replays that message whole.
	out := &sse{w: w, flush: flush, at: newCursor(at.Latest, at.Open)}
	out.frame(protocol.FrameHello, "", protocol.Hello{
		Protocol: protocol.Version, LogID: at.LogID, Latest: at.Latest,
	})

	switch {
	case !resuming:
		// This connection starts at the live edge, and nothing below it is sent.
	case unservable(r.Header.Get(protocol.LogHeader), from, at):
		out.frame(protocol.FrameResync, "", protocol.Resync{LogID: at.LogID, Latest: at.Latest})
	default:
		out.at = newCursor(uint64(from), nil)
		d.replay(out, from, at)
	}

	// The whole Vendor state follows, so a Client that attached between two beats
	// has a Vendor row to draw.
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
			out.fromLog(f, at.Latest)
		case <-beat:
			views, beat = d.vendors.Watch()
			out.frame(protocol.FrameVendors, "", vendorsBody{views})
		case <-keepalive.C:
			out.keepalive()
		}
	}
	d.log.Debug("an Event stream failed to write", "err", out.err)
}

// resumeAt is where this connection resumes from. A reader that sends no
// Last-Event-ID starts at the live edge, which is a different thing from a Cursor
// of zero: zero is a real Cursor and it means the whole log.
func resumeAt(r *http.Request) (protocol.Cursor, bool, error) {
	raw := r.Header.Get(protocol.CursorHeader)
	if raw == "" {
		return 0, false, nil
	}
	from, err := protocol.ParseCursor(raw)
	return from, true, err
}

// unservable is the resync rule, and there are only two ways to meet it. Nothing
// is ever deleted, so a Cursor is never too old: it is unservable when it names a
// Sequence Number this log never allotted, or when the reader says it took the
// Cursor from a different log. A reader that names no log compares nothing, which
// is what a fresh reader and a curl both do.
func unservable(held string, from protocol.Cursor, at eventlog.Resume) bool {
	return uint64(from) > at.Latest || (held != "" && held != at.LogID)
}

// replay writes every Event between a Cursor and the moment this reader joined,
// oldest first and one page at a time. Deltas are not replayed: a Delta never
// carries text its Event will not eventually hold, so the rows carry everything.
func (d *Daemon) replay(out *sse, from protocol.Cursor, at eventlog.Resume) {
	for after := uint64(from); out.err == nil; {
		page, err := d.events.Replay(after, at.Latest, replayPage)
		if err != nil {
			d.log.Error("a replay could not be read", "from", after, "err", err)
			out.err = err
			return
		}
		for _, e := range page {
			out.frame(protocol.FrameEvent, stamp(out.at.event(e.Seq, slices.Contains(at.Open, e.Seq))), e)
			after = e.Seq
		}
		if len(page) < replayPage {
			return
		}
	}
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
	// The Hub marks this Host Incompatible and never dials it again, so this line
	// is the only evidence the check ran: one of them and no more is the Hub having
	// stopped rather than the Handshake having passed.
	d.log.Info("the Handshake was refused", "asked", asked, "speaks", protocol.ServedVersions)
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

// newCursor is where a reader that has read the log up to seen stands, given the
// messages still open at or below it.
func newCursor(seen uint64, open []uint64) cursor {
	c := cursor{at: seen, seen: seen, open: open}
	if len(open) > 0 {
		c.at = open[0] - 1
	}
	return c
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
//
// An Event at or below replayed was written before this reader joined and has been
// sent already, so it is dropped rather than sent twice. A Delta is always
// forwarded: it is at most once and never replayed, and the one for a message the
// replay caught mid-flight is what carries that message the rest of the way.
func (s *sse) fromLog(f eventlog.Frame, replayed uint64) {
	switch {
	case f.Event != nil:
		if f.Event.Seq <= replayed {
			return
		}
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
