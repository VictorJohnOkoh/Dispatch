package daemon

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/vendors"
)

// pollInterval is the Vendor poll's beat. One beat asks every Vendor for both of
// its lists, because they travel together and a second timer would only be a
// second thing to get wrong.
const pollInterval = 5 * time.Second

// pollTimeout bounds one Vendor's beat. The Vendors are polled side by side, so a
// Vendor that hangs delays its own line and no other.
const pollTimeout = 3 * time.Second

// Vendors is the Daemon's whole view of the Vendors on this Host: the goroutine
// that polls them, the Catalogue cache GET /v1/models answers from, and the
// content of the vendors frame.
//
// Reachability is not a value a Vendor returns and it is not stored. A beat that
// fails empties that Vendor's line, so the Host card's Vendor row empties rather
// than going stale.
type Vendors struct {
	adapters []vendors.Adapter
	every    time.Duration
	log      *slog.Logger

	mu    sync.Mutex
	beats []beat // one per adapter, in the order the config named them

	// beat is closed and replaced by every poll, so any number of streams wait on
	// the change without registering. A stream that is slow misses beats and reads
	// the state after them, which is right for a fact that is worthless when old.
	beat chan struct{}

	// polled is closed after the first beat, so a server can wait for a Catalogue
	// that has been filled before it answers a request that reads one.
	polled chan struct{}
	first  sync.Once
}

// beat is what the last poll saw of one Vendor. The zero value is a Vendor that is
// not answering.
type beat struct {
	models []vendors.Model

	// resident is held only so the first vendors frame on a new stream carries the
	// whole current state, which ADR 0009 asks for. It is what the last beat pushed
	// rather than an answer to a fetch, so ADR 0007's rule that a Resident list is
	// never cached still holds.
	resident []vendors.Resident

	at time.Time
}

func newVendors(adapters []vendors.Adapter, log *slog.Logger) *Vendors {
	return &Vendors{
		adapters: adapters,
		every:    pollInterval,
		log:      log,
		beats:    make([]beat, len(adapters)),
		beat:     make(chan struct{}),
		polled:   make(chan struct{}),
	}
}

// Run polls every Vendor on the beat until ctx is done. The first poll happens
// before the first tick, so a request arriving right after start meets a cache
// that has been filled rather than one that is merely empty.
func (v *Vendors) Run(ctx context.Context) {
	t := time.NewTicker(v.every)
	defer t.Stop()
	for {
		v.pollAll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// pollAll asks every Vendor at once, so one Vendor that hangs costs the beat
// pollTimeout rather than costing every Vendor behind it its own.
func (v *Vendors) pollAll(ctx context.Context) {
	next := make([]beat, len(v.adapters))
	var wg sync.WaitGroup
	for i, a := range v.adapters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next[i] = v.pollOne(ctx, a)
		}()
	}
	wg.Wait()

	v.mu.Lock()
	v.beats = next
	close(v.beat)
	v.beat = make(chan struct{})
	v.mu.Unlock()

	v.first.Do(func() { close(v.polled) })
}

// pollOne asks one Vendor for both of its lists under one deadline. Either call
// failing returns the zero beat, because a Vendor is reachable exactly when a call
// to it succeeds, and half a beat would draw a Vendor that is not answering as one
// with nothing loaded.
func (v *Vendors) pollOne(parent context.Context, a vendors.Adapter) beat {
	ctx, cancel := context.WithTimeout(parent, pollTimeout)
	defer cancel()

	base := a.Endpoint().Base
	models, err := a.Catalogue(ctx)
	if err != nil {
		v.warn(parent, base, "catalogue", err)
		return beat{}
	}
	resident, err := a.Resident(ctx)
	if err != nil {
		v.warn(parent, base, "resident", err)
		return beat{}
	}
	return beat{models: models, resident: resident, at: time.Now()}
}

// warn reports a Vendor that did not answer. A shutdown that cancels a call in
// flight is not a Vendor failure and is not logged as one.
func (v *Vendors) warn(parent context.Context, base, call string, err error) {
	if parent.Err() == nil {
		v.log.Warn("vendor poll failed", "base", base, "call", call, "err", err)
	}
}

// Catalogue is what GET /v1/models answers, drawn from the last beat. Every
// configured Vendor has a line whether or not it is answering.
func (v *Vendors) Catalogue() []CatalogueView {
	v.mu.Lock()
	defer v.mu.Unlock()

	out := make([]CatalogueView, len(v.adapters))
	for i, b := range v.beats {
		out[i] = CatalogueView{VendorLine: v.line(i), At: micros(b.at), Models: modelViews(b.models)}
	}
	return out
}

