package vendors

import (
	"context"
	"errors"
	"os"
	"testing"
)

// The fixtures in testdata/lmstudio came from this test's endpoints. It skips
// unless a human points DISPATCH_LMSTUDIO_LIVE at a running LM Studio, because
// everything else in this package must pass with no network. LM Studio's server
// is started once from its own application, which is Host setup and not something
// the Daemon ever does.
//
// DISPATCH_LMSTUDIO_MODEL names a Model that LM Studio has downloaded; the Load it
// runs pulls the weights into memory and takes as long as that takes.
//
//	DISPATCH_LMSTUDIO_LIVE=http://127.0.0.1:1234 DISPATCH_LMSTUDIO_MODEL=qwen2.5-coder-1.5b-instruct go test ./internal/vendors/ -run Live -v
func TestLiveLMStudio(t *testing.T) {
	base := os.Getenv("DISPATCH_LMSTUDIO_LIVE")
	if base == "" {
		t.Skip("set DISPATCH_LMSTUDIO_LIVE to run this against a real LM Studio")
	}
	model := os.Getenv("DISPATCH_LMSTUDIO_MODEL")
	ctx := context.Background()
	lm := NewLMStudio(base, nil)

	models, err := lm.Catalogue(ctx)
	if err != nil {
		t.Fatalf("Catalogue: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("Catalogue returned no Models, so this LM Studio has none downloaded")
	}

	// This Vendor is the only one in v1 that ever answers Yes.
	answered := false
	for _, m := range models {
		t.Logf("%+v", m)
		if m.Caps.Tools == Yes {
			answered = true
		}
	}
	if !answered {
		t.Error("no Model answered Yes to Tools, which is the value only LM Studio fills")
	}

	if model == "" {
		t.Skip("set DISPATCH_LMSTUDIO_MODEL to exercise Load, Resident and Unload")
	}

	if err := lm.Load(ctx, model); err != nil {
		t.Fatalf("Load %s: %v", model, err)
	}
	resident, err := lm.Resident(ctx)
	if err != nil {
		t.Fatalf("Resident: %v", err)
	}
	if len(resident) == 0 {
		t.Fatal("Resident is empty after a Load that returned without error")
	}
	for _, r := range resident {
		t.Logf("%+v", r)
	}

	if err := lm.Load(ctx, "no-such-model-v9"); !errors.Is(err, ErrModelNotFound) {
		t.Errorf("Load of a Model this LM Studio does not have returned %v, want ErrModelNotFound", err)
	}
	if err := lm.Unload(ctx, model); err != nil {
		t.Errorf("Unload %s: %v", model, err)
	}
}
