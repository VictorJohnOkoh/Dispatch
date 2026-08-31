package workspace

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// tempDir is t.TempDir with symlinks already followed, so a test may compare a
// path it built by hand against one Contain resolved. On Windows this also spells
// out a short 8.3 component, which the temp directory often has.
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// newRoot makes a Root and fails the test rather than the caller, because every
// test here needs one and none of them is about NewRoot.
func newRoot(t *testing.T, dir string) Root {
	t.Helper()
	r, err := NewRoot(dir)
	if err != nil {
		t.Fatalf("NewRoot(%q): %v", dir, err)
	}
	return r
}

// symlink points link at the directory target. On Windows a symlink needs a
// privilege the test runner usually does not hold, so it falls back to a directory
// junction: a different reparse point with the property these tests are about,
// which is that EvalSymlinks follows it. Skipping instead would leave the case
// Contain exists for untested on the machine it runs on.
func symlink(t *testing.T, target, link string) {
	t.Helper()
	err := os.Symlink(target, link)
	if err == nil {
		return
	}
	if runtime.GOOS != "windows" {
		t.Skipf("symlinks unavailable: %v", err)
	}
	out, junction := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if junction != nil {
		t.Skipf("no symlink (%v) and no junction (%v): %s", err, junction, out)
	}
}

// The Root is inside itself. A check that refused it would refuse a Session that
// starts where the user pointed it.
func TestContainAcceptsTheRootItself(t *testing.T) {
	dir := tempDir(t)
	r := newRoot(t, dir)

	for _, name := range []string{"", ".", r.String()} {
		got, err := r.Contain(r.String(), name)
		if err != nil {
			t.Fatalf("Contain(root, %q): %v", name, err)
		}
		if got != r.String() {
			t.Errorf("Contain(root, %q) = %q, want %q", name, got, r.String())
		}
	}
}

// The ordinary case, and the one that makes the check usable: a write names a file
// that is not there yet, so refusing every path that does not exist would leave the
// one tool kind this bounds unable to create anything.
func TestContainAcceptsAPathUnderTheRoot(t *testing.T) {
	dir := tempDir(t)
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := newRoot(t, dir)

	for _, name := range []string{"src", "src/new.go", "deep/tree/that/is/not/there.txt"} {
		got, err := r.Contain(r.String(), name)
		if err != nil {
			t.Fatalf("Contain(root, %q): %v", name, err)
		}
		if want := filepath.Join(r.String(), filepath.FromSlash(name)); got != want {
			t.Errorf("Contain(root, %q) = %q, want %q", name, got, want)
		}
	}
}

// A .. that escapes is refused and a .. that does not is kept, because refusing
// every .. would refuse a path the user may legitimately name.
func TestContainJudgesDotDotByWhereItLands(t *testing.T) {
	dir := tempDir(t)
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := newRoot(t, dir)

	if _, err := r.Contain(r.String(), filepath.Join("src", "..", "docs")); err != nil {
		t.Errorf("a .. that stays inside was refused: %v", err)
	}
	escapes := []string{
		"..",
		filepath.Join("..", "elsewhere"),
		filepath.Join("src", "..", "..", "elsewhere"),
	}
	for _, name := range escapes {
		if _, err := r.Contain(r.String(), name); !errors.Is(err, ErrOutsideRoot) {
			t.Errorf("Contain(root, %q) error = %v, want ErrOutsideRoot", name, err)
		}
	}
}

// Resolve before compare. The tree is mutable and the Harness is the thing mutating
// it, so a link that points outside is the case the whole function exists for.
func TestContainRefusesASymlinkPointingOutside(t *testing.T) {
	dir := tempDir(t)
	outside := tempDir(t)
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlink(t, outside, filepath.Join(dir, "escape"))
	r := newRoot(t, dir)

	for _, name := range []string{"escape", "escape/secret.txt", "escape/new.txt"} {
		if _, err := r.Contain(r.String(), name); !errors.Is(err, ErrOutsideRoot) {
			t.Errorf("Contain(root, %q) error = %v, want ErrOutsideRoot", name, err)
		}
	}
}

