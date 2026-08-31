// Package workspace holds the Workspace Root and the one function that enforces
// it. The Root is the directory on a Host outside which no Session may operate.
//
// It bounds two things and nothing else: the working directory a Session starts
// in, and every path a Harness delegates back for a write. It is not a sandbox. A
// Harness runs as an ordinary process with the user's own permissions, so its own
// reads and its shell commands are not bounded by it, and nothing here stops it
// opening a file that user can open. The containment for what a Harness runs is
// the Approval Policy, not this.
//
// It is a leaf package and imports nothing else in this project.
//
// ADR 0008 owns this.
package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrOutsideRoot is a path that resolved outside the Root. It is the refusal the
// Daemon turns into a 422, and it is distinct from a resolve that failed for a
// reason of its own.
var ErrOutsideRoot = errors.New("outside the Workspace Root")

// Root is a Host's Workspace Root, resolved once at Daemon start.
type Root struct{ resolved string }

// NewRoot resolves a Host's Workspace Root. It must already be a directory,
// because a Session cannot run in one that is not there, and finding that out at
// Daemon start is better than finding it out at the first Session.
func NewRoot(path string) (Root, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Root{}, fmt.Errorf("workspace: Workspace Root %q: %w", path, err)
	}
	resolved, err := evalLinks(abs)
	if err != nil {
		return Root{}, fmt.Errorf("workspace: Workspace Root %q: %w", path, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return Root{}, fmt.Errorf("workspace: Workspace Root %q: %w", path, err)
	}
	if !info.IsDir() {
		return Root{}, fmt.Errorf("workspace: Workspace Root %q is not a directory", path)
	}
	return Root{resolved: resolved}, nil
}

// String is the resolved Root, which is the base a Session start passes back in.
func (r Root) String() string { return r.resolved }

// Contain resolves name against base with symlinks followed and returns the
// resolved absolute path when it is the Root or under it. Every path the Daemon
// gives a Harness and every path a Harness gives back passes through here, and
// there is no second way in.
//
// base is named by the caller rather than defaulted, because the two call sites
// have different bases and the Daemon's own working directory is neither of them.
// A Session start passes the Root, so a relative directory the user named means
// what they think it means. A delegated write passes the Session's working
// directory. base must be absolute for the same reason: a relative one would
// silently mean whatever directory the Daemon happened to be started in.
//
// An absolute name is taken as it stands rather than joined onto base, because a
// Harness delegates absolute paths and joining one would name a directory nobody
// meant. It is checked against the Root exactly like a joined one, so an absolute
// name buys no way past this.
//
// Re-resolving at every write rather than trusting the check at Session start is
// not paranoia. The tree is mutable and the Harness is the thing mutating it, so a
// symlink that pointed inside the Root a minute ago may not now. The window
// between this resolve and the write is left open: the adversary here is a
// confused Model, not an attacker racing the Daemon for microseconds.
func (r Root) Contain(base, name string) (string, error) {
	if r.resolved == "" {
		return "", errors.New("workspace: Contain on a zero Root")
	}
	if !filepath.IsAbs(base) {
		return "", fmt.Errorf("workspace: base %q is not absolute", base)
	}

	candidate := name
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(base, name)
	}
	resolved, err := resolveDeepest(candidate)
	if err != nil {
		return "", fmt.Errorf("workspace: resolve %q: %w", candidate, err)
	}
	if !r.holds(resolved) {
		return "", fmt.Errorf("workspace: %q is %w %q", resolved, ErrOutsideRoot, r.resolved)
	}
	return resolved, nil
}

// resolveDeepest follows symlinks on the deepest ancestor of path that exists and
// rejoins the part that does not.
//
// Walking up is what makes the check usable rather than merely correct.
// EvalSymlinks fails on a path that is not there, and a write that creates a file
// names one by definition, so resolving the candidate directly would refuse every
// new file. The guarantee is the same either way: a symlink can only be traversed
// through a component that exists, and a component that does not exist yet cannot
// point anywhere.
func resolveDeepest(path string) (string, error) {
	var err error
	for dir, rest := range ancestors(path) {
		var resolved string
		if resolved, err = evalLinks(dir); err == nil {
			return filepath.Join(resolved, rest), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}
	return "", err // nothing down to the volume root is there
}

// ancestors yields path and then each directory above it, each paired with the
// part of path that lies below it. Both walks here need the pair, so the walk is
// written once.
func ancestors(path string) iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		rest := ""
		for yield(path, rest) {
			parent := filepath.Dir(path)
			if parent == path { // the volume root
				return
			}
			rest = filepath.Join(filepath.Base(path), rest)
			path = parent
		}
	}
}

// maxLinkHops bounds a cycle of links pointing at each other.
const maxLinkHops = 32

// evalLinks resolves a path that exists, with every link followed.
//
// It is filepath.EvalSymlinks with one repair, and the repair is the reason this
// is not a one-line call. The stdlib does not follow a Windows directory junction:
// os.Lstat calls one irregular rather than a symlink, so EvalSymlinks hands the
// junction's own path straight back and a Root check would accept whatever it
// points at. A junction is also the one reparse point an unprivileged process on
// Windows can make, and a Harness runs unprivileged, so it is the escape most
// likely to exist rather than the exotic one. os.Readlink does read a junction,
// which is what closes it.
//
// This corrects ADR 0008, which named EvalSymlinks alone.
func evalLinks(path string) (string, error) {
	for range maxLinkHops {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", err
		}
		hop := expandDeepestLink(resolved)
		if hop == "" {
			return resolved, nil
		}
		path = hop
	}
	return "", fmt.Errorf("more than %d links in %q", maxLinkHops, path)
}

// expandDeepestLink returns path with its deepest link ancestor replaced by what
// that ancestor points at, or "" when EvalSymlinks already followed everything.
// Deepest first is enough, because the OS traverses the ancestors above it for us,
// so one substitution per hop reaches the same place as walking down would.
func expandDeepestLink(path string) string {
	for dir, rest := range ancestors(path) {
		target, err := os.Readlink(dir)
		if err != nil {
			continue // not a link, or not one this process may read
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(dir), target)
		}
		return filepath.Join(target, rest)
	}
	return ""
}

// holds reports whether path is the Root or under it. It compares path elements
// rather than string prefixes, because a prefix test lets work-other pass for
// work. Any .. is already gone: both paths are absolute and cleaned, and Clean
// resolves every .. in an absolute path.
func (r Root) holds(path string) bool {
	rel, err := filepath.Rel(foldCase(r.resolved), foldCase(path))
	if err != nil {
		return false // different volumes on Windows, and so not under the Root
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// foldCase is the comparison's case handling: folded on Windows and exact elsewhere,
// because a case-sensitive check on a case-insensitive filesystem refuses a
// directory the OS considers inside the Root.
func foldCase(path string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}
