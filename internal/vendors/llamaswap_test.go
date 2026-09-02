package vendors

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// The bodies under testdata/llamaswap came from a real llama-swap v251 on
// loopback, fronting llama.cpp b10622.
func llamaSwapFrom(t *testing.T, r *recorded) *LlamaSwapAdapter {
	t.Helper()
	r.dir = "llamaswap"
	return NewLlamaSwap("http://127.0.0.1:8080", r)
}

// llama-swap is why Support has three values. It knows the ids of the Models it
// fronts and nothing else about them until one is loaded.
func TestLlamaSwapReportsUnknownForAnUnloadedModel(t *testing.T) {
	rt := &recorded{body: map[string]string{
		"/v1/models": "models.json",
		"/running":   "running-empty.json",
	}}
	ls := llamaSwapFrom(t, rt)

	models, err := ls.Catalogue(context.Background())
	if err != nil {
		t.Fatalf("Catalogue: %v", err)
	}
	if len(models) != 4 {
		t.Fatalf("Catalogue returned %d Models, want 4", len(models))
	}

	for _, m := range models {
		if m.ID == "" || m.Name != m.ID {
			t.Errorf("Catalogue returned %+v, want the id as both id and name", m)
		}
		if (m.Caps != Capabilities{}) {
			t.Errorf("%s reports %+v, want every Capability Unknown", m.ID, m.Caps)
		}
	}

	// Reading a Capability off an unloaded Model would load it, at 4.4s to 22.5s
	// each, and evict whatever a Session was using.
	for _, path := range rt.paths() {
		if strings.HasPrefix(path, "/upstream/") {
			t.Errorf("Catalogue asked %s about a Model that is not loaded", path)
		}
	}
}

// The answer sharpens for a Model that is already resident, because
// chat_template_caps is computed from the template that will actually run.
func TestLlamaSwapSharpensAResidentModel(t *testing.T) {
	ls := llamaSwapFrom(t, &recorded{body: map[string]string{
		"/v1/models":                         "models-one-loaded.json",
		"/running":                           "running.json",
		"/upstream/qwen2.5-coder-1.5b/props": "props.json",
	}})

	models, err := ls.Catalogue(context.Background())
	if err != nil {
		t.Fatalf("Catalogue: %v", err)
	}

	for _, m := range models {
		if m.ID != "qwen2.5-coder-1.5b" {
			if (m.Caps != Capabilities{}) {
				t.Errorf("%s is not resident and reports %+v, want every Capability Unknown", m.ID, m.Caps)
			}
			continue
		}
		// No flag on /props says whether the Model reasons, so Reasoning stays
		// Unknown even for a Model llama-swap has loaded.
		want := Capabilities{Chat: Yes, Tools: Yes, Reasoning: Unknown, Vision: No}
		if m.Caps != want {
			t.Errorf("the resident Model reports %+v, want %+v", m.Caps, want)
		}
		if m.Quant != "Q8_0" {
			t.Errorf("the resident Model reports Quant %q, want Q8_0", m.Quant)
		}
	}
}

// n_ctx on /props is what this instance was started with, so it is a Resident
// answer and never a TrainedContext.
func TestLlamaSwapNeverReportsATrainedContext(t *testing.T) {
	ls := llamaSwapFrom(t, &recorded{body: map[string]string{
		"/v1/models":                         "models-one-loaded.json",
		"/running":                           "running.json",
		"/upstream/qwen2.5-coder-1.5b/props": "props.json",
	}})

	models, err := ls.Catalogue(context.Background())
	if err != nil {
		t.Fatalf("Catalogue: %v", err)
	}
	for _, m := range models {
		if m.TrainedContext != 0 {
			t.Errorf("%s reports TrainedContext %d, want 0: llama-swap does not carry one", m.ID, m.TrainedContext)
		}
	}
}

func TestLlamaSwapResidentIsWhatIsRunning(t *testing.T) {
	ls := llamaSwapFrom(t, &recorded{body: map[string]string{"/running": "running.json"}})

	resident, err := ls.Resident(context.Background())
	if err != nil {
		t.Fatalf("Resident: %v", err)
	}
	want := Resident{ModelID: "qwen2.5-coder-1.5b"}
	if len(resident) != 1 || resident[0] != want {
		t.Errorf("Resident = %+v, want one %+v", resident, want)
	}
}

