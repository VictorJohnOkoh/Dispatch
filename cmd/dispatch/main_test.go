package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/VictorJohnOkoh/Dispatch/internal/config"
	"github.com/VictorJohnOkoh/Dispatch/internal/vendors"
	"golang.org/x/crypto/ssh"
)

func writeConfig(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// daemonConfig is the example file with the Workspace Root pointed at a directory
// that is there, because main resolves it, and with a port the kernel picks,
// because a test may not claim the real one.
func daemonConfig(t *testing.T, root string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "daemon.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := strings.Replace(string(body), `"/home/victor/work"`, strconv.Quote(root), 1)
	s = strings.Replace(s, `"/home/victor/.local/state/dispatch/events.db"`,
		strconv.Quote(filepath.ToSlash(filepath.Join(root, "events.db"))), 1)
	return strings.Replace(s, `"127.0.0.1:7717"`, `"127.0.0.1:0"`, 1)
}

// hubConfig is the example file with a key and a known_hosts file that are
// really there, because the Hub reads both at start, and with a port the kernel
// picks.
func hubConfig(t *testing.T, dir string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "hub.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(private, "")
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "id_ed25519")
	known := filepath.Join(dir, "known_hosts")
	writeConfig(t, dir, "id_ed25519", string(pem.EncodeToMemory(block)))
	writeConfig(t, dir, "known_hosts", "")
	s := strings.ReplaceAll(string(body), `"/home/victor/.ssh/id_ed25519"`, strconv.Quote(filepath.ToSlash(key)))
	s = strings.ReplaceAll(s, `"/home/victor/.ssh/known_hosts"`, strconv.Quote(filepath.ToSlash(known)))
	return strings.Replace(s, `"127.0.0.1:7700"`, `"127.0.0.1:0"`, 1)
}

// done is a context that is already cancelled, so a role that serves binds, sees
// the interrupt it was given and shuts down.
func done(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	return ctx
}

func TestDaemonStartsFromTheExampleFile(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "daemon.json", daemonConfig(t, dir))
	var log strings.Builder
	if code := run(done(t), []string{"daemon", "-config", path}, &log); code != 0 {
		t.Fatalf("exit %d: %s", code, log.String())
	}
	for _, want := range []string{"dispatch starting", "vendors=1", "daemon listening"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log = %q, want %q in it", log.String(), want)
		}
	}
}

// Every Vendor kind the config accepts has an Adapter, which is the claim three
// Vendors exist to make. The default branch stays because a fourth kind added to
// the config and not here would otherwise be a Vendor the Daemon never speaks to.
func TestEveryVendorKindHasAnAdapter(t *testing.T) {
	for _, kind := range []vendors.Kind{vendors.Ollama, vendors.LMStudio, vendors.LlamaSwap} {
		adapter, err := newVendor(vendors.Endpoint{Kind: kind, Base: "http://127.0.0.1:1"})
		if err != nil {
			t.Errorf("Vendor kind %s: %v", kind, err)
			continue
		}
		if adapter.Endpoint().Kind != kind {
			t.Errorf("Vendor kind %s got an Adapter for %s", kind, adapter.Endpoint().Kind)
		}
	}
}

// A Harness whose Adapter has not landed yet is a warning and not a stop, so the
// Daemon still serves the ones it has. A file naming none of them is the stop,
// because that Daemon can start no Session.
func TestDaemonWarnsAboutAHarnessWithNoAdapter(t *testing.T) {
	dir := t.TempDir()
	// Hermes is the Harness ADR 0003 turned into a test fixture, so it is the one
	// this build knows the name of and has no Adapter for.
	withHermes := strings.Replace(daemonConfig(t, dir), `{"name": "passthrough"},`,
		`{"name": "passthrough"}, {"name": "hermes", "exe": "/usr/local/bin/hermes"},`, 1)
	path := writeConfig(t, dir, "daemon.json", withHermes)
	var log strings.Builder
	if code := run(done(t), []string{"daemon", "-config", path}, &log); code != 0 {
		t.Fatalf("exit %d: %s", code, log.String())
	}
	if !strings.Contains(log.String(), "harness=hermes") {
		t.Errorf("log = %q", log.String())
	}

	// The same file with every Harness this build has an Adapter for taken out of
	// it, so hermes is the only one left and the Daemon can start no Session.
	only := withHermes
	for _, served := range []string{
		`{"name": "passthrough"}, `,
		`{"name": "opencode", "exe": "/usr/local/bin/opencode"},`,
		`{"name": "pi", "exe": "/usr/local/bin/pi"}`,
	} {
		only = strings.Replace(only, served, "", 1)
	}
	only = strings.Replace(only, `"/usr/local/bin/hermes"},`, `"/usr/local/bin/hermes"}`, 1)
	path = writeConfig(t, dir, "none.json", only)
	var errOut strings.Builder
	if code := run(done(t), []string{"daemon", "-config", path}, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "no Harness in this file has an Adapter yet") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestEveryHarnessWithAnAdapterIsServed(t *testing.T) {
	var lines strings.Builder
	got, err := newHarnesses([]config.HarnessProfile{
		{Name: "passthrough"},
		{Name: "opencode", Exe: "/usr/local/bin/opencode"},
		{Name: "pi", Exe: "/usr/local/bin/pi"},
	}, t.TempDir(), slog.New(slog.NewTextHandler(&lines, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Name != "passthrough" || got[1].Name != "opencode" || got[2].Name != "pi" {
		t.Errorf("served Harnesses = %v", got)
	}
	if lines.Len() != 0 {
		t.Errorf("warning = %q", lines.String())
	}
}

// The Workspace Root is resolved at start, so a Root that is not there stops the
// Daemon before the first Session rather than during it.
func TestDaemonRefusesAWorkspaceRootThatIsNotThere(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "daemon.json", daemonConfig(t, filepath.Join(dir, "absent")))
	var errOut strings.Builder
	if code := run(done(t), []string{"daemon", "-config", path}, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "Workspace Root") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestHubStartsFromTheExampleFile(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "hub.json", hubConfig(t, dir))
	var log strings.Builder
	if code := run(done(t), []string{"hub", "-config", path}, &log); code != 0 {
		t.Fatalf("exit %d: %s", code, log.String())
	}
	for _, want := range []string{"role=hub", "hosts=2"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log = %q, want %q in it", log.String(), want)
		}
	}
}

// The Hub reads every key at start, so a Host whose key is not there stops it
// rather than becoming a Host that is Down for a reason nobody can see.
func TestHubRefusesAKeyThatIsNotThere(t *testing.T) {
	dir := t.TempDir()
	body := strings.Replace(hubConfig(t, dir), "id_ed25519", "absent", 1)
	path := writeConfig(t, dir, "hub.json", body)
	var errOut strings.Builder
	if code := run(done(t), []string{"hub", "-config", path}, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "absent") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

// A Host id is part of a path and of a Cursor, so its shape is checked here,
// where protocol can be imported.
func TestHubRefusesAHostIDThatIsNotOne(t *testing.T) {
	dir := t.TempDir()
	body, err := os.ReadFile(filepath.Join("..", "..", "hub.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	path := writeConfig(t, dir, "hub.json", strings.Replace(string(body), `"workstation"`, `"work station"`, 1))
	var errOut strings.Builder
	if code := run(t.Context(), []string{"hub", "-config", path}, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "not a Host id") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestRunRefusesWhatIsNotARole(t *testing.T) {
	for _, args := range [][]string{{}, {"hubb"}} {
		if code := run(t.Context(), args, io.Discard); code != 2 {
			t.Errorf("run(%q) = %d, want 2", args, code)
		}
	}
}
