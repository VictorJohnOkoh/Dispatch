package vendors

import (
	"context"
	"errors"
	"os"
	"testing"
)

// The fixtures in testdata/ollama came from this test. It skips unless a human
// points DISPATCH_OLLAMA_LIVE at a running Ollama, because everything else in this
// package must pass with no network. DISPATCH_OLLAMA_MODEL names a Model that
// Ollama has; the Load it runs pulls the weights into memory and takes as long as
// that takes.
//
//	DISPATCH_OLLAMA_LIVE=http://127.0.0.1:11434 DISPATCH_OLLAMA_MODEL=qwen3:latest go test ./internal/vendors/ -run Live -v
func TestLiveOllama(t *testing.T) {
	base := os.Getenv("DISPATCH_OLLAMA_LIVE")
	if base == "" {
		t.Skip("set DISPATCH_OLLAMA_LIVE to run this against a real Ollama")
	}
	model := os.Getenv("DISPATCH_OLLAMA_MODEL")
	ctx := context.Background()
	o := NewOllama(base, nil)

	models, err := o.Catalogue(ctx)
	if err != nil {
		t.Fatalf("Catalogue: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("Catalogue returned no Models, so this Ollama is serving none")
	}
	for _, m := range models {
		t.Logf("%+v", m)
	}

	if model == "" {
		t.Skip("set DISPATCH_OLLAMA_MODEL to exercise Load, Resident and Unload")
	}

	if err := o.Load(ctx, model); err != nil {
		t.Fatalf("Load %s: %v", model, err)
	}
	resident, err := o.Resident(ctx)
	if err != nil {
		t.Fatalf("Resident: %v", err)
	}
	if len(resident) == 0 {
		t.Fatal("Resident is empty after a Load that returned without error")
	}
	for _, r := range resident {
		t.Logf("%+v", r)
	}

	if err := o.Load(ctx, "no-such-model:v9"); !errors.Is(err, ErrModelNotFound) {
		t.Errorf("Load of a Model this Ollama does not have returned %v, want ErrModelNotFound", err)
	}
	if err := o.Unload(ctx, model); err != nil {
		t.Errorf("Unload %s: %v", model, err)
	}
}
