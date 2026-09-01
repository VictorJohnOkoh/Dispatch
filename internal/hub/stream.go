package hub

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
)

// ADR 0004's reconnection curve: one second, doubling to a minute, with full
// jitter. A Host that is down must not end the merged stream. A stream that ended
// would read to the Client as one that finished, and it would reconnect at once
// and keep doing it.
const (
	firstDelay = time.Second
	maxDelay   = time.Minute

	// steadyFor is how long one connection must last before the curve starts over.
	// A shorter one is a flap, and a flap that reset the curve would let a
	// flapping Host redial at full speed forever.
	steadyFor = time.Minute
)

type daemonFrame struct {
	host hostset.HostID
	id   string
	name string
	data []byte
}

func (h *Hub) stream(w http.ResponseWriter, r *http.Request) {
	flush, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "this server cannot stream", http.StatusInternalServerError)
		return
	}
	cursors := make(protocol.MergedCursor)
	if raw := r.Header.Get(protocol.CursorHeader); raw != "" {
		parsed, err := protocol.ParseMergedCursor(raw)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		cursors = parsed
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	frames := make(chan daemonFrame)
	var readers sync.WaitGroup
	for _, host := range h.hosts.All() {
		at, resuming := cursors[string(host.ID)]
		reader := &hostReader{hub: h, id: host.ID, at: at, resuming: resuming}
		readers.Add(1)
		go func() {
			defer readers.Done()
			reader.run(r.Context(), frames)
		}()
	}
	go func() {
		readers.Wait()
		close(frames)
	}()

	keepalive := time.NewTicker(h.keepalive)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			io.WriteString(w, protocol.Keepalive)
			flush.Flush()
		case frame, ok := <-frames:
			if !ok {
				return
			}
			switch {
			case frame.name == string(protocol.FrameResync):
				// A Cursor the Host will not serve must stop being echoed, or the
				// Client hands the same one back on its next reconnect.
				delete(cursors, string(frame.host))
				fmt.Fprintf(w, "id: %s\n", cursors.String())
			case frame.id != "":
				at, err := protocol.ParseCursor(frame.id)
				if err != nil {
					continue
				}
				cursors[string(frame.host)] = at
				fmt.Fprintf(w, "id: %s\n", cursors.String())
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", frame.name, addHost(frame.data, frame.host))
			flush.Flush()
		}
	}
}

// hostReader is one Host's leg of the merged stream. It holds where that Host
// resumes from, because a reconnect starts where the last connection stopped and
// not where the Client's Cursor stood when the stream opened.
type hostReader struct {
	hub      *Hub
	id       hostset.HostID
	at       protocol.Cursor
	resuming bool

	// unknownLog says this connection dropped the Client's Cursor because the Hub
	// could not name the log it came from. The Client learns that from the resync
	// that follows the Hello.
	unknownLog bool
}

// run reads this Host until the Client leaves. It retries forever, so a Host the
// user switches on comes back without being told to.
func (r *hostReader) run(ctx context.Context, out chan<- daemonFrame) {
	delay := firstDelay
	for ctx.Err() == nil {
		start := time.Now()
		r.once(ctx, out)
		if time.Since(start) >= steadyFor {
			delay = firstDelay
		}
		if !sleep(ctx, jitter(delay)) {
			return
		}
		delay = min(2*delay, maxDelay)
	}
}

// once holds one connection to this Host's Daemon and reads it until it ends.
func (r *hostReader) once(ctx context.Context, out chan<- daemonFrame) {
	conn, err := r.hub.dialer.Dial(ctx, r.id)
	if err != nil {
		return
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://daemon/v1/events", nil)
	req.Header.Set(protocol.VersionHeader, fmt.Sprint(protocol.Version))

	// A Cursor travels with the identity of the log it came from, and the Hub holds
	// that identity in memory only. A Hub that restarted holds none, and a Sequence
	// Number checked against no log would resume a replaced log at a number that
	// means something else there. So the connection starts at the live edge instead
	// and the Client is told to refetch.
	logID := r.hub.hosts.LogID(r.id)
	r.unknownLog = r.resuming && logID == ""
	if r.resuming && logID != "" {
		req.Header.Set(protocol.CursorHeader, r.at.String())
		req.Header.Set(protocol.LogHeader, logID)
	}
	if err := req.Write(conn); err != nil {
		return
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	r.read(ctx, resp.Body, out)
}

// read splits SSE into Frames. It parses no body but the Hello's, so an Event Kind
// this build has never heard of still reaches the Client.
func (r *hostReader) read(ctx context.Context, body io.Reader, out chan<- daemonFrame) {
	reader := bufio.NewReader(body)
	frame := daemonFrame{host: r.id}
	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		switch {
		case line == "":
			if frame.name != "" && !r.deliver(ctx, frame, out) {
				return
			}
			frame = daemonFrame{host: r.id}
		case strings.HasPrefix(line, "id:"):
			frame.id = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "event:"):
			frame.name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data := strings.TrimPrefix(line, "data:")
			if strings.HasPrefix(data, " ") {
				data = data[1:]
			}
			if len(frame.data) > 0 {
				frame.data = append(frame.data, '\n')
			}
			frame.data = append(frame.data, data...)
		}
		if err != nil {
			return
		}
	}
}

// deliver forwards one Frame and keeps what the Hub learns from a Hello. It reports
// whether the Client is still there.
func (r *hostReader) deliver(ctx context.Context, frame daemonFrame, out chan<- daemonFrame) bool {
	if frame.name != string(protocol.FrameHello) {
		return r.forward(ctx, frame, out)
	}
	hello := r.hub.hosts.ObserveHello(r.id, frame.data)
	if !r.forward(ctx, frame, out) {
		return false
	}
	if !r.unknownLog {
		return true
	}
	r.unknownLog = false
	body, _ := json.Marshal(protocol.Resync{LogID: hello.LogID, Latest: hello.Latest})
	return r.forward(ctx, daemonFrame{host: r.id, name: string(protocol.FrameResync), data: body}, out)
}

// forward tracks where this Host stands and hands the Frame to the merged stream.
func (r *hostReader) forward(ctx context.Context, frame daemonFrame, out chan<- daemonFrame) bool {
	switch {
	case frame.name == string(protocol.FrameResync):
		// The Cursor was refused, so the next connection must not send it again.
		r.at, r.resuming = 0, false
	case frame.id != "":
		if at, err := protocol.ParseCursor(frame.id); err == nil {
			r.at, r.resuming = at, true
		}
	}
	select {
	case out <- frame:
		return true
	case <-ctx.Done():
		return false
	}
}

// jitter is ADR 0004's full jitter: anywhere from nothing to the whole delay, so
// two Hosts that dropped together do not redial together.
func jitter(d time.Duration) time.Duration { return rand.N(d) }

// sleep reports whether the wait finished rather than the Client leaving.
func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func addHost(data []byte, host hostset.HostID) []byte {
	if len(data) == 0 || data[0] != '{' {
		return data
	}
	prefix := fmt.Sprintf(`{"host":%q,`, host)
	return append([]byte(prefix), data[1:]...)
}
