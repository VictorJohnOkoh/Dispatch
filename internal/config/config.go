// Package config holds the two files this program reads at start, daemon.json
// and hub.json, and the loader that turns each into values.
//
// The traffic goes one way. Nothing under internal/ imports this package:
// main.go reads the file, validates it and hands plain values down, so no
// package below cmd needs a config file to be tested. This package imports
// event and vendors for the types its fields hold, and nothing else here.
//
// An unknown key is a startup error, which is what makes ADR 0010's first fence
// mechanical: a peers key in daemon.json fails to load rather than being quietly
// ignored, because Daemon has nowhere to put it.
//
// ADR 0010 owns this, and SPEC.md chose the format.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/VictorJohnOkoh/Dispatch/internal/event"
	"github.com/VictorJohnOkoh/Dispatch/internal/vendors"
)

// Daemon is one Host's own configuration. It has no field naming another Host,
// and that absence is the design rather than an omission.
type Daemon struct {
	// Listen is the Daemon's address, and it is loopback only. The Hub reaches it
	// through the SSH tunnel and nothing else reaches it at all.
	Listen string `json:"listen"`

	// WorkspaceRoot stays a plain string. Resolving it touches the filesystem and
	// can fail, so main.go passes it to workspace.NewRoot.
	WorkspaceRoot string `json:"workspaceRoot"`

	// LogPath is the SQLite file the Event log is written to.
	LogPath string `json:"logPath"`

	Vendors   []VendorProfile  `json:"vendors"`
	Harnesses []HarnessProfile `json:"harnesses"`

	// PolicyDefault is a default per slot and never a Session's Approval Policy.
	// The Daemon computes that at Session start by clipping this against the
	// Harness's Gates, because a slot with no Gate may only be Auto.
	PolicyDefault event.Policy `json:"policyDefault"`
}

// Hub is the only type in the system that holds more than one Host.
type Hub struct {
	Listen string        `json:"listen"`
	Hosts  []HostProfile `json:"hosts"`
}

// VendorProfile is one Vendor on this Host. It decodes straight into the
// vendors.Endpoint an Adapter constructor takes.
type VendorProfile struct {
	Endpoint vendors.Endpoint
}

// HostProfile is one Host the Hub connects to over SSH.
type HostProfile struct {
	// ID is this Host's name on the wire. Its character rule belongs to protocol,
	// which this package may not import, so main.go checks the shape.
	ID string `json:"id"`

	// Address is the SSH endpoint, host:port.
	Address string `json:"address"`

	User    string `json:"user"`
	KeyPath string `json:"keyPath"`

	// KnownHosts is the file this Host's key is checked against. It is empty for
	// the user's own ~/.ssh/known_hosts, which main.go resolves.
	KnownHosts string `json:"knownHosts"`

	// DaemonPort is the loopback port the direct-tcpip channel opens onto.
	DaemonPort int `json:"daemonPort"`
}

// HarnessProfile names one Harness and where its program is.
type HarnessProfile struct {
	Name string `json:"name"`

	// Exe is the path to the program, per ADR 0006, and never a bare name for the
	// PATH to resolve. It is empty for a Harness that spawns no process, which is
	// passthrough alone. Whether the path is absolute is checked where the process
	// is spawned, because this file is written for a Host that may not be the one
	// reading it here.
	Exe string `json:"exe"`
}

// vendorKinds maps the spelling in the file to the Kind the Daemon switches on
// once, when it calls the matching Adapter's constructor.
var vendorKinds = map[string]vendors.Kind{
	"ollama":    vendors.Ollama,
	"lmstudio":  vendors.LMStudio,
	"llamaswap": vendors.LlamaSwap,
}

// LoadDaemon reads and validates one Host's daemon.json.
func LoadDaemon(path string) (Daemon, error) {
	var d Daemon
	if err := decodeFile(path, &d); err != nil {
		return Daemon{}, err
	}
	if err := d.Validate(); err != nil {
		return Daemon{}, fmt.Errorf("config: %s: %w", path, err)
	}
	return d, nil
}