// The other half of the same rule: a link that stays inside is accepted, and the
// path that comes back is the resolved one rather than the one that was asked for.
func TestContainAcceptsASymlinkPointingInside(t *testing.T) {
	dir := tempDir(t)
	if err := os.MkdirAll(filepath.Join(dir, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := newRoot(t, dir)
	symlink(t, filepath.Join(r.String(), "real"), filepath.Join(r.String(), "link"))

	got, err := r.Contain(r.String(), "link/file.txt")
	if err != nil {
		t.Fatalf("Contain: %v", err)
	}
	if want := filepath.Join(r.String(), "real", "file.txt"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Path elements, not string prefixes. work-other is not inside work, and a prefix
// test says it is.
func TestContainRefusesASiblingSharingTheRootsPrefix(t *testing.T) {
	parent := tempDir(t)
	inside := filepath.Join(parent, "work")
	sibling := filepath.Join(parent, "work-other")
	for _, d := range []string{inside, sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r := newRoot(t, inside)

	if _, err := r.Contain(r.String(), sibling); !errors.Is(err, ErrOutsideRoot) {
		t.Errorf("Contain(root, %q) error = %v, want ErrOutsideRoot", sibling, err)
	}
}

// The base is named by the caller because the two call sites have different ones,
// and neither is the directory the Daemon happens to have been started in.
func TestContainResolvesAgainstTheBase(t *testing.T) {
	dir := tempDir(t)
	work := filepath.Join(dir, "project", "sub")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	r := newRoot(t, dir)

	got, err := r.Contain(work, "notes.md")
	if err != nil {
		t.Fatalf("Contain: %v", err)
	}
	if want := filepath.Join(work, "notes.md"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A relative base would silently mean the Daemon's own working directory, which is
// the one base the ADR rules out. It is a caller's bug, so it is an error and not a
// containment refusal.
func TestContainRefusesARelativeBase(t *testing.T) {
	r := newRoot(t, tempDir(t))

	_, err := r.Contain("project", "notes.md")
	if err == nil {
		t.Fatal("a relative base was accepted")
	}
	if errors.Is(err, ErrOutsideRoot) {
		t.Error("a relative base was reported as a containment refusal")
	}
}

// A base outside the Root cannot launder a path into it, so the base is checked
// against the Root too rather than trusted for having been checked once.
func TestContainRefusesABaseOutsideTheRoot(t *testing.T) {
	r := newRoot(t, tempDir(t))
	outside := tempDir(t)

	if _, err := r.Contain(outside, "notes.md"); !errors.Is(err, ErrOutsideRoot) {
		t.Errorf("error = %v, want ErrOutsideRoot", err)
	}
}

// The zero Root is a construction bug, and a containment check that quietly passes
// is worse than one that fails loudly.
func TestZeroRootContainsNothing(t *testing.T) {
	var r Root
	if _, err := r.Contain(tempDir(t), "notes.md"); err == nil {
		t.Fatal("the zero Root accepted a path")
	}
}

// A case-sensitive check on a case-insensitive filesystem refuses a directory the
// OS considers inside the Root.
func TestContainFoldsCaseOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case folding is Windows only")
	}
	r := newRoot(t, tempDir(t))

	if _, err := r.Contain(strings.ToUpper(r.String()), "notes.md"); err != nil {
		t.Errorf("an upper-cased base was refused: %v", err)
	}
}

// The Root is resolved once at Daemon start, so a Root that is not there or is not
// a directory is a config error the Daemon reports then, rather than a Session
// failure the user has to guess at later.
func TestNewRootRefusesWhatCannotBeARoot(t *testing.T) {
	dir := tempDir(t)
	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{filepath.Join(dir, "not-there"), file} {
		if _, err := NewRoot(path); err == nil {
			t.Errorf("NewRoot(%q) was accepted", path)
		}
	}
}

// The Root is resolved too, so a Root named through a symlink and a path named
// directly still compare as the same tree.
func TestNewRootResolvesSymlinks(t *testing.T) {
	parent := tempDir(t)
	real := filepath.Join(parent, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "link")
	symlink(t, real, link)

	r := newRoot(t, link)
	if r.String() != real {
		t.Errorf("Root is %q, want the resolved %q", r.String(), real)
	}
}

// Two links pointing at each other. Neither resolves, so neither can be shown to be
// inside the Root, and the answer is an error rather than a hang. A junction pair
// makes this on Windows with no privilege at all.
func TestContainRefusesALinkCycle(t *testing.T) {
	dir := tempDir(t)
	r := newRoot(t, dir)
	a := filepath.Join(r.String(), "a")
	b := filepath.Join(r.String(), "b")
	symlink(t, b, a)
	symlink(t, a, b)

	if _, err := r.Contain(r.String(), "a"); err == nil {
		t.Fatal("a link cycle was accepted")
	}
}
