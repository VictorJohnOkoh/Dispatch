package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/vendors"
)

// fake is a Vendor that answers whatever the test set. It is the whole of the
// Adapter interface because the poll is the only caller here.
type fake struct {
	endpoint    vendors.Endpoint
	models      []vendors.Model
	resident    []vendors.Resident
	err         error
	residentErr error

	polled chan struct{} // closed by the first Catalogue call
	block  chan struct{} // Catalogue waits on this when it is not nil
	calls  int

	loadErr  error
	mu       sync.Mutex
	loading  chan struct{} // closed when Load starts
	gate     chan struct{} // Load waits on this when it is not nil
	loaded   []string
	unloaded []string
}

func (f *fake) Endpoint() vendors.Endpoint { return f.endpoint }

func (f *fake) Catalogue(ctx context.Context) ([]vendors.Model, error) {
	f.calls++
	if f.polled != nil {
		close(f.polled)
		f.polled = nil
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.models, f.err
}

func (f *fake) Resident(context.Context) ([]vendors.Resident, error) {
	if f.residentErr != nil {
		return nil, f.residentErr
	}
	return f.resident, f.err
}

// Load records what the Daemon asked for, and waits on gate when a test set one,
// which is how a cold Model load is held inside Starting.
func (f *fake) Load(ctx context.Context, modelID string) error {
	f.mu.Lock()
	f.loaded = append(f.loaded, modelID)
	loading := f.loading
	f.loading = nil
	gate := f.gate
	f.mu.Unlock()

	if loading != nil {
		close(loading)
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.loadErr
}

// loads is what Load was called with, so far.
func (f *fake) loads() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.loaded)
}

func (f *fake) Unload(_ context.Context, modelID string) error {
	f.mu.Lock()
	f.unloaded = append(f.unloaded, modelID)
	f.mu.Unlock()
	return f.err
}

// unloads is what Unload was called with, so far.
func (f *fake) unloads() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.unloaded)
}

// errNoModel is a Vendor refusing, which is the only Vendor failure these tests
// need to tell apart from success.
var errNoModel = errors.New("no such model")

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func ollamaFake() *fake {
	return &fake{
		endpoint: vendors.Endpoint{Kind: vendors.Ollama, Base: "http://127.0.0.1:11434"},
		models: []vendors.Model{{
			ID:             "qwen3:8b",
			Name:           "qwen3:8b",
			Caps:           vendors.Capabilities{Chat: vendors.Yes, Tools: vendors.Yes},
			TrainedContext: 40960,
			Quant:          "Q4_K_M",
			DiskBytes:      5_200_000_000,
		}},
		resident: []vendors.Resident{{ModelID: "qwen3:8b", LoadedContext: 4096, VRAM: 6_000_000_000}},
	}
}

func TestABeatFillsTheCatalogueCache(t *testing.T) {
	f := ollamaFake()
	v := newVendors([]vendors.Adapter{f}, quiet())
	v.pollAll(t.Context())

	got := v.Catalogue()
	if len(got) != 1 {
		t.Fatalf("%d lines, want 1", len(got))
	}
	line := got[0]
	if line.Kind != "ollama" || line.At == 0 {
		t.Fatalf("line = %+v", line)
	}
	if len(line.Models) != 1 || line.Models[0].ID != "qwen3:8b" {
		t.Fatalf("models = %+v", line.Models)
	}
	want := CapsView{Chat: "yes", Tools: "yes", Reasoning: "unknown", Vision: "unknown"}
	if line.Models[0].Caps != want {
		t.Errorf("caps = %+v, want %+v", line.Models[0].Caps, want)
	}
}

// A Capability the Vendor did not report is drawn Unknown and never No, which is
// the rule the three values exist for.
func TestACapabilityNobodyReportedIsDrawnUnknown(t *testing.T) {
	f := ollamaFake()
	f.models[0].Caps = vendors.Capabilities{}
	v := newVendors([]vendors.Adapter{f}, quiet())
	v.pollAll(t.Context())

	body, err := json.Marshal(v.Catalogue()[0].Models[0].Caps)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); !strings.Contains(got, `"chat":"unknown"`) {
		t.Errorf("caps = %s", got)
	}
}

// Reachability is never stored and never remembered. A Vendor that stops answering
// empties its line rather than leaving the list it had.
func TestAVendorThatStopsAnsweringEmptiesItsLine(t *testing.T) {
	f := ollamaFake()
	v := newVendors([]vendors.Adapter{f}, quiet())
	v.pollAll(t.Context())

	f.err = errors.New("connection refused")
	v.pollAll(t.Context())

	line := v.Catalogue()[0]
	if len(line.Models) != 0 || line.At != 0 {
		t.Fatalf("line = %+v, want an empty one", line)
	}
	if line.Base != f.endpoint.Base {
		t.Errorf("base = %q, want the configured one", line.Base)
	}
	if frame, _ := v.Watch(); frame[0].Reachable || len(frame[0].Resident) != 0 {
		t.Errorf("frame = %+v, want an empty one", frame[0])
	}
}