// LoadHub reads and validates hub.json.
func LoadHub(path string) (Hub, error) {
	var h Hub
	if err := decodeFile(path, &h); err != nil {
		return Hub{}, err
	}
	if err := h.Validate(); err != nil {
		return Hub{}, fmt.Errorf("config: %s: %w", path, err)
	}
	return h, nil
}

func (d Daemon) Validate() error {
	if d.Listen == "" {
		return fmt.Errorf("listen is empty")
	}
	if d.WorkspaceRoot == "" {
		return fmt.Errorf("workspaceRoot is empty")
	}
	if d.LogPath == "" {
		return fmt.Errorf("logPath is empty")
	}
	if len(d.Vendors) == 0 {
		return fmt.Errorf("this Host names no Vendor")
	}
	if len(d.Harnesses) == 0 {
		return fmt.Errorf("this Host names no Harness")
	}
	for i, h := range d.Harnesses {
		if h.Name == "" {
			return fmt.Errorf("a Harness has no name")
		}
		for _, earlier := range d.Harnesses[:i] {
			if earlier.Name == h.Name {
				return fmt.Errorf("Harness %q is named twice", h.Name)
			}
		}
		if h.Exe != "" && !strings.ContainsAny(h.Exe, `/\`) {
			return fmt.Errorf("Harness %q: exe %q is a bare name, not a path", h.Name, h.Exe)
		}
	}
	// A policyDefault that is not in the file never reaches event.Policy's own
	// decoder, so five empty slots arrive here rather than an error there.
	for kind, rule := range d.PolicyDefault {
		switch rule {
		case event.RuleAuto, event.RuleWait, event.RuleRefuse:
		default:
			return fmt.Errorf("policyDefault %s slot is %q, not a Rule", event.ToolKind(kind), rule)
		}
	}
	return nil
}

func (h Hub) Validate() error {
	if h.Listen == "" {
		return fmt.Errorf("listen is empty")
	}
	if len(h.Hosts) == 0 {
		return fmt.Errorf("no Host is named")
	}
	for i, host := range h.Hosts {
		if host.ID == "" {
			return fmt.Errorf("a Host has no id")
		}
		for _, earlier := range h.Hosts[:i] {
			if earlier.ID == host.ID {
				return fmt.Errorf("Host %q is named twice", host.ID)
			}
		}
		if host.Address == "" {
			return fmt.Errorf("Host %q has no address", host.ID)
		}
		if host.User == "" {
			return fmt.Errorf("Host %q has no user", host.ID)
		}
		if host.KeyPath == "" {
			return fmt.Errorf("Host %q has no keyPath", host.ID)
		}
		if host.DaemonPort < 1 || host.DaemonPort > 65535 {
			return fmt.Errorf("Host %q: daemonPort %d is not a port", host.ID, host.DaemonPort)
		}
	}
	return nil
}

// UnmarshalJSON resolves the Vendor kind while it decodes, so an unknown Vendor
// is a startup error like any other bad key. It runs its own strict decoder
// because DisallowUnknownFields on the outer one stops at a custom unmarshaler.
func (p *VendorProfile) UnmarshalJSON(b []byte) error {
	var raw struct {
		Kind  string `json:"kind"`
		Base  string `json:"base"`
		Token string `json:"token"`
	}
	if err := decodeStrict(b, &raw); err != nil {
		return err
	}
	kind, ok := vendorKinds[raw.Kind]
	if !ok {
		return fmt.Errorf("no such Vendor kind %q", raw.Kind)
	}
	if raw.Base == "" {
		return fmt.Errorf("Vendor %q has no base", raw.Kind)
	}
	p.Endpoint = vendors.Endpoint{Kind: kind, Base: raw.Base, Token: raw.Token}
	return nil
}

// decodeFile reads one JSON value out of path with unknown keys refused.
func decodeFile(path string, v any) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if err := decodeStrict(body, v); err != nil {
		return fmt.Errorf("config: %s: %w", path, err)
	}
	return nil
}

// decodeStrict decodes exactly one JSON value out of body and refuses a key the
// target has no field for. A second value after the first is an error, because a
// file pasted into itself is a mistake and not a configuration.
func decodeStrict(body []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("a second value follows the first")
	}
	return nil
}
