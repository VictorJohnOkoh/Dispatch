package vendors

import "testing"

// Unknown is an answer and not a missing value, and it is the zero value, so an
// unfilled Capabilities is honest rather than wrong. A zero value of No would be
// a claim the Vendor never made.
func TestSupportHasThreeValuesAndUnknownIsZero(t *testing.T) {
	var zero Support
	if zero != Unknown {
		t.Errorf("the zero Support is %d, want Unknown", zero)
	}
	if Unknown == No || No == Yes || Unknown == Yes {
		t.Error("Unknown, No and Yes are three distinct answers")
	}

	var caps Capabilities
	for _, c := range []struct {
		name string
		got  Support
	}{{"Chat", caps.Chat}, {"Tools", caps.Tools}, {"Reasoning", caps.Reasoning}, {"Vision", caps.Vision}} {
		if c.got != Unknown {
			t.Errorf("an unfilled %s is %d, want Unknown", c.name, c.got)
		}
	}
}

// Behaviour 11. One Model list, three Vendors, and every Support value in it. The
// three Vendors are in v1 to fill the three values, so drop one and a value in
// this design stops being exercised. The Adapters are behind one interface here,
// which is the other claim three Vendors exist to test.
func TestOneModelListShowsAllThreeCapabilityValues(t *testing.T) {
	adapters := []Adapter{
		ollamaFrom(t, &recorded{body: map[string]string{"/api/tags": "tags.json"}}),
		lmStudioFrom(t, lmModels("models.json")),
		llamaSwapFrom(t, &recorded{body: map[string]string{
			"/v1/models": "models.json",
			"/running":   "running-empty.json",
		}}),
	}

	// Three Kinds and three Support values, both closed sets, so this is a grid
	// and not a map.
	var seen [3][3]bool
	for _, adapter := range adapters {
		kind := adapter.Endpoint().Kind
		models, err := adapter.Catalogue(t.Context())
		if err != nil {
			t.Fatalf("%s Catalogue: %v", kind, err)
		}
		for _, m := range models {
			for _, got := range []Support{m.Caps.Chat, m.Caps.Tools, m.Caps.Reasoning, m.Caps.Vision} {
				seen[kind][got] = true
			}
		}
	}

	for _, want := range []struct {
		kind    Kind
		support Support
	}{
		// LM Studio is the only one that ever answers Yes, and the only one that
		// answers No.
		{LMStudio, Yes},
		{LMStudio, No},
		// Ollama answers Yes for what /api/tags lists, and Unknown for the rest.
		{Ollama, Yes},
		{Ollama, Unknown},
		// llama-swap knows nothing about a Model it has not loaded.
		{LlamaSwap, Unknown},
	} {
		if !seen[want.kind][want.support] {
			t.Errorf("no Model from %s answered %d", want.kind, want.support)
		}
	}

	// No Vendor ever answers No where it was merely not told.
	if seen[Ollama][No] || seen[LlamaSwap][No] {
		t.Error("a Vendor that reports no capability metadata answered No, which hides a usable Model")
	}
}
