package vendors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// LMStudioAdapter speaks to one LM Studio over its native v1 REST API. Discovery
// is one call to /api/v1/models, which answers Catalogue and Resident together
// because a loaded instance is nested under the Model it is an instance of.
//
// It is the only Vendor in v1 that answers Yes or No to a Capability, because it
// is the only one whose listing carries typed booleans.
//
// A note for anyone reading token counts off this Vendor: LM Studio was the only
// one of the three to count reasoning tokens for the same thinking output, 51
// against 0 elsewhere. See Usage in stream.go.
type LMStudioAdapter struct {
	endpoint Endpoint
	client   *http.Client
}

// NewLMStudio makes an Adapter for the LM Studio at base, such as
// http://127.0.0.1:1234. A nil RoundTripper uses http.DefaultTransport; tier-two
// tests pass one that replays recorded bodies instead of dialling.
func NewLMStudio(base string, rt http.RoundTripper) *LMStudioAdapter {
	return &LMStudioAdapter{
		endpoint: Endpoint{Kind: LMStudio, Base: strings.TrimSuffix(base, "/")},
		client:   &http.Client{Transport: rt},
	}
}

func (l *LMStudioAdapter) Endpoint() Endpoint { return l.endpoint }

const (
	lmModelsPath = "/api/v1/models"
	lmLoadPath   = "/api/v1/models/load"
	lmUnloadPath = "/api/v1/models/unload"
)

// lmModelsBody is the part of /api/v1/models this adapter reads. The booleans are
// pointers because an absent key is Unknown and a false one is No, and an
// LM Studio below 0.4.0 sends no capabilities object at all. Nothing outside this
// decode holds a pointer.
type lmModelsBody struct {
	Models []struct {
		Type             string `json:"type"`
		Key              string `json:"key"`
		DisplayName      string `json:"display_name"`
		MaxContextLength int    `json:"max_context_length"`
		SizeBytes        int64  `json:"size_bytes"`
		Quantization     struct {
			Name string `json:"name"`
		} `json:"quantization"`
		Capabilities    *lmCapabilities `json:"capabilities"`
		LoadedInstances []struct {
			ID     string `json:"id"`
			Config struct {
				ContextLength int `json:"context_length"`
			} `json:"config"`
		} `json:"loaded_instances"`
	} `json:"models"`
}

// lmCapabilities is the capabilities object, split out because a Model may carry
// none at all. reasoning is an object naming the options rather than a boolean, so
// only its presence is read.
type lmCapabilities struct {
	Vision            *bool           `json:"vision"`
	TrainedForToolUse *bool           `json:"trained_for_tool_use"`
	Reasoning         json.RawMessage `json:"reasoning"`
}

func (l *LMStudioAdapter) Catalogue(ctx context.Context) ([]Model, error) {
	body, err := l.models(ctx)
	if err != nil {
		return nil, err
	}

	models := make([]Model, len(body.Models))
	for i, m := range body.Models {
		models[i] = Model{
			ID:             m.Key,
			Name:           m.DisplayName,
			Caps:           lmStudioCaps(m.Type, m.Capabilities),
			TrainedContext: m.MaxContextLength,
			Quant:          m.Quantization.Name,
			DiskBytes:      m.SizeBytes,
		}
	}
	return models, nil
}

func (l *LMStudioAdapter) Resident(ctx context.Context) ([]Resident, error) {
	body, err := l.models(ctx)
	if err != nil {
		return nil, err
	}

	// VRAM stays zero: LM Studio reports no memory figure over HTTP at all.
	var resident []Resident
	for _, m := range body.Models {
		for _, instance := range m.LoadedInstances {
			resident = append(resident, Resident{
				ModelID:       m.Key,
				LoadedContext: instance.Config.ContextLength,
			})
		}
	}
	return resident, nil
}

