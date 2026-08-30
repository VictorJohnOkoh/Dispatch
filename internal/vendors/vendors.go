// Package vendors holds the Vendor Adapter interface and the types it answers
// with. A Vendor is a program on a Host that serves model inference over a local
// API. It is a leaf package and imports nothing else in this project.
//
// The directory is vendors and not vendor, because vendor is a reserved directory
// name to the go command and ./... silently skips it.
//
// ADR 0007 owns this.
package vendors

import "context"

// Adapter is everything one Vendor contributes. One value per configured Vendor,
// made at Daemon start and shared by every Session. The Vendor is the program on
// the Host; this is the code that speaks to it.
//
// There is no Health method. A Vendor is reachable exactly when one of these calls
// returns without error, and the Daemon derives its view from that rather than
// asking a second time in a second way.
type Adapter interface {
	// Endpoint is fixed for the life of the Daemon. It is what a Harness config is
	// pointed at and what the passthrough Adapter dials.
	Endpoint() Endpoint

	// Catalogue lists every Model this Vendor can serve, loaded or not. It changes
	// when the user pulls or deletes a Model, so the Daemon caches it and the
	// Client may show it Stale.
	Catalogue(ctx context.Context) ([]Model, error)

	// Resident reports what is in memory now. It changes constantly, so it is never
	// cached and never shown Stale. An empty slice and an error are different
	// answers: nothing loaded, against nobody answering.
	Resident(ctx context.Context) ([]Resident, error)

	// Load makes a Model resident with this Vendor's own evictor disabled, and
	// returns once it is resident. It reports no progress, and it blocks for as
	// long as the load takes.
	Load(ctx context.Context, modelID string) error

	// Unload makes a Model not resident, whatever that takes on this Vendor.
	Unload(ctx context.Context, modelID string) error
}

// Endpoint is where a Vendor answers, and it never leaves this Host.
type Endpoint struct {
	Kind Kind

	// Base is the Vendor's root, such as http://127.0.0.1:11434. All three Vendors
	// serve their OpenAI-compatible surface at Base + "/v1", so a caller that wants
	// that surface appends it. Native paths belong to the Adapter and to nobody
	// else.
	Base string

	// Token is the bearer token when the user turned authentication on, and empty
	// otherwise. Every Vendor here defaults to none.
	Token string
}

// Kind is read in one place: the Daemon reads the Host config and calls the
// matching Adapter's constructor. Nothing downstream branches on it.
type Kind uint8

const (
	Ollama Kind = iota
	LMStudio
	LlamaSwap
)

// Model is one set of weights a Vendor can serve.
type Model struct {
	// ID is the Vendor's own spelling and is used verbatim, never parsed and never
	// normalised. It is unique inside one Vendor on one Host and nowhere else.
	ID string

	// Name is for a human. It is the ID when the Vendor offers nothing better.
	Name string

	Caps Capabilities

	// TrainedContext is the Model's maximum. Showing it while a Resident instance
	// is running a smaller LoadedContext is the lie the picker must not tell.
	TrainedContext int

	// Quant is a label such as "Q4_K_M". llama-swap never reports one.
	Quant string

	// DiskBytes is the size of the weights on disk.
	DiskBytes int64
}

// Support is a Vendor's answer about one Capability. Unknown is an answer and not a
// missing value, so nothing here is a pointer and no caller writes a nil check.
// Unknown is the zero value, which makes an unfilled Capabilities honest rather
// than wrong.
type Support uint8

const (
	Unknown Support = iota
	No
	Yes
)

// Capabilities is the four questions the Client's Model picker asks. Nothing here
// is version dependent: an endpoint that does not carry the answer produces
// Unknown.
type Capabilities struct {
	// Chat is whether this Model can back a Session at all. An embedding Model
	// cannot.
	Chat Support

	// Tools is whether the Model was trained for tool calling. It is not whether
	// the endpoint will accept a tools array.
	Tools Support

	// Reasoning is whether the Model produces reasoning content. A Session on a
	// Model that does writes Reasoning Events.
	Reasoning Support

	Vision Support
}

// Resident is one Model in memory now.
type Resident struct {
	// ModelID matches a Model.ID from Catalogue. It repeats when LM Studio holds
	// two instances of one Model, which the Daemon never causes and must still
	// read.
	ModelID string

	// LoadedContext is what the runner was actually started with, which is not
	// TrainedContext.
	LoadedContext int

	// VRAM is resident bytes on the GPU. Ollama alone reports it, and comparing it
	// against Model.DiskBytes is how partial CPU offload is seen.
	VRAM int64
}
