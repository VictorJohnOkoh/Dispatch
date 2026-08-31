package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
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
func (f *fake) Load(context.Context, string) error   { return f.err }
func (f *fake) Unload(context.Context, string) error { return f.err }

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
	if frame := v.Frame()[0]; frame.Reachable || len(frame.Resident) != 0 {
		t.Errorf("frame = %+v, want an empty one", frame)
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

	if frame := v.Frame()[0]; frame.Reachable {
		t.Errorf("frame = %+v, want an unreachable Vendor", frame)
	}
	if line := v.Catalogue()[0]; line.At != 0 || len(line.Models) != 0 {
		t.Errorf("line = %+v, want an empty one", line)
	}
}

func TestTheFrameCarriesReachabilityBesideTheResidentList(t *testing.T) {
	v := newVendors([]vendors.Adapter{ollamaFake()}, quiet())
	v.pollAll(t.Context())

	frame := v.Frame()
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
