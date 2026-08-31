package daemon

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/vendors"
)

// pollInterval is the Vendor poll's beat. One beat asks every Vendor for both of
// its lists, because they travel together in the vendors frame and a second timer
// would only mean a second thing to get wrong.
const pollInterval = 5 * time.Second

// pollTimeout bounds one Vendor's beat. A Vendor that hangs delays the next beat
// by this much and delays no request at all.
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
	log      *slog.Logger

	mu    sync.Mutex
	beats []beat // one per adapter, in the order the config named them
}

// beat is what the last poll saw of one Vendor. The zero value is a Vendor that is
// not answering, which is why nothing here is a pointer.
type beat struct {
	models   []vendors.Model
	resident []vendors.Resident
	at       time.Time
}

func newVendors(adapters []vendors.Adapter, log *slog.Logger) *Vendors {
	return &Vendors{adapters: adapters, log: log, beats: make([]beat, len(adapters))}
}

// Run polls every Vendor on the beat until ctx is done. The first poll happens
// before the first tick, so a request arriving right after start meets a cache
// that has been filled rather than one that is merely empty.
func (v *Vendors) Run(ctx context.Context) {
	t := time.NewTicker(pollInterval)
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

func (v *Vendors) pollAll(ctx context.Context) {
	next := make([]beat, len(v.adapters))
	for i, a := range v.adapters {
		next[i] = v.pollOne(ctx, a)
	}
	v.mu.Lock()
	v.beats = next
	v.mu.Unlock()
}

// pollOne asks one Vendor for both of its lists under one deadline. A failed
// Catalogue returns the zero beat: it is the call this Daemon reads reachability
// from, and an unreachable Vendor's line is empty rather than old.
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
		e := v.adapters[i].Endpoint()
		out[i] = CatalogueView{
			Kind:      e.Kind.String(),
			Base:      e.Base,
			Reachable: !b.at.IsZero(),
			At:        micros(b.at),
			Models:    modelViews(b.models),
		}
	}
	return out
}

// Frame is the content of the vendors frame: reachability beside what is in memory
// now. It is pushed on the beat rather than fetched, because a Resident list is
// worthless when old.
func (v *Vendors) Frame() []VendorView {
	v.mu.Lock()
	defer v.mu.Unlock()

	out := make([]VendorView, len(v.adapters))
	for i, b := range v.beats {
		e := v.adapters[i].Endpoint()
		out[i] = VendorView{
			Kind:      e.Kind.String(),
			Base:      e.Base,
			Reachable: !b.at.IsZero(),
			Resident:  residentViews(b.resident),
		}
	}
	return out
}

// CatalogueView is one Vendor's line in the answer to GET /v1/models.
type CatalogueView struct {
	Kind      string `json:"kind"`
	Base      string `json:"base"`
	Reachable bool   `json:"reachable"`

	// At is when this list was true, in Unix microseconds, and 0 when no beat has
	// filled it. It is what a Client stamps a Stale catalogue with.
	At int64 `json:"at"`

	Models []ModelView `json:"models"`
}

// VendorView is one Vendor's line in the vendors frame.
type VendorView struct {
	Kind      string         `json:"kind"`
	Base      string         `json:"base"`
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
// bool, because a Vendor that carries no answer is not the same as one that said
// no.
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

// supportNames spells the three answers. There are exactly three and they are
// numbered from zero, so it is an array indexed by the Support.
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