func TestLlamaSwapResidentIsEmptyWhenNothingIsRunning(t *testing.T) {
	ls := llamaSwapFrom(t, &recorded{body: map[string]string{"/running": "running-empty.json"}})

	resident, err := ls.Resident(context.Background())
	if err != nil {
		t.Fatalf("Resident: %v", err)
	}
	if len(resident) != 0 {
		t.Errorf("Resident returned %d, want none", len(resident))
	}
}

// llama-swap has no load endpoint. Asking a Model for its own props is what
// starts it, which ADR 0002 established on the development Host.
func TestLlamaSwapLoadAsksTheModelForItsProps(t *testing.T) {
	rt := &recorded{body: map[string]string{"/upstream/qwen2.5-coder-1.5b/props": "props.json"}}
	ls := llamaSwapFrom(t, rt)

	if err := ls.Load(context.Background(), "qwen2.5-coder-1.5b"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := rt.paths(); len(got) != 1 || got[0] != "/upstream/qwen2.5-coder-1.5b/props" {
		t.Errorf("Load called %v, want one /upstream/<id>/props", got)
	}
}

func TestLlamaSwapLoadNamesAModelItDoesNotFront(t *testing.T) {
	ls := llamaSwapFrom(t, &recorded{
		body:   map[string]string{"/upstream/no-such-model-v9/props": "props-missing.json"},
		status: map[string]int{"/upstream/no-such-model-v9/props": http.StatusNotFound},
	})

	err := ls.Load(context.Background(), "no-such-model-v9")
	if !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("Load of a missing Model returned %v, want ErrModelNotFound", err)
	}
	if want := "no-such-model-v9"; !strings.Contains(err.Error(), want) {
		t.Errorf("the error is %q and does not name %q", err, want)
	}
}

// llama-swap answers an unload with the two bytes OK rather than with JSON, so
// nothing here may decode the body.
func TestLlamaSwapUnloadReadsNoBody(t *testing.T) {
	rt := &recorded{body: map[string]string{"/api/models/unload/qwen2.5-coder-1.5b": "unload-ok.txt"}}
	ls := llamaSwapFrom(t, rt)

	if err := ls.Unload(context.Background(), "qwen2.5-coder-1.5b"); err != nil {
		t.Fatalf("Unload: %v", err)
	}
	if rt.seen[0].Method != http.MethodPost {
		t.Errorf("Unload used %s, want POST", rt.seen[0].Method)
	}
}

func TestLlamaSwapUnloadNamesAModelItDoesNotFront(t *testing.T) {
	ls := llamaSwapFrom(t, &recorded{
		body:   map[string]string{"/api/models/unload/no-such-model-v9": "unload-missing.json"},
		status: map[string]int{"/api/models/unload/no-such-model-v9": http.StatusNotFound},
	})

	if err := ls.Unload(context.Background(), "no-such-model-v9"); !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("Unload of a missing Model returned %v, want ErrModelNotFound", err)
	}
}

func TestALlamaSwapThatDoesNotAnswerIsAnError(t *testing.T) {
	ls := llamaSwapFrom(t, &recorded{body: map[string]string{}})

	if _, err := ls.Catalogue(context.Background()); err == nil {
		t.Error("Catalogue against a Vendor that does not answer returned no error")
	}
	if _, err := ls.Resident(context.Background()); err == nil {
		t.Error("Resident against a Vendor that does not answer returned no error")
	}
}

func TestLlamaSwapEndpointIsTheLlamaSwapRoot(t *testing.T) {
	ls := NewLlamaSwap("http://127.0.0.1:8080/", nil)

	want := Endpoint{Kind: LlamaSwap, Base: "http://127.0.0.1:8080"}
	if ls.Endpoint() != want {
		t.Errorf("Endpoint = %+v, want %+v", ls.Endpoint(), want)
	}
}

var _ Adapter = (*LlamaSwapAdapter)(nil)
