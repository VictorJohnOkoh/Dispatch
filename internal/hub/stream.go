package hub

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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

	// steadyFor is how long one connection must have been Ready before the curve
	// starts over. A shorter one is a flap, and a flap that reset the curve would
	// let a flapping Host redial at full speed forever.
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
	if raw := resumeAt(r); raw != "" {
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
	// Go holds the header until something flushes it, and the first thing to write
	// may be a keepalive ten seconds away. A Client waiting that long cannot tell
	// an open stream from a hung one, and a browser's EventSource does not open
	// until the header lands.
	flush.Flush()
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
	// reading is the same channel, dropped once every reader has ended. The stream
	// stays open on keepalives alone after that: one that ended would read to the
	// Client as one that finished, and the browser would reconnect at once and keep
	// doing it against Hosts the Hub has stopped working on.
	reading := frames
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			io.WriteString(w, protocol.Keepalive)
			flush.Flush()
		case frame, ok := <-reading:
			if !ok {
				reading = nil
				continue
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

// resumeAt is the Cursor this connection resumes on. A reader that can set a
// header sends one; the browser's first stream after a server-rendered first paint
// cannot, and puts the same Cursor in the query instead.
func resumeAt(r *http.Request) string {
	if raw := r.Header.Get(protocol.CursorHeader); raw != "" {
		return raw
	}
	return r.URL.Query().Get(protocol.CursorParam)
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

	// state is what this Hub last told the Client about this Host. It is held only
	// to keep from saying the same thing twice: Host State is derived from the
	// connection below, never stored, and this is one connection's memory of what
	// it has already said.
	state protocol.HostState

	// failures is how many attempts in a row have not reached Ready. A Host is
	// Connecting until three of them, because a stream that dropped is usually back
	// before the third, and dimming a Host for a blink costs the user more than
	// holding the last display for seven seconds does.
	failures int
}

// downAfter is how many attempts in a row must fail before a Host is Down. ADR
// 0004 counts three, which is about seven seconds on this curve.
const downAfter = 3

// refusalLimit bounds the refusal body the Hub reads. It is a handful of version
// numbers, and a Host that answers megabytes to a Handshake is not one this Hub
// reads to the end.
const refusalLimit = 4096

// run reads this Host until the Client leaves. It retries forever, so a Host the
// user switches on comes back without being told to.
func (r *hostReader) run(ctx context.Context, out chan<- daemonFrame) {
	delay := r.hub.backoff
	for ctx.Err() == nil {
		// A stream that dropped is Connecting again, which is ADR 0004's transition
		// out of Ready when the stream closes.
		r.says(ctx, protocol.HostStateFrame{State: protocol.Connecting}, out)

		// The curve starts over only after a connection that was Ready for a whole
		// minute. That is measured here rather than read off the state afterwards,
		// because by then this Host is Connecting again and every connection would
		// look like a flap.
		if steady := r.once(ctx, out); steady >= r.hub.steady {
			delay = r.hub.backoff
		}
		// Incompatible is the one state the Hub stops working on. This reader ends,
		// so the Host takes no more dials and makes no more backoff traffic, and it
		// keeps its place in the merged stream and on the page.
		if r.state == protocol.Incompatible {
			return
		}
		if !sleep(ctx, jitter(delay)) {
			return
		}
		delay = min(2*delay, maxDelay)
	}
}

// failed counts one attempt that did not reach Ready, and calls the Host Down once
// three in a row have. The cause is where the last one failed: nothing answering
// at all and a tunnel with nothing behind it are different problems for the user.
func (r *hostReader) failed(ctx context.Context, why protocol.Cause, out chan<- daemonFrame) {
	r.failures++
	if r.failures < downAfter {
		return
	}
	r.says(ctx, protocol.HostStateFrame{State: protocol.Down, Cause: why}, out)
}

// says tells the Client what this Host is, unless it has said so already. The
// frame is the Hub's own: it cannot be an Event, because every Event carries a
// Session id and a Host that is down has no Session to carry one.
func (r *hostReader) says(ctx context.Context, said protocol.HostStateFrame, out chan<- daemonFrame) bool {
	if r.state == said.State {
		return true
	}
	r.state = said.State
	// The merged-stream writer adds the Host to every Frame. It must not be in this
	// body too, or JSON carries the same key twice.
	body, _ := json.Marshal(said)
	select {
	case out <- daemonFrame{host: r.id, name: string(protocol.FrameHost), data: body}:
		return true
	case <-ctx.Done():
		return false
	}
}

// once holds one connection to this Host's Daemon and reads it until it ends. It
// answers how long that connection was Ready, which is what decides whether the
// backoff curve starts over.
func (r *hostReader) once(ctx context.Context, out chan<- daemonFrame) time.Duration {
	conn, err := r.hub.dialer.Dial(ctx, r.id)
	if err != nil {
		if errors.Is(err, hostset.ErrNoDaemon) {
			r.failed(ctx, protocol.NoDaemon, out)
		} else {
			// Nothing answered at all, which is a machine that is not there.
			r.failed(ctx, protocol.Unreachable, out)
		}
		return 0
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
		r.failed(ctx, protocol.NoDaemon, out)
		return 0
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		// The tunnel opened and nothing was behind it, which is a different problem
		// for the user: the machine is there and its Daemon is not.
		r.failed(ctx, protocol.NoDaemon, out)
		return 0
	}
	defer resp.Body.Close()
	// A Daemon that refused the Handshake answers 426 with the versions it can
	// serve. That Host is Incompatible rather than Down: the machine answered, and
	// what it said was that it speaks another version.
	if resp.StatusCode == protocol.StatusUpgradeRequired {
		var refusal protocol.Refusal
		// A refusal this Hub cannot read leaves Speaks empty, and the card names the
		// half it does know. The status is the answer; the versions are the detail.
		_ = json.NewDecoder(io.LimitReader(resp.Body, refusalLimit)).Decode(&refusal)
		r.says(ctx, protocol.HostStateFrame{State: protocol.Incompatible, Speaks: refusal.Speaks}, out)
		return 0
	}
	if resp.StatusCode != http.StatusOK {
		r.failed(ctx, protocol.NoDaemon, out)
		return 0
	}

	// A live connection that says nothing is watched by a timer of its own. A read
	// deadline is not enough: an SSH channel answers SetReadDeadline with "deadline
	// not supported", so the only way to end a read that will never return is to
	// close the connection under it.
	quiet := time.AfterFunc(r.hub.stale, func() { conn.Close() })
	defer quiet.Stop()

	ready := r.read(ctx, beating(quiet, r.hub.stale, resp.Body), out)
	if ready.IsZero() {
		r.failed(ctx, protocol.NoDaemon, out)
		return 0
	}
	return time.Since(ready)
}

// beating pushes the watchdog out every time the Host says anything, and a
// keepalive comment is something. A stream that is open and silent looks exactly
// like a stream that is working, so the silence is the only evidence there is.
func beating(quiet *time.Timer, wait time.Duration, body io.Reader) io.Reader {
	return readerFunc(func(p []byte) (int, error) {
		n, err := body.Read(p)
		if err == nil {
			quiet.Reset(wait)
		}
		return n, err
	})
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

// read splits SSE into Frames. The first Frame must be a valid Hello: an open HTTP
// response proves only that something answered, while the Handshake proves it was
// this Host's Daemon. After that it parses no body, so an Event Kind this build has
// never heard of still reaches the Client.
func (r *hostReader) read(ctx context.Context, body io.Reader, out chan<- daemonFrame) time.Time {
	reader := bufio.NewReader(body)
	frame := daemonFrame{host: r.id}
	var ready time.Time
	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		switch {
		case line == "":
			if frame.name != "" {
				if ready.IsZero() {
					if frame.name != string(protocol.FrameHello) || !validHello(frame.data) {
						return time.Time{}
					}
					if !r.deliver(ctx, frame, out) || !r.says(ctx, protocol.HostStateFrame{State: protocol.Ready}, out) {
						return time.Time{}
					}
					r.failures = 0
					ready = time.Now()
				} else if !r.deliver(ctx, frame, out) {
					return ready
				}
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
			return ready
		}
	}
}

func validHello(data []byte) bool {
	var hello protocol.Hello
	return json.Unmarshal(data, &hello) == nil && hello.Protocol == protocol.Version
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