// A Vendor that answers one call and not the other is not answering. Drawing it
// reachable with nothing loaded would say nothing is in memory, which is the one
// thing the failed call did not tell us.
func TestHalfABeatIsNotABeat(t *testing.T) {
	f := ollamaFake()
	f.residentErr = errors.New("connection reset")
	v := newVendors([]vendors.Adapter{f}, quiet())
	v.pollAll(t.Context())

	if frame, _ := v.Watch(); frame[0].Reachable {
		t.Errorf("frame = %+v, want an unreachable Vendor", frame[0])
	}
	if line := v.Catalogue()[0]; line.At != 0 || len(line.Models) != 0 {
		t.Errorf("line = %+v, want an empty one", line)
	}
}

func TestTheFrameCarriesReachabilityBesideTheResidentList(t *testing.T) {
	v := newVendors([]vendors.Adapter{ollamaFake()}, quiet())
	v.pollAll(t.Context())

	frame, _ := v.Watch()
	if len(frame) != 1 || !frame[0].Reachable {
		t.Fatalf("frame = %+v", frame)
	}
	if len(frame[0].Resident) != 1 || frame[0].Resident[0].LoadedContext != 4096 {
		t.Errorf("resident = %+v", frame[0].Resident)
	}
}

// The cache is read under the lock the poll writes under, so a reader never waits
// on the Vendor behind it.
func TestAReaderNeverWaitsOnTheVendor(t *testing.T) {
	f := ollamaFake()
	f.block = make(chan struct{})
	f.polled = make(chan struct{})
	polled := f.polled
	v := newVendors([]vendors.Adapter{f}, quiet())

	done := make(chan struct{})
	go func() { defer close(done); v.pollAll(t.Context()) }()
	<-polled

	read := make(chan int, 1)
	go func() { read <- len(v.Catalogue()) }()
	select {
	case n := <-read:
		if n != 1 {
			t.Errorf("%d lines, want 1", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the read waited on the Vendor")
	}
	close(f.block)
	<-done
}

// Run polls once before the first beat, so a request arriving right after start
// meets a cache that has been filled rather than one that is merely empty, and it
// keeps polling on the beat after that.
func TestRunPollsBeforeTheFirstBeatAndOnEveryBeatAfter(t *testing.T) {
	f := &countingFake{fake: *ollamaFake()}
	v := newVendors([]vendors.Adapter{f}, quiet())
	v.every = time.Millisecond

	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	go func() { defer close(stopped); v.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for f.count() < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("%d beats in two seconds, want at least 3", f.count())
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-stopped
}

// countingFake counts its beats, because Run reads them from its own goroutine.
type countingFake struct {
	fake
	mu sync.Mutex
	n  int
}

func (c *countingFake) Catalogue(ctx context.Context) ([]vendors.Model, error) {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	return c.fake.Catalogue(ctx)
}

func (c *countingFake) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// Watch hands out the lines and a channel the next beat closes, so a stream sends
// what it has now and waits for the next change without registering anything.
func TestWatchClosesOnTheNextBeat(t *testing.T) {
	f := ollamaFake()
	v := newVendors([]vendors.Adapter{f}, quiet())

	before, beat := v.Watch()
	if len(before) != 1 || before[0].Reachable {
		t.Fatalf("before the first beat = %+v", before)
	}
	select {
	case <-beat:
		t.Fatal("the beat closed before a poll")
	default:
	}

	v.pollAll(t.Context())
	select {
	case <-beat:
	case <-time.After(2 * time.Second):
		t.Fatal("a poll did not close the beat")
	}

	after, next := v.Watch()
	if len(after) != 1 || !after[0].Reachable {
		t.Errorf("after the beat = %+v", after)
	}
	if next == beat {
		t.Error("Watch handed back the beat that already closed")
	}
}

// A Model id is unique only inside one Vendor. Two Vendors on one Host can both
// answer to the same id, and the start says which one it means.
func TestAStartNamingAVendorGetsThatVendorAndNotTheFirst(t *testing.T) {
	first := ollamaFake()
	second := ollamaFake()
	second.endpoint = vendors.Endpoint{Kind: vendors.LMStudio, Base: "http://127.0.0.1:1234"}
	v := newVendors([]vendors.Adapter{first, second}, quiet())
	v.pollAll(t.Context())

	if got := v.serving(second.endpoint.Base, "qwen3:8b"); got != vendors.Adapter(second) {
		t.Errorf("a start naming the second Vendor got %v", got)
	}
	if got := v.serving("", "qwen3:8b"); got != vendors.Adapter(first) {
		t.Errorf("a start naming no Vendor got %v, want the first that lists the Model", got)
	}
	if got := v.serving("http://127.0.0.1:9999", "qwen3:8b"); got != nil {
		t.Errorf("a start naming a Vendor this Host does not have got %v", got)
	}
}
