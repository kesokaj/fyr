package harness

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// OldStateDir is the state directory before the rename to fyr. A place that
// ran v2.7.0 or earlier holds its manifest, logs and stores there until a
// writing verb moves it (MigrateStateDir; plan fyr D13).
const OldStateDir = ".config/sh-agent"

// stateDirs says what a home holds of the two state directories: the new one
// present (Lstat: a link counts), and the old one as a directory or as a
// symlink. A file at the old path is no state and counts as absent.
func stateDirs(home string) (cur, oldDir, oldLink bool) {
	_, err := os.Lstat(filepath.Join(home, StateDir))
	cur = err == nil
	if fi, err := os.Lstat(filepath.Join(home, OldStateDir)); err == nil {
		oldLink = fi.Mode()&fs.ModeSymlink != 0
		oldDir = fi.IsDir()
	}
	return cur, oldDir, oldLink
}

const bothStateDirs = "both ~/.config/sh-agent and ~/.config/fyr exist; fyr reads ~/.config/fyr and leaves ~/.config/sh-agent as it is — merge or remove it by hand"

// MigrateStateDir runs first in every verb that writes under the state
// directory, before anything opens it: install's resync reads the previous
// manifest from there, so the old one must be in place before it (D14).
// Only an absent ~/.config/fyr and a plain ~/.config/sh-agent directory move,
// by one rename. Both present is the operator's to merge (F3): neither is
// touched and fyr uses the new one. A symlinked old directory is the
// operator's arrangement and is refused: a verb that ran on would start an
// empty state directory beside it, and the next install would not see the
// old manifest's files.
func MigrateStateDir(home string, out io.Writer) error {
	cur, oldDir, oldLink := stateDirs(home)
	switch {
	case oldLink && !cur:
		return errors.New("~/.config/sh-agent is a symlink, and fyr does not move a link: move what it points to to ~/.config/fyr (or point a ~/.config/fyr link at it), then run the command again")
	case !oldDir && !oldLink:
		return nil
	case cur:
		fmt.Fprintln(out, bothStateDirs)
		return nil
	}
	if err := os.Rename(filepath.Join(home, OldStateDir), filepath.Join(home, StateDir)); err != nil {
		return fmt.Errorf("moving ~/.config/sh-agent to ~/.config/fyr: %w", err)
	}
	fmt.Fprintln(out, "migrated ~/.config/sh-agent → ~/.config/fyr")
	return nil
}

// StateDirNotice is what a read-only verb or mode says instead of migrating,
// "" when there is nothing to say. It reads neither directory's content.
func StateDirNotice(home string) string {
	cur, oldDir, oldLink := stateDirs(home)
	switch {
	case !oldDir && !oldLink:
		return ""
	case cur:
		return bothStateDirs
	case oldLink:
		return "~/.config/sh-agent is a symlink from before the rename; this command reads nothing from it — run fyr install to migrate ~/.config/sh-agent, which tells you what to move"
	}
	return "~/.config/sh-agent holds the state from before the rename; this command reads nothing from it — run fyr install to migrate ~/.config/sh-agent to ~/.config/fyr"
}
