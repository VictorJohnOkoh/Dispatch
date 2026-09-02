package vendors

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// The bodies under testdata/lmstudio came from a real LM Studio 0.4.21 on
// loopback. One listing answers both Catalogue and Resident, which is why almost
// every test here names one fixture.
func lmStudioFrom(t *testing.T, r *recorded) *LMStudioAdapter {
	t.Helper()
	r.dir = "lmstudio"
	return NewLMStudio("http://127.0.0.1:1234", r)
}

func lmModels(name string) *recorded {
	return &recorded{body: map[string]string{"/api/v1/models": name}}
}

// modelByID scans rather than indexes, for the reason isResident does: a listing
// is a handful of Models and a test reads three of them.
func modelByID(models []Model, id string) (Model, bool) {
	for _, m := range models {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// LM Studio is the only Vendor in v1 that ever answers Yes, and the only one that
// answers No. Both matter: Yes shows the Model in an agent Session's picker, No
// hides it, and Unknown shows it with the gap visible.
func TestLMStudioAnswersEveryCapabilityValue(t *testing.T) {
	lm := lmStudioFrom(t, lmModels("models.json"))

	models, err := lm.Catalogue(context.Background())
	if err != nil {
		t.Fatalf("Catalogue: %v", err)
	}
	for _, c := range []struct {
		id   string
		want Capabilities
	}{
		// trained_for_tool_use true, vision true, and a reasoning object.
		{"qwen/qwen3.5-9b", Capabilities{Chat: Yes, Tools: Yes, Reasoning: Yes, Vision: Yes}},
		// The same two booleans false, and no reasoning key at all.
		{"qwen2.5-coder-1.5b-instruct", Capabilities{Chat: Yes, Tools: No, Reasoning: Unknown, Vision: No}},
		// An embedding Model cannot back a Session, and LM Studio says so in type.
		{"text-embedding-nomic-embed-text-v1.5", Capabilities{Chat: No, Tools: Unknown, Reasoning: Unknown, Vision: Unknown}},
	} {
		m, ok := modelByID(models, c.id)
		if !ok {
			t.Fatalf("Catalogue did not return %s", c.id)
		}
		if m.Caps != c.want {
			t.Errorf("%s reports %+v, want %+v", c.id, m.Caps, c.want)
		}
	}
}

// gpt-oss-20b reasons and LM Studio sends no reasoning key for it. Reading that
// absence as No would hide a reasoning Model from a picker that filters on it.
func TestAMissingLMStudioCapabilityIsUnknownAndNeverNo(t *testing.T) {
	lm := lmStudioFrom(t, lmModels("models.json"))

	models, err := lm.Catalogue(context.Background())
	if err != nil {
		t.Fatalf("Catalogue: %v", err)
	}
	for _, m := range models {
		if m.ID == "gpt-oss-20b" && m.Caps.Reasoning != Unknown {
			t.Errorf("gpt-oss-20b reports Reasoning %d, want Unknown", m.Caps.Reasoning)
		}
	}

	// An LM Studio below 0.4.0 serves no capabilities object at all.
	if got := (lmStudioCaps("llm", nil)); got != (Capabilities{Chat: Yes}) {
		t.Errorf("a Model with no capabilities object reports %+v, want Chat Yes and the rest Unknown", got)
	}
}

func TestLMStudioCatalogueReadsTheRestOfTheListing(t *testing.T) {
	lm := lmStudioFrom(t, lmModels("models.json"))

	models, err := lm.Catalogue(context.Background())
	if err != nil {
		t.Fatalf("Catalogue: %v", err)
	}

	want := Model{
		ID:             "qwen2.5-coder-1.5b-instruct",
		Name:           "Qwen2.5 Coder 1.5B Instruct",
		Caps:           Capabilities{Chat: Yes, Tools: No, Vision: No},
		TrainedContext: 32768,
		Quant:          "Q8_0",
		DiskBytes:      1646573056,
	}
	for _, m := range models {
		if m.ID == want.ID && m != want {
			t.Errorf("Catalogue returned %+v, want %+v", m, want)
		}
	}
}

// One call answers both questions, and the answers differ: several Models on
// disk, one of them in memory, at a context that is not its trained maximum.
func TestLMStudioResidentIsADifferentAnswerFromCatalogue(t *testing.T) {
	lm := lmStudioFrom(t, lmModels("models.json"))

	models, err := lm.Catalogue(context.Background())
	if err != nil {
		t.Fatalf("Catalogue: %v", err)
	}
	resident, err := lm.Resident(context.Background())
	if err != nil {
		t.Fatalf("Resident: %v", err)
	}

	if len(models) == len(resident) {
		t.Fatalf("Catalogue and Resident both returned %d, want different answers", len(models))
	}
	want := Resident{ModelID: "qwen2.5-coder-1.5b-instruct", LoadedContext: 32768}
	if len(resident) != 1 || resident[0] != want {
		t.Errorf("Resident = %+v, want one %+v", resident, want)
	}
}

// LM Studio holds two instances of one Model, which the Daemon never causes and
// must still read.
func TestLMStudioResidentRepeatsAModelLoadedTwice(t *testing.T) {
	lm := lmStudioFrom(t, lmModels("models-two-instances.json"))

	resident, err := lm.Resident(context.Background())
	if err != nil {
		t.Fatalf("Resident: %v", err)
	}
	if len(resident) != 2 {
		t.Fatalf("Resident returned %d instances, want 2", len(resident))
	}
	if resident[0].ModelID != resident[1].ModelID {
		t.Errorf("Resident returned %s and %s, want one Model twice", resident[0].ModelID, resident[1].ModelID)
	}
}

func TestLMStudioResidentIsEmptyWhenNothingIsLoaded(t *testing.T) {
	lm := lmStudioFrom(t, lmModels("models-none-loaded.json"))

	resident, err := lm.Resident(context.Background())
	if err != nil {
		t.Fatalf("Resident: %v", err)
	}
	if len(resident) != 0 {
		t.Errorf("Resident returned %d, want none", len(resident))
	}
}

func TestLMStudioLoadNamesTheModelAndNothingElse(t *testing.T) {
	rt := &recorded{body: map[string]string{"/api/v1/models/load": "load-ok.json"}}
	lm := lmStudioFrom(t, rt)

	if err := lm.Load(context.Background(), "qwen2.5-coder-1.5b-instruct"); err != nil {
		t.Fatalf("Load: %v", err)
	}

	var sent map[string]any
	if err := json.Unmarshal(sentBody(t, rt.seen[0]), &sent); err != nil {
		t.Fatalf("decode the sent body: %v", err)
	}
	if sent["model"] != "qwen2.5-coder-1.5b-instruct" {
		t.Errorf("Load sent %v, want the Model id", sent["model"])
	}
	// An explicit load carries no ttl, which is what exempts it from the JIT
	// timer and from Auto-Evict.
	if _, ok := sent["ttl"]; ok {
		t.Error("Load sent a ttl, want none")
	}
}

func TestLMStudioLoadNamesAModelItDoesNotHave(t *testing.T) {
	lm := lmStudioFrom(t, &recorded{
		body:   map[string]string{"/api/v1/models/load": "load-missing.json"},
		status: map[string]int{"/api/v1/models/load": http.StatusNotFound},
	})

	err := lm.Load(context.Background(), "no-such-model-v9")
	if !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("Load of a missing Model returned %v, want ErrModelNotFound", err)
	}
	if want := "no-such-model-v9"; !strings.Contains(err.Error(), want) {
		t.Errorf("the error is %q and does not name %q", err, want)
	}
}

// The Daemon's intent is that the VRAM comes back, so one Unload unloads every
// instance of that Model by its own instance id.
func TestLMStudioUnloadUnloadsEveryInstance(t *testing.T) {
	rt := &recorded{body: map[string]string{
		"/api/v1/models":        "models-two-instances.json",
		"/api/v1/models/unload": "unload-ok.json",
	}}
	lm := lmStudioFrom(t, rt)

	if err := lm.Unload(context.Background(), "qwen2.5-coder-1.5b-instruct"); err != nil {
		t.Fatalf("Unload: %v", err)
	}

	var unloaded []string
	for _, req := range rt.seen {
		if req.URL.Path != "/api/v1/models/unload" {
			continue
		}
		var sent struct {
			InstanceID string `json:"instance_id"`
		}
		if err := json.Unmarshal(sentBody(t, req), &sent); err != nil {
			t.Fatalf("decode the sent body: %v", err)
		}
		unloaded = append(unloaded, sent.InstanceID)
	}

	want := []string{"qwen2.5-coder-1.5b-instruct", "qwen2.5-coder-1.5b-instruct:2"}
	if len(unloaded) != len(want) {
		t.Fatalf("Unload sent %v, want %v", unloaded, want)
	}
	for i, id := range want {
		if unloaded[i] != id {
			t.Errorf("Unload sent %q, want %q", unloaded[i], id)
		}
	}
}

// A Model that is not resident is already what Unload is asking for.
func TestLMStudioUnloadOfAnUnloadedModelIsNotAnError(t *testing.T) {
	rt := lmModels("models-none-loaded.json")
	lm := lmStudioFrom(t, rt)

	if err := lm.Unload(context.Background(), "qwen2.5-coder-1.5b-instruct"); err != nil {
		t.Fatalf("Unload of an unloaded Model: %v", err)
	}
	if len(rt.seen) != 1 {
		t.Errorf("Unload made %d calls, want only the listing", len(rt.seen))
	}
}

func TestLMStudioUnloadNamesAModelItDoesNotHave(t *testing.T) {
	lm := lmStudioFrom(t, lmModels("models-none-loaded.json"))

	if err := lm.Unload(context.Background(), "no-such-model-v9"); !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("Unload of a missing Model returned %v, want ErrModelNotFound", err)
	}
}

func TestLMStudioThatDoesNotAnswerIsAnError(t *testing.T) {
	lm := lmStudioFrom(t, &recorded{body: map[string]string{}})

	if _, err := lm.Catalogue(context.Background()); err == nil {
		t.Error("Catalogue against a Vendor that does not answer returned no error")
	}
	if _, err := lm.Resident(context.Background()); err == nil {
		t.Error("Resident against a Vendor that does not answer returned no error")
	}
}

func TestLMStudioEndpointIsTheLMStudioRoot(t *testing.T) {
	lm := NewLMStudio("http://127.0.0.1:1234/", nil)

	want := Endpoint{Kind: LMStudio, Base: "http://127.0.0.1:1234"}
	if lm.Endpoint() != want {
		t.Errorf("Endpoint = %+v, want %+v", lm.Endpoint(), want)
	}
}

var _ Adapter = (*LMStudioAdapter)(nil)