// Polled is closed once a beat has been taken, whether or not any Vendor
// answered it. A Vendor that is not answering has a line, so waiting on this
// never waits on a Vendor that is down.
func (v *Vendors) Polled() <-chan struct{} { return v.polled }

// serving is the Vendor whose last beat listed this Model, or nil when no Vendor
// on this Host did. It reads the poll's cache, so a Session start refuses an
// unknown Model without a call on the request path, and a Vendor that is not
// answering serves nothing.
func (v *Vendors) serving(model string) vendors.Adapter {
	v.mu.Lock()
	defer v.mu.Unlock()

	for i, b := range v.beats {
		for _, m := range b.models {
			if m.ID == model {
				return v.adapters[i]
			}
		}
	}
	return nil
}

// Frame is the content of the vendors frame: reachability beside what is in memory
// now. It is pushed on the beat rather than fetched, because a Resident list is
// worthless when old.
func (v *Vendors) Frame() []VendorView {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.frame()
}

// Watch is the Vendor lines as they stand and a channel the next beat closes. It
// is how a stream sends the whole current state first and a change after that.
func (v *Vendors) Watch() ([]VendorView, <-chan struct{}) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.frame(), v.beat
}

// frame builds the lines. The caller holds the mutex.
func (v *Vendors) frame() []VendorView {
	out := make([]VendorView, len(v.adapters))
	for i, b := range v.beats {
		out[i] = VendorView{
			VendorLine: v.line(i),
			Reachable:  !b.at.IsZero(),
			Resident:   residentViews(b.resident),
		}
	}
	return out
}

// line names one Vendor. Both answers carry it, and it is built in one place so
// the two cannot name the same Vendor differently.
func (v *Vendors) line(i int) VendorLine {
	e := v.adapters[i].Endpoint()
	return VendorLine{Kind: e.Kind.String(), Base: e.Base}
}

// VendorLine says which Vendor a line is about.
type VendorLine struct {
	Kind string `json:"kind"`
	Base string `json:"base"`
}

// CatalogueView is one Vendor's line in the answer to GET /v1/models. It carries
// no reachability field: that belongs to the vendors frame, and two fields for one
// fact are two fields that can disagree.
type CatalogueView struct {
	VendorLine

	// At is when this list was true, in Unix microseconds, and 0 when no beat has
	// filled it. It is the stamp a Client puts on this answer once it is holding it
	// against a Host that has stopped answering, which is what Stale means.
	At int64 `json:"at"`

	Models []ModelView `json:"models"`
}

// VendorView is one Vendor's line in the vendors frame.
type VendorView struct {
	VendorLine
	Reachable bool           `json:"reachable"`
	Resident  []ResidentView `json:"resident"`
}

type ModelView struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Caps           CapsView `json:"caps"`
	TrainedContext int      `json:"trainedContext"`
	Quant          string   `json:"quant,omitempty"`
	DiskBytes      int64    `json:"diskBytes"`
}

// CapsView spells the four answers. Each is "yes", "no" or "unknown", and never a
// bool, because a Vendor that carries no answer is not a Vendor that said no.
type CapsView struct {
	Chat      string `json:"chat"`
	Tools     string `json:"tools"`
	Reasoning string `json:"reasoning"`
	Vision    string `json:"vision"`
}

type ResidentView struct {
	ModelID       string `json:"modelId"`
	LoadedContext int    `json:"loadedContext"`
	VRAM          int64  `json:"vram"`
}

// supportNames spells the three answers, indexed by the Support.
var supportNames = [...]string{vendors.Unknown: "unknown", vendors.No: "no", vendors.Yes: "yes"}

func modelViews(models []vendors.Model) []ModelView {
	out := make([]ModelView, len(models))
	for i, m := range models {
		out[i] = ModelView{
			ID:             m.ID,
			Name:           m.Name,
			Caps:           CapsView{supportNames[m.Caps.Chat], supportNames[m.Caps.Tools], supportNames[m.Caps.Reasoning], supportNames[m.Caps.Vision]},
			TrainedContext: m.TrainedContext,
			Quant:          m.Quant,
			DiskBytes:      m.DiskBytes,
		}
	}
	return out
}

func residentViews(resident []vendors.Resident) []ResidentView {
	out := make([]ResidentView, len(resident))
	for i, r := range resident {
		out[i] = ResidentView{ModelID: r.ModelID, LoadedContext: r.LoadedContext, VRAM: r.VRAM}
	}
	return out
}

func micros(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMicro()
}
