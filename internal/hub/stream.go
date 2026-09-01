package hub

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
	"github.com/VictorJohnOkoh/Dispatch/internal/protocol"
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
		readers.Add(1)
		go func(id hostset.HostID) {
			defer readers.Done()
			h.readStream(r.Context(), id, at, resuming, frames)
		}(host.ID)
	}
	go func() {
		readers.Wait()
		close(frames)
	}()

	for frame := range frames {
		if frame.id != "" {
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

func (h *Hub) readStream(ctx context.Context, id hostset.HostID, at protocol.Cursor, resuming bool, out chan<- daemonFrame) {
	conn, err := h.dialer.Dial(ctx, id)
	if err != nil {
		return
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://daemon/v1/events", nil)
	req.Header.Set(protocol.VersionHeader, fmt.Sprint(protocol.Version))
	if resuming {
		req.Header.Set(protocol.CursorHeader, at.String())
	}
	if err := req.Write(conn); err != nil {
		return
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	readFrames(ctx, id, resp.Body, out)
}

func readFrames(ctx context.Context, host hostset.HostID, body io.Reader, out chan<- daemonFrame) {
	scan := bufio.NewScanner(body)
	frame := daemonFrame{host: host}
	for scan.Scan() {
		line := scan.Text()
		switch {
		case line == "":
			if frame.name != "" {
				select {
				case out <- frame:
				case <-ctx.Done():
					return
				}
			}
			frame = daemonFrame{host: host}
		case strings.HasPrefix(line, "id:"):
			frame.id = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "event:"):
			frame.name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			frame.data = append(frame.data, strings.TrimSpace(strings.TrimPrefix(line, "data:"))...)
		}
	}
}

func addHost(data []byte, host hostset.HostID) []byte {
	if len(data) == 0 || data[0] != '{' {
		return data
	}
	prefix := fmt.Sprintf(`{"host":%q,`, host)
	return append([]byte(prefix), data[1:]...)
}
