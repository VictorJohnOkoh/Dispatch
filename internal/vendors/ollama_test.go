package vendors

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func ollamaFrom(t *testing.T, r *recorded) *OllamaAdapter {
	t.Helper()
	r.dir = "ollama"
	return NewOllama("http://127.0.0.1:11434", r)
}

func TestCatalogueReadsTheModelsOllamaServes(t *testing.T) {
	o := ollamaFrom(t, &recorded{body: map[string]string{"/api/tags": "tags.json"}})

	models, err := o.Catalogue(context.Background())
	if err != nil {
		t.Fatalf("Catalogue: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("Catalogue returned %d Models, want 2", len(models))
	}

	want := Model{
		ID:             "qwen3.5:9b",
		Name:           "qwen3.5:9b",
		Caps:           Capabilities{Chat: Yes, Tools: Yes, Reasoning: Yes, Vision: Yes},
		TrainedContext: 262144,
		Quant:          "Q4_K_M",
		DiskBytes:      6594474711,
	}
	if models[0] != want {
		t.Errorf("Catalogue[0] = %+v, want %+v", models[0], want)
	}
}

// Absent is not No. qwen3:latest lists no vision capability, and an Ollama older
// than v0.30.2 lists none at all. Reading either absence as No would hide a usable
// Model, which is the mistake ADR 0007 made Support three-valued to prevent.
func TestNoCapabilityIsEverReportedAsNo(t *testing.T) {
	o := ollamaFrom(t, &recorded{body: map[string]string{"/api/tags": "tags.json"}})

	models, err := o.Catalogue(context.Background())
	if err != nil {
		t.Fatalf("Catalogue: %v", err)
	}
	for _, m := range models {
		for _, c := range []struct {
			name string
			got  Support
		}{{"Chat", m.Caps.Chat}, {"Tools", m.Caps.Tools}, {"Reasoning", m.Caps.Reasoning}, {"Vision", m.Caps.Vision}} {
			if c.got == No {
				t.Errorf("%s reports %s as No, want Yes or Unknown", m.ID, c.name)
			}
		}
	}

	if got := models[1].Caps.Vision; got != Unknown {
		t.Errorf("qwen3:latest lists no vision, so Vision is %d, want Unknown", got)
	}
}

// An Ollama older than v0.30.2 sends no capabilities key at all.
func TestCapabilitiesAreAllUnknownWhenOllamaSendsNone(t *testing.T) {
	var caps Capabilities
	if got := ollamaCaps(nil); got != caps {
		t.Errorf("ollamaCaps(nil) = %+v, want every field Unknown", got)
	}
}

// Resident is a different answer from Catalogue, and the fixtures prove it: two
// Models on disk, one in memory, and a LoadedContext that is not TrainedContext.
func TestResidentIsADifferentAnswerFromCatalogue(t *testing.T) {
	o := ollamaFrom(t, &recorded{body: map[string]string{
		"/api/tags": "tags.json",
		"/api/ps":   "ps.json",
	}})

	models, err := o.Catalogue(context.Background())
	if err != nil {
		t.Fatalf("Catalogue: %v", err)
	}
	resident, err := o.Resident(context.Background())
	if err != nil {
		t.Fatalf("Resident: %v", err)
	}

	if len(models) == len(resident) {
		t.Fatalf("Catalogue and Resident both returned %d, want different answers", len(models))
	}
	want := Resident{ModelID: "qwen3:latest", LoadedContext: 40960, VRAM: 11194387660}
	if resident[0] != want {
		t.Errorf("Resident[0] = %+v, want %+v", resident[0], want)
	}

	// The same Model on disk was trained for the same 40960, but the bytes in VRAM
	// are twice the bytes on disk, which is what makes these two calls worth having.
	if resident[0].VRAM <= models[1].DiskBytes {
		t.Errorf("VRAM %d is not more than DiskBytes %d", resident[0].VRAM, models[1].DiskBytes)
	}
}

// Nothing loaded and nobody answering are different answers.
func TestResidentReturnsEmptyWhenNothingIsLoaded(t *testing.T) {
	o := ollamaFrom(t, &recorded{body: map[string]string{"/api/ps": "ps-empty.json"}})

	resident, err := o.Resident(context.Background())
	if err != nil {
		t.Fatalf("Resident: %v", err)
	}
	if len(resident) != 0 {
		t.Errorf("Resident returned %d, want none", len(resident))
	}
}

func TestLoadTurnsOffOllamasOwnEvictor(t *testing.T) {
	rt := &recorded{body: map[string]string{"/api/chat": "chat-load-ok.json"}}
	o := ollamaFrom(t, rt)

	if err := o.Load(context.Background(), "qwen3:latest"); err != nil {
		t.Fatalf("Load: %v", err)
	}

	sent := decodeSent(t, rt.seen[0])
	if sent.Model != "qwen3:latest" {
		t.Errorf("Load sent model %q, want qwen3:latest", sent.Model)
	}
	if sent.KeepAlive != -1 {
		t.Errorf("Load sent keep_alive %d, want -1", sent.KeepAlive)
	}
	if len(sent.Messages) != 0 {
		t.Errorf("Load sent %d messages, want an empty chat", len(sent.Messages))
	}
	// /api/chat streams NDJSON unless told not to, and a load wants one answer.
	if sent.Stream {
		t.Error("Load sent stream true, want false")
	}
}

func TestUnloadSendsKeepAliveZero(t *testing.T) {
	rt := &recorded{body: map[string]string{"/api/chat": "chat-unload-ok.json"}}
	o := ollamaFrom(t, rt)

	if err := o.Unload(context.Background(), "qwen3:latest"); err != nil {
		t.Fatalf("Unload: %v", err)
	}
	if got := decodeSent(t, rt.seen[0]).KeepAlive; got != 0 {
		t.Errorf("Unload sent keep_alive %d, want 0", got)
	}
}

func TestLoadNamesAModelOllamaDoesNotHave(t *testing.T) {
	o := ollamaFrom(t, &recorded{
		body:   map[string]string{"/api/chat": "chat-load-missing.json"},
		status: map[string]int{"/api/chat": http.StatusNotFound},
	})

	err := o.Load(context.Background(), "no-such-model:v9")
	if !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("Load of a missing Model returned %v, want ErrModelNotFound", err)
	}
	// The Vendor's own words survive, because they name the Model the user typed.
	if want := "no-such-model:v9"; !strings.Contains(err.Error(), want) {
		t.Errorf("the error is %q and does not name %q", err, want)
	}
}

