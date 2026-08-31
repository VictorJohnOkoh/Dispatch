package admission

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/vendors"
)

// single is the one policy v1 ships, and naming it as a Policy is the assertion
// that it still is one.
var single Policy = SingleSession{}

// A Host with nothing running admits, which is the only way a first Session ever
// starts.
func TestSingleSessionAdmitsAnIdleHost(t *testing.T) {
	got := single.Admit(context.Background(), Request{Harness: "opencode", Model: "qwen3:8b"})
	if got != nil {
		t.Fatalf("refused an idle Host: %+v", got)
	}
}

// The refusal names the Session holding the slot, because that is what lets the
// Client offer "stop that one and start this one" as one action, and that offer is
// the whole argument against a queue.
func TestSingleSessionNamesTheBlockingSession(t *testing.T) {
	req := Request{
		Harness: "pi",
		Model:   "qwen3:8b",
		Live: []Live{{
			Session: "s-7f3a2c",
			Harness: "opencode",
			Model:   "llama3.1:8b",
			Since:   time.Now(),
		}},
	}

	got := single.Admit(context.Background(), req)
	if got == nil {
		t.Fatal("admitted a second Session on a busy Host")
	}
	if got.Reason == "" {
		t.Error("the Refusal carries no reason, and it reaches the user unchanged")
	}
	if want := []event.SessionID{"s-7f3a2c"}; !slices.Equal(got.Blocking, want) {
		t.Errorf("Blocking = %v, want %v", got.Blocking, want)
	}
}

// A Daemon that somehow holds two live Sessions names both of them, because the
// user has to stop every one before this start can succeed.
func TestSingleSessionNamesEveryLiveSession(t *testing.T) {
	req := Request{Live: []Live{{Session: "s-one"}, {Session: "s-two"}}}

	got := single.Admit(context.Background(), req)
	if got == nil {
		t.Fatal("admitted a third Session")
	}
	if want := []event.SessionID{"s-one", "s-two"}; !slices.Equal(got.Blocking, want) {
		t.Errorf("Blocking = %v, want %v", got.Blocking, want)
	}
}

// There is no configurable count and no second condition. A Request carrying every
// other field still admits, so nothing here has grown a hidden rule.
func TestSingleSessionReadsOnlyLive(t *testing.T) {
	req := Request{
		Harness: "opencode",
		Model:   "qwen3:8b",
		Vendor:  stubVendor{},
		Dir:     "/home/v/work/project",
	}

	if got := single.Admit(context.Background(), req); got != nil {
		t.Fatalf("refused for something that is not a live Session: %+v", got)
	}
}

// Blocking is empty when stopping something would not help, and the policy that
// proves it is the one ADR 0010 calls a written bet: a VRAM policy reads the Vendor
// off the Request rather than off a new field, and refuses a Model that will not fit
// however many Sessions the user stops.
func TestARefusalCanNameNothingToStop(t *testing.T) {
	got := (vramAware{}).Admit(context.Background(), Request{Model: "huge", Vendor: stubVendor{}})
	if got == nil {
		t.Fatal("admitted a Model that does not fit")
	}
	if len(got.Blocking) != 0 {
		t.Errorf("Blocking = %v, want nothing to stop", got.Blocking)
	}
}

// vramAware is not shipped and is not meant to be. It exists to check that the
// Request already carries what a later policy needs, which is the bet Request was
// shaped around.
type vramAware struct{}

func (vramAware) Admit(ctx context.Context, req Request) *Refusal {
	catalogue, err := req.Vendor.Catalogue(ctx)
	if err != nil {
		return &Refusal{Reason: "the Vendor is not answering"}
	}
	for _, m := range catalogue {
		if m.ID == req.Model && m.DiskBytes > 1<<40 {
			return &Refusal{Reason: "this Model does not fit on this Host"}
		}
	}
	return nil
}

// stubVendor is the smallest thing that is a vendors.Adapter. Only Catalogue
// answers, because it is the only call any policy here makes.
type stubVendor struct{ vendors.Adapter }

func (stubVendor) Catalogue(context.Context) ([]vendors.Model, error) {
	return []vendors.Model{{ID: "huge", DiskBytes: 1 << 41}}, nil
}
