package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VictorJohnOkoh/Dispatch/internal/config"
	"github.com/VictorJohnOkoh/Dispatch/internal/hub"
)

// desk is one registered Host as the module hands it back.
func desk(dir string) hub.Registered {
	return hub.Registered{
		ID: "desk", Address: "192.168.1.22:22", User: "victor",
		KeyPath:    filepath.Join(dir, "id_ed25519"),
		KnownHosts: filepath.Join(dir, "known_hosts"),
		DaemonPort: defaultDaemonPort,
	}
}

// A Hub that has never had a configuration gets one, and it listens where ADR
// 0013 says.
func TestTheFirstHostMakesHubJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hub.json")
	if err := commitHost(path, desk(dir)); err != nil {
		t.Fatalf("commitHost = %v", err)
	}
	cfg, err := config.LoadHub(path)
	if err != nil {
		t.Fatalf("LoadHub = %v", err)
	}
	if cfg.Listen != defaultHubListen || len(cfg.Hosts) != 1 || cfg.Hosts[0].ID != "desk" {
		t.Errorf("hub.json = %+v", cfg)
	}
	if cfg.Hosts[0].KeyPath != desk(dir).KeyPath || cfg.Hosts[0].DaemonPort != defaultDaemonPort {
		t.Errorf("Host = %+v", cfg.Hosts[0])
	}
	// The file is replaced whole, so the temporary one it was written as is gone.
	if _, err := os.Stat(path + ".new"); err == nil {
		t.Error("the temporary file stayed")
	}
}

// A second Host is added to the file that is there, and everything the user
// already had in it is kept.
func TestASecondHostKeepsTheFirstAndTheSettings(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "hub.json", hubConfig(t, dir))
	before, err := config.LoadHub(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := commitHost(path, desk(dir)); err != nil {
		t.Fatalf("commitHost = %v", err)
	}
	after, err := config.LoadHub(path)
	if err != nil {
		t.Fatalf("LoadHub = %v", err)
	}
	if after.Listen != before.Listen || len(after.Hosts) != len(before.Hosts)+1 {
		t.Errorf("hub.json = %+v, want the file it was with one more Host", after)
	}
	if after.Hosts[0] != before.Hosts[0] {
		t.Errorf("the first Host changed to %+v", after.Hosts[0])
	}
}

// Registration adds a Host and never replaces one, so an id that is already
// there is refused rather than overwritten.
func TestAHostThatIsAlreadyNamedIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "hub.json", hubConfig(t, dir))
	taken := desk(dir)
	taken.ID = "workstation"
	err := commitHost(path, taken)
	if err == nil || !strings.Contains(err.Error(), "workstation") {
		t.Fatalf("commitHost = %v, want an error naming the Host", err)
	}
}

// The same machine on the same Daemon port under a second name is the same
// Host, and one Host in the list twice is a Hub that opens two streams to it.
func TestTheSameAddressAndDaemonPortIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "hub.json", hubConfig(t, dir))
	same := desk(dir)
	same.Address = "192.168.1.21:22" // the laptop in the example file
	err := commitHost(path, same)
	if err == nil || !strings.Contains(err.Error(), "192.168.1.21:22") {
		t.Fatalf("commitHost = %v, want an error naming the address", err)
	}
}

// The Hub dials the address as it is written, so a user who typed no port gets
// SSH's own.
func TestAnAddressWithNoPortGetsPort22(t *testing.T) {
	for address, want := range map[string]string{
		"192.168.1.20":     "192.168.1.20:22",
		"192.168.1.20:220": "192.168.1.20:220",
		"desk.local":       "desk.local:22",
	} {
		if got := withPort(address); got != want {
			t.Errorf("withPort(%q) = %q, want %q", address, got, want)
		}
	}
}

func TestHostAddNeedsAnIDAnAddressAndAnAccount(t *testing.T) {
	var out strings.Builder
	if code := runHost(t.Context(), []string{"add", "-id", "desk"}, &out); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out.String(), "account") {
		t.Errorf("stderr = %q", out.String())
	}
}

// A Host id is part of a path and of a Cursor, so its shape is checked here,
// where protocol can be imported, and before the user types a password.
func TestHostAddRefusesAHostIDThatIsNotOne(t *testing.T) {
	var out strings.Builder
	code := runHost(t.Context(), []string{"add", "-id", "work station", "-address", "10.0.0.4", "-user", "victor"}, &out)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out.String(), "not a Host id") {
		t.Errorf("stderr = %q", out.String())
	}
}

func TestHostTakesOnlyAdd(t *testing.T) {
	var out strings.Builder
	for _, args := range [][]string{{}, {"remove"}} {
		if code := runHost(t.Context(), args, &out); code != 2 {
			t.Errorf("runHost(%q) = %d, want 2", args, code)
		}
	}
}
