package vendors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// LlamaSwapAdapter speaks to one llama-swap, which is how llama.cpp becomes a
// Vendor at all: it fronts a configured set of llama-server processes and starts
// whichever Model is asked for. ADR 0002 owns that choice.
//
// Discovery is three calls rather than one. /v1/models names the Models and
// nothing else, /running says which are in memory, and only a Model that is
// already in memory is asked for its capabilities, because asking is what starts
// a Model that is not.
//
// The trap in this Vendor's token counts is its input against cacheRead split.
// Usage in stream.go has it.
type LlamaSwapAdapter struct {
	endpoint Endpoint
	client   *http.Client
}

// NewLlamaSwap makes an Adapter for the llama-swap at base, such as
// http://127.0.0.1:8080. A nil RoundTripper uses http.DefaultTransport; tier-two
// tests pass one that replays recorded bodies instead of dialling.
func NewLlamaSwap(base string, rt http.RoundTripper) *LlamaSwapAdapter {
	return &LlamaSwapAdapter{
		endpoint: Endpoint{Kind: LlamaSwap, Base: strings.TrimSuffix(base, "/")},
		client:   &http.Client{Transport: rt},
	}
}

func (s *LlamaSwapAdapter) Endpoint() Endpoint { return s.endpoint }

const (
	swapModelsPath  = "/v1/models"
	swapRunningPath = "/running"
)

// propsPath is where one Model's capabilities live, and reaching it is also how a
// cold Model is started. A bare /props is HTTP 404 with "no model id could be
// identified", because llama-swap fronts several Models and cannot guess.
func propsPath(modelID string) string { return "/upstream/" + modelID + "/props" }

func unloadPath(modelID string) string { return "/api/models/unload/" + modelID }

// swapModelsBody is the part of /v1/models this adapter reads, which is the id.
// There is no size, no quantisation and no context length in this listing.
type swapModelsBody struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// swapRunningBody is the part of /running this adapter reads.
type swapRunningBody struct {
	Running []struct {
		Model string `json:"model"`
	} `json:"running"`
}

// swapPropsBody is the part of /upstream/<id>/props this adapter reads.
// chat_template_caps is computed from the Jinja template the server will actually
// use, which makes it the most accurate capability answer of the three Vendors
// while the Model is resident.
type swapPropsBody struct {
	ModelFtype string `json:"model_ftype"`
	Modalities *struct {
		Vision *bool `json:"vision"`
	} `json:"modalities"`
	ChatTemplate    string `json:"chat_template"`
	ChatTemplateCap *struct {
		SupportsToolCalls *bool `json:"supports_tool_calls"`
	} `json:"chat_template_caps"`
}

func (s *LlamaSwapAdapter) Catalogue(ctx context.Context) ([]Model, error) {
	var listing swapModelsBody
	if err := s.call(ctx, http.MethodGet, swapModelsPath, &listing); err != nil {
		return nil, err
	}
	resident, err := s.Resident(ctx)
	if err != nil {
		return nil, err
	}

	models := make([]Model, len(listing.Data))
	for i, m := range listing.Data {
		// The id is the only name llama-swap has for a Model.
		models[i] = Model{ID: m.ID, Name: m.ID}
		if !isResident(resident, m.ID) {
			continue
		}

		var props swapPropsBody
		if err := s.call(ctx, http.MethodGet, propsPath(m.ID), &props); err != nil {
			// The Model was resident a moment ago and is not now, which is an
			// answer that went back to Unknown rather than a Vendor that failed.
			if errors.Is(err, ErrModelNotFound) {
				continue
			}
			return nil, err
		}
		models[i].Caps = swapCaps(props)
		models[i].Quant = props.ModelFtype
	}
	return models, nil
}

// isResident scans rather than indexes, because a Host runs a handful of Models
// at once and the list is never long enough for a map to pay for itself.
func isResident(resident []Resident, modelID string) bool {
	for _, r := range resident {
		if r.ModelID == modelID {
			return true
		}
	}
	return false
}

// Resident is what llama-swap is running. A Model appears here from the moment
// its llama-server process starts, which is also when it starts claiming VRAM.
//
// LoadedContext and VRAM stay zero. /running carries neither, and the context is
// only on /props, which would be one extra call per Model on a list the Daemon
// fetches every beat.
func (s *LlamaSwapAdapter) Resident(ctx context.Context) ([]Resident, error) {
	var body swapRunningBody
	if err := s.call(ctx, http.MethodGet, swapRunningPath, &body); err != nil {
		return nil, err
	}

	resident := make([]Resident, len(body.Running))
	for i, r := range body.Running {
		resident[i] = Resident{ModelID: r.Model}
	}
	return resident, nil
}

// Load asks the Model for its own props, which starts it. llama-swap has no load
// endpoint, and this is the cheap request ADR 0002 named. The evictor to turn off
// is ttl: 0 in llama-swap's own config, which is Host setup rather than a call.
func (s *LlamaSwapAdapter) Load(ctx context.Context, modelID string) error {
	return s.call(ctx, http.MethodGet, propsPath(modelID), nil)
}

func (s *LlamaSwapAdapter) Unload(ctx context.Context, modelID string) error {
	// The answer is the two bytes OK rather than JSON, so nothing decodes it.
	return s.call(ctx, http.MethodPost, unloadPath(modelID), nil)
}

// swapCaps maps one /props answer onto the four questions the picker asks.
// Reasoning stays Unknown even here: llama.cpp has no flag for it, and inferring
// one from the reasoning format would be a guess about the Model dressed as an
// answer.
func swapCaps(props swapPropsBody) Capabilities {
	caps := Capabilities{}
	if props.ChatTemplate != "" {
		caps.Chat = Yes
	}
	if props.Modalities != nil {
		caps.Vision = supportOf(props.Modalities.Vision)
	}
	if props.ChatTemplateCap != nil {
		// supports_tool_calls is whether the template can render the Model's own
		// tool calls, which is the closest llama.cpp gets to trained for tool use.
		caps.Tools = supportOf(props.ChatTemplateCap.SupportsToolCalls)
	}
	return caps
}

// call does one request and decodes the body into out, which may be nil when only
// the status matters.
func (s *LlamaSwapAdapter) call(ctx context.Context, method, path string, out any) error {
	// Every call this Vendor makes is a GET or an empty POST, so there is no
	// payload argument to carry.
	status, body, err := request(ctx, s.client, method, s.endpoint.Base+path, nil)
	if err != nil {
		return fmt.Errorf("llamaswap: %s: %w", path, err)
	}
	if status != http.StatusOK {
		return swapError(path, status, body)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("llamaswap: %s: %w", path, err)
	}
	return nil
}

// swapError carries the Vendor's own words. A 404 from a per-Model path names the
// Model the user typed, which is a different fact from a Vendor that is wrong.
func swapError(path string, status int, body []byte) error {
	var refusal struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.Unmarshal(body, &refusal)

	perModel := strings.HasPrefix(path, "/upstream/") || strings.HasPrefix(path, "/api/models/unload/")
	if status == http.StatusNotFound && perModel {
		return fmt.Errorf("llamaswap: %s: %w: %s", path, ErrModelNotFound, refusal.Error.Message)
	}
	if refusal.Error.Message != "" {
		return fmt.Errorf("llamaswap: %s: HTTP %d: %s", path, status, refusal.Error.Message)
	}
	return fmt.Errorf("llamaswap: %s: HTTP %d", path, status)
}
