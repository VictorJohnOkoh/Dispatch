package hub

import (
	"net/http"
	"sync"

	"github.com/VictorJohnOkoh/Dispatch/internal/hub/internal/hostset"
)

// retries carries a retry the user commands to the readers that hold an
// Incompatible Host. Host State stays in those readers, so this holds no state of
// its own: only one channel per Host, which a retry closes. Every open stream
// that is waiting on that Host wakes, and a reader that is not waiting sees
// nothing.
type retries struct {
	mu      sync.Mutex
	waiting map[hostset.HostID]chan struct{}
}

// wait is what an Incompatible reader blocks on until the user retries its Host.
func (r *retries) wait(id hostset.HostID) <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.waiting == nil {
		r.waiting = make(map[hostset.HostID]chan struct{})
	}
	ch, ok := r.waiting[id]
	if !ok {
		ch = make(chan struct{})
		r.waiting[id] = ch
	}
	return ch
}

func (r *retries) retry(id hostset.HostID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ch, ok := r.waiting[id]; ok {
		close(ch)
		delete(r.waiting, id)
	}
}

// retryHost is ADR 0004's one way out of Incompatible. It answers 202 whatever
// the Host State is, because a retry on a Host that is not Incompatible changes
// nothing, and the Client learns what happened from the stream.
func (h *Hub) retryHost(w http.ResponseWriter, r *http.Request) {
	id := hostset.HostID(r.PathValue("host"))
	if _, ok := h.hosts.Find(id); !ok {
		http.Error(w, errNoHost.Error(), http.StatusNotFound)
		return
	}
	h.retries.retry(id)
	w.WriteHeader(http.StatusAccepted)
}
