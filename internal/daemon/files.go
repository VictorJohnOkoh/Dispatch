package daemon

import (
	"fmt"
	"os"

	"github.com/VictorJohnOkoh/Dispatch/internal/workspace"
)

// files is the contained file access one Session's Adapter is given. OpenCode
// delegates its writes to its client, so this is where they land, and it is the
// second lever the Daemon has on one: a path outside the Workspace Root is
// refused here even though the permission gate passed.
//
// The path is resolved against the Session's working directory every time, and
// not once at the start. The tree is mutable and the Harness is what mutates it,
// so a symlink that pointed inside the Root a minute ago may not now.
type files struct {
	root workspace.Root
	dir  string
}

// WriteTextFile writes one file the Harness asked for, or refuses it. The file
// keeps the permissions of a file the user made, because a Harness runs as an
// ordinary process and nothing here is a sandbox.
func (f files) WriteTextFile(path, content string) error {
	resolved, err := f.root.Contain(f.dir, path)
	if err != nil {
		return fmt.Errorf("daemon: this write was refused: %w", err)
	}
	if err := os.WriteFile(resolved, []byte(content), 0o644); err != nil {
		return fmt.Errorf("daemon: this write failed: %w", err)
	}
	return nil
}