// The status and the path are the fact. Ollama rewording its own sentence must not
// silently downgrade this to a generic error.
func TestTheNamedErrorDoesNotDependOnOllamasWording(t *testing.T) {
	o := ollamaFrom(t, &recorded{
		body:   map[string]string{"/api/chat": "ps-empty.json"},
		status: map[string]int{"/api/chat": http.StatusNotFound},
	})

	if err := o.Load(context.Background(), "no-such-model:v9"); !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("a 404 from /api/chat returned %v, want ErrModelNotFound", err)
	}
}

// A 404 from a discovery path is a wrong Vendor, not a wrong Model id.
func TestA404FromDiscoveryIsNotAMissingModel(t *testing.T) {
	o := ollamaFrom(t, &recorded{
		body:   map[string]string{"/api/tags": "ps-empty.json"},
		status: map[string]int{"/api/tags": http.StatusNotFound},
	})

	_, err := o.Catalogue(context.Background())
	if err == nil {
		t.Fatal("Catalogue against a 404 returned no error")
	}
	if errors.Is(err, ErrModelNotFound) {
		t.Errorf("Catalogue reported %v as a missing Model", err)
	}
}

// A Vendor is reachable exactly when a call to it succeeds, so a transport that
// cannot dial is an error rather than an empty answer.
func TestAVendorThatDoesNotAnswerIsAnError(t *testing.T) {
	o := ollamaFrom(t, &recorded{body: map[string]string{}})

	if _, err := o.Catalogue(context.Background()); err == nil {
		t.Error("Catalogue against a Vendor that does not answer returned no error")
	}
	if _, err := o.Resident(context.Background()); err == nil {
		t.Error("Resident against a Vendor that does not answer returned no error")
	}
}

func TestEndpointIsTheOllamaRoot(t *testing.T) {
	o := NewOllama("http://127.0.0.1:11434/", nil)

	want := Endpoint{Kind: Ollama, Base: "http://127.0.0.1:11434"}
	if o.Endpoint() != want {
		t.Errorf("Endpoint = %+v, want %+v", o.Endpoint(), want)
	}
}

// The adapter satisfies the interface the Daemon holds.
var _ Adapter = (*OllamaAdapter)(nil)

type sentChat struct {
	Model     string     `json:"model"`
	Messages  []struct{} `json:"messages"`
	Stream    bool       `json:"stream"`
	KeepAlive int        `json:"keep_alive"`
}

func decodeSent(t *testing.T, req *http.Request) sentChat {
	t.Helper()
	var sent sentChat
	if err := json.Unmarshal(sentBody(t, req), &sent); err != nil {
		t.Fatalf("decode the sent body: %v", err)
	}
	return sent
}
