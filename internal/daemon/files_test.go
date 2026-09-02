package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/VictorJohnOkoh/Dispatch/internal/workspace"
)

func contained(t *testing.T) (files, string) {
	t.Helper()
	dir := t.TempDir()
	root, err := workspace.NewRoot(dir)
	if err != nil {
		t.Fatalf("NewRoot: %v", err)
	}
	return files{root: root, dir: root.String()}, root.String()
}

// A relative path is the Session's working directory, which is what an Adapter
// writing its own config asks for.
func TestADelegatedWriteLandsInTheWorkingDirectory(t *testing.T) {
	f, dir := contained(t)

	if err := f.WriteTextFile("opencode.json", "{}"); err != nil {
		t.Fatalf("WriteTextFile: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "opencode.json"))
	if err != nil || string(raw) != "{}" {
		t.Fatalf("the file holds %q: %v", raw, err)
	}
}

// A Harness delegates absolute paths, and an absolute path buys no way past the
// Workspace Root. This is the second lever on a write the permission gate passed.
func TestAWriteOutsideTheWorkspaceRootIsRefused(t *testing.T) {
	f, dir := contained(t)
	outside := filepath.Join(dir, "..", "escaped.txt")

	err := f.WriteTextFile(outside, "banana")
	if !errors.Is(err, workspace.ErrOutsideRoot) {
		t.Fatalf("WriteTextFile = %v, want it refused", err)
	}
	if _, err := os.Stat(outside); err == nil {
		t.Error("the refused write happened anyway")
	}
}
