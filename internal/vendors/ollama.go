package vendors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ErrModelNotFound is a Model this Vendor does not have, which is a different fact
// from the Vendor not answering.
var ErrModelNotFound = errors.New("model not found")

// OllamaAdapter speaks to one Ollama over its native API. Discovery is /api/tags rather
// than the OpenAI-compatible /v1/models, because /v1/models carries only an id and
// this one call carries the capabilities, the context length, the quantisation and
// the size on disk. ADR 0007's matrix calls that one call the whole of discovery.
type OllamaAdapter struct {
	endpoint Endpoint
	client   *http.Client
}

// NewOllama makes an Adapter for the Ollama at base, such as http://127.0.0.1:11434.
// A nil RoundTripper uses http.DefaultTransport; tier-two tests pass one that
// replays recorded bodies instead of dialling.
func NewOllama(base string, rt http.RoundTripper) *OllamaAdapter {
	return &OllamaAdapter{
		endpoint: Endpoint{Kind: Ollama, Base: strings.TrimSuffix(base, "/")},
		client:   &http.Client{Transport: rt},
	}
}

func (o *OllamaAdapter) Endpoint() Endpoint { return o.endpoint }

// chatPath is where a load happens and the only path whose 404 names a Model.
const chatPath = "/api/chat"

// tagsBody is the part of /api/tags this adapter reads.
type tagsBody struct {
	Models []struct {
		Name    string `json:"name"`
		Model   string `json:"model"`
		Size    int64  `json:"size"`
		Details struct {
			QuantizationLevel string `json:"quantization_level"`
			ContextLength     int    `json:"context_length"`
		} `json:"details"`
		Capabilities []string `json:"capabilities"`
	} `json:"models"`
}

func (o *OllamaAdapter) Catalogue(ctx context.Context) ([]Model, error) {
	var body tagsBody
	if err := o.call(ctx, http.MethodGet, "/api/tags", nil, &body); err != nil {
		return nil, err
	}

	models := make([]Model, len(body.Models))
	for i, m := range body.Models {
		models[i] = Model{
			ID:             m.Model,
			Name:           m.Name,
			Caps:           ollamaCaps(m.Capabilities),
			TrainedContext: m.Details.ContextLength,
			Quant:          m.Details.QuantizationLevel,
			DiskBytes:      m.Size,
		}
	}
	return models, nil
}

// ollamaCaps maps Ollama's capability strings onto the four questions the picker
// asks. A capability Ollama listed is Yes. Anything it did not list stays Unknown
// and never becomes No: an Ollama older than v0.30.2 sends no capabilities at all,
// and reading that absence as No would hide every Model on the Host.
func ollamaCaps(listed []string) Capabilities {
	var caps Capabilities
	for _, c := range listed {
		switch c {
		case "completion":
			caps.Chat = Yes
		case "tools":
			caps.Tools = Yes
		case "thinking":
			caps.Reasoning = Yes
		case "vision":
			caps.Vision = Yes
		}
	}
	return caps
}

// psBody is the part of /api/ps this adapter reads. context_length is top level
// here and nested under details on /api/tags, and it means a different thing:
// what this instance was started with, not what the Model was trained for.
type psBody struct {
	Models []struct {
		Model         string `json:"model"`
		SizeVRAM      int64  `json:"size_vram"`
		ContextLength int    `json:"context_length"`
	} `json:"models"`
}

func (o *OllamaAdapter) Resident(ctx context.Context) ([]Resident, error) {
	var body psBody
	if err := o.call(ctx, http.MethodGet, "/api/ps", nil, &body); err != nil {
		return nil, err
	}

	resident := make([]Resident, len(body.Models))
	for i, m := range body.Models {
		resident[i] = Resident{
			ModelID:       m.Model,
			LoadedContext: m.ContextLength,
			VRAM:          m.SizeVRAM,
		}
	}
	return resident, nil
}

// Load sends an empty chat, which Ollama answers only once the weights are in
// memory. keep_alive -1 turns off Ollama's own evictor, because the Daemon does
// all the loading and unloading itself.
func (o *OllamaAdapter) Load(ctx context.Context, modelID string) error {
	return o.keepAlive(ctx, modelID, -1)
}

func (o *OllamaAdapter) Unload(ctx context.Context, modelID string) error {
	return o.keepAlive(ctx, modelID, 0)
}

func (o *OllamaAdapter) keepAlive(ctx context.Context, modelID string, seconds int) error {
	request := struct {
		Model     string     `json:"model"`
		Messages  []struct{} `json:"messages"`
		Stream    bool       `json:"stream"`
		KeepAlive int        `json:"keep_alive"`
	}{Model: modelID, Messages: []struct{}{}, KeepAlive: seconds}

	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("ollama: %w", err)
	}
	return o.call(ctx, http.MethodPost, chatPath, payload, nil)
}

// call does one request and decodes the body into out, which may be nil when only
// the status matters.
func (o *OllamaAdapter) call(ctx context.Context, method, path string, payload []byte, out any) error {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, o.endpoint.Base+path, reader)
	if err != nil {
		return fmt.Errorf("ollama: %s: %w", path, err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama: %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("ollama: %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return ollamaError(path, resp.StatusCode, body)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("ollama: %s: %w", path, err)
	}
	return nil
}

// ollamaError carries the Vendor's own words. A 404 from /api/chat is the one
// status worth a named error, because it is the answer to a Model id the user
// typed rather than to a Vendor that is wrong.
func ollamaError(path string, status int, body []byte) error {
	var refusal struct {
		Error string `json:"error"`
	}
	json.Unmarshal(body, &refusal)

	if status == http.StatusNotFound && path == chatPath {
		return fmt.Errorf("ollama: %s: %w: %s", path, ErrModelNotFound, refusal.Error)
	}
	if refusal.Error != "" {
		return fmt.Errorf("ollama: %s: HTTP %d: %s", path, status, refusal.Error)
	}
	return fmt.Errorf("ollama: %s: HTTP %d", path, status)
}