// Load loads the Model explicitly rather than letting a first prompt do it. That
// is what exempts it from LM Studio's 60 minute idle timer and from Auto-Evict,
// which on a default LM Studio would silently unload whatever was resident.
func (l *LMStudioAdapter) Load(ctx context.Context, modelID string) error {
	payload, err := json.Marshal(struct {
		Model string `json:"model"`
	}{Model: modelID})
	if err != nil {
		return fmt.Errorf("lmstudio: %w", err)
	}
	return l.call(ctx, http.MethodPost, lmLoadPath, payload, nil)
}

// Unload unloads every instance of the Model, because the Daemon's intent is that
// the VRAM comes back and LM Studio unloads one instance at a time. A Model that
// is already not resident is what Unload asks for, so that is not an error.
func (l *LMStudioAdapter) Unload(ctx context.Context, modelID string) error {
	body, err := l.models(ctx)
	if err != nil {
		return err
	}

	known := false
	for _, m := range body.Models {
		if m.Key != modelID {
			continue
		}
		known = true
		for _, instance := range m.LoadedInstances {
			payload, err := json.Marshal(struct {
				InstanceID string `json:"instance_id"`
			}{InstanceID: instance.ID})
			if err != nil {
				return fmt.Errorf("lmstudio: %w", err)
			}
			if err := l.call(ctx, http.MethodPost, lmUnloadPath, payload, nil); err != nil {
				return err
			}
		}
	}
	if !known {
		return fmt.Errorf("lmstudio: %s: %w: %s", lmUnloadPath, ErrModelNotFound, modelID)
	}
	return nil
}

func (l *LMStudioAdapter) models(ctx context.Context) (lmModelsBody, error) {
	var body lmModelsBody
	err := l.call(ctx, http.MethodGet, lmModelsPath, nil, &body)
	return body, err
}

// lmStudioCaps maps one listing entry onto the four questions the picker asks. A
// boolean LM Studio sent is Yes or No, and a key it did not send stays Unknown:
// gpt-oss-20b reasons and carries no reasoning key, and an LM Studio below 0.4.0
// carries no capabilities object at all.
func lmStudioCaps(kind string, listed *lmCapabilities) Capabilities {
	caps := Capabilities{Chat: lmStudioChat(kind)}
	if listed == nil {
		return caps
	}

	caps.Vision = supportOf(listed.Vision)
	caps.Tools = supportOf(listed.TrainedForToolUse)
	if len(listed.Reasoning) > 0 && !bytes.Equal(listed.Reasoning, []byte("null")) {
		caps.Reasoning = Yes
	}
	return caps
}

// lmStudioChat reads the one field that says whether a Model can back a Session.
// A type this adapter does not know stays Unknown rather than becoming No.
func lmStudioChat(kind string) Support {
	switch kind {
	case "llm", "vlm":
		return Yes
	case "embedding":
		return No
	default:
		return Unknown
	}
}

// call does one request and decodes the body into out, which may be nil when only
// the status matters.
func (l *LMStudioAdapter) call(ctx context.Context, method, path string, payload []byte, out any) error {
	status, body, err := request(ctx, l.client, method, l.endpoint.Base+path, payload)
	if err != nil {
		return fmt.Errorf("lmstudio: %s: %w", path, err)
	}
	if status != http.StatusOK {
		return lmStudioError(path, status, body)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("lmstudio: %s: %w", path, err)
	}
	return nil
}

// lmStudioError carries the Vendor's own words. A 404 from the load or unload path
// is the one status worth a named error, because it answers a Model id the user
// typed rather than a Vendor that is wrong.
func lmStudioError(path string, status int, body []byte) error {
	var refusal struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.Unmarshal(body, &refusal)

	if status == http.StatusNotFound && (path == lmLoadPath || path == lmUnloadPath) {
		return fmt.Errorf("lmstudio: %s: %w: %s", path, ErrModelNotFound, refusal.Error.Message)
	}
	if refusal.Error.Message != "" {
		return fmt.Errorf("lmstudio: %s: HTTP %d: %s", path, status, refusal.Error.Message)
	}
	return fmt.Errorf("lmstudio: %s: HTTP %d", path, status)
}
