package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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
	return strings.Replace(s, `"127.0.0.1:7717"`, `"127.0.0.1:0"`, 1)
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
	if code := run(done(t), []string{"daemon", "-config", path}, io.Discard, &log); code != 0 {
		t.Fatalf("exit %d: %s", code, log.String())
	}
	for _, want := range []string{"dispatch starting", "vendors=1", "daemon listening"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log = %q, want %q in it", log.String(), want)
		}
	}
}

// A configured Vendor with no Adapter stops the Daemon at start, rather than
// leaving a Vendor the user named and the Daemon never speaks to.
func TestDaemonRefusesAVendorWithNoAdapter(t *testing.T) {
	dir := t.TempDir()
	body := strings.Replace(daemonConfig(t, dir), `"ollama"`, `"lmstudio"`, 1)
	path := writeConfig(t, dir, "daemon.json", body)
	var errOut strings.Builder
	if code := run(done(t), []string{"daemon", "-config", path}, io.Discard, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "no Adapter yet") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

// The Workspace Root is resolved at start, so a Root that is not there stops the
// Daemon before the first Session rather than during it.
func TestDaemonRefusesAWorkspaceRootThatIsNotThere(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "daemon.json", daemonConfig(t, filepath.Join(dir, "absent")))
	var errOut strings.Builder
	if code := run(done(t), []string{"daemon", "-config", path}, io.Discard, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "Workspace Root") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestHubStartsFromTheExampleFile(t *testing.T) {
	var out strings.Builder
	if code := run(t.Context(), []string{"hub", "-config", filepath.Join("..", "..", "hub.example.json")}, &out, io.Discard); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "2 Hosts") {
		t.Errorf("out = %q", out.String())
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
	if code := run(t.Context(), []string{"hub", "-config", path}, io.Discard, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "not a Host id") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestRunRefusesWhatIsNotARole(t *testing.T) {
	for _, args := range [][]string{{}, {"hubb"}} {
		if code := run(t.Context(), args, io.Discard, io.Discard); code != 2 {
			t.Errorf("run(%q) = %d, want 2", args, code)
		}
	}
}
