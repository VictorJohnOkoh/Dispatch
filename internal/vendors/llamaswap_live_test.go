package vendors

import (
	"context"
	"errors"
	"os"
	"testing"
)

// The fixtures in testdata/llamaswap came from this test's endpoints. It skips
// unless a human points DISPATCH_LLAMASWAP_LIVE at a running llama-swap, because
// everything else in this package must pass with no network.
//
// DISPATCH_LLAMASWAP_MODEL names a Model in llama-swap's config. Loading it starts
// a llama-server, which ran 4.4s for a 1.5B Q8_0 on the development Host.
//
//	DISPATCH_LLAMASWAP_LIVE=http://127.0.0.1:8080 DISPATCH_LLAMASWAP_MODEL=qwen2.5-coder-1.5b go test ./internal/vendors/ -run Live -v
func TestLiveLlamaSwap(t *testing.T) {
	base := os.Getenv("DISPATCH_LLAMASWAP_LIVE")
	if base == "" {
		t.Skip("set DISPATCH_LLAMASWAP_LIVE to run this against a real llama-swap")
	}
	model := os.Getenv("DISPATCH_LLAMASWAP_MODEL")
	ctx := context.Background()
	ls := NewLlamaSwap(base, nil)

	models, err := ls.Catalogue(ctx)
	if err != nil {
		t.Fatalf("Catalogue: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("Catalogue returned no Models, so this llama-swap fronts none")
	}
	for _, m := range models {
		t.Logf("%+v", m)
	}

	if model == "" {
		t.Skip("set DISPATCH_LLAMASWAP_MODEL to exercise Load, Resident and Unload")
	}

	// A Model this llama-swap has not loaded answers Unknown to everything, which
	// is the value llama-swap is in v1 to fill.
	if err := ls.Unload(ctx, model); err != nil {
		t.Fatalf("Unload %s before the Load: %v", model, err)
	}
	cold, err := ls.Catalogue(ctx)
	if err != nil {
		t.Fatalf("Catalogue: %v", err)
	}
	for _, m := range cold {
		if m.ID == model && m.Caps != (Capabilities{}) {
			t.Errorf("%s is not loaded and reports %+v, want every Capability Unknown", m.ID, m.Caps)
		}
	}

	if err := ls.Load(ctx, model); err != nil {
		t.Fatalf("Load %s: %v", model, err)
	}
	resident, err := ls.Resident(ctx)
	if err != nil {
		t.Fatalf("Resident: %v", err)
	}
	if len(resident) == 0 {
		t.Fatal("Resident is empty after a Load that returned without error")
	}
	for _, r := range resident {
		t.Logf("%+v", r)
	}

	// The answer sharpens once the Model is resident, and only then.
	warm, err := ls.Catalogue(ctx)
	if err != nil {
		t.Fatalf("Catalogue: %v", err)
	}
	for _, m := range warm {
		if m.ID == model && m.Caps == (Capabilities{}) {
			t.Errorf("%s is loaded and still reports every Capability Unknown", m.ID)
		}
	}

	if err := ls.Load(ctx, "no-such-model-v9"); !errors.Is(err, ErrModelNotFound) {
		t.Errorf("Load of a Model this llama-swap does not front returned %v, want ErrModelNotFound", err)
	}
	if err := ls.Unload(ctx, model); err != nil {
		t.Errorf("Unload %s: %v", model, err)
	}
}
