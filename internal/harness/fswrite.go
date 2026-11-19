package harness

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteAtomic writes through a temp file and rename, so a half-written
// context file is never visible and a running fyr binary can be
// replaced on Linux, where opening it for writing would fail with ETXTBSY.
func WriteAtomic(dest string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".fyr-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return abandon(tmp, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		return abandon(tmp, err)
	}
	// Sync before the rename: without it a crash between the two can leave
	// the rename durable and the bytes not, so the deployed file is a
	// zero-length or truncated context that no reader can tell from a real
	// one (security review of the settings write, 2026-09-16, finding 2 —
	// the same pattern was fixed there first).
	if err := tmp.Sync(); err != nil {
		return abandon(tmp, err)
	}
	// Close before the rename, as the settings writer always did: a close
	// failure then leaves dest untouched, so an error from this function
	// always means dest is unchanged; and on Linux a binary renamed into
	// place while still open cannot be executed until the close (ETXTBSY).
	// Round 2 of the 2026-09-30 review had these two lines the other way.
	if err := tmp.Close(); err != nil {
		return abandon(tmp, err)
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return abandon(tmp, err)
	}
	return nil
}

// abandon closes and removes the temp file of a failed write and returns the
// failure, naming the temp file when even its removal fails so nothing is
// left behind unseen (the settings writer said so before it shared this
// helper; code review 2026-09-30).
func abandon(tmp *os.File, err error) error {
	tmp.Close() // already closed on the rename branch, which is harmless; the file is discarded next
	if rm := os.Remove(tmp.Name()); rm != nil {
		return fmt.Errorf("%w; temp file %s left behind: %v", err, tmp.Name(), rm)
	}
	return err
}

// isLinkedPath reports whether home/rel itself or any directory between home
// and home/rel is a symbolic link.
func isLinkedPath(home, rel string) bool {
	if fi, err := os.Lstat(filepath.Join(home, rel)); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return true
	}
	return underLink(home, rel)
}

// underLink reports whether any directory between home and home/rel is a
// symlink: a file under a linked directory is written, chmodded or removed
// through the link otherwise (security review 2026-09-30 of IN3).
func underLink(home, rel string) bool {
	dir := filepath.Dir(rel)
	for dir != "." && dir != "" && dir != string(filepath.Separator) {
		if fi, err := os.Lstat(filepath.Join(home, dir)); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return true
		}
		dir = filepath.Dir(dir)
	}
	return false
}

// pruneEmptyDirs removes now-empty directories from dir upward, stopping at
// the vendor root and anything above it so uninstall never removes
// ~/.gemini/config itself. ~/.claude stays a stop too: the resync that sheds
// a pre-ADR-062 install's files there must leave the directory, which holds
// the operator's own settings.
func pruneEmptyDirs(home, dir string) {
	stop := map[string]bool{
		home:                                       true,
		filepath.Join(home, ".claude"):             true, // an install before ADR-062 wrote there
		filepath.Join(home, ".gemini"):             true,
		filepath.Join(home, AgyRoot):               true,
		filepath.Join(home, ".config"):             true,
		filepath.Join(home, ".local"):              true,
		filepath.Join(home, filepath.Dir(BinPath)): true,
	}
	for !stop[dir] && len(dir) > len(home) {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// MkdirAllTracked is os.MkdirAll that returns the directories it created,
// outermost first, so a caller can remove exactly those again (RemoveCreated).
// "" creates nothing.
func MkdirAllTracked(dir string) ([]string, error) {
	if dir == "" {
		return nil, nil
	}
	var missing []string
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if _, err := os.Lstat(d); err == nil {
			break
		}
		missing = append(missing, d)
		if filepath.Dir(d) == d {
			break
		}
	}
	var created []string
	for k := len(missing) - 1; k >= 0; k-- {
		err := os.Mkdir(missing[k], 0o700)
		if errors.Is(err, fs.ErrExist) {
			continue // made by someone else meanwhile: not ours to remove
		}
		if err != nil {
			RemoveCreated(created)
			return nil, err
		}
		created = append(created, missing[k])
	}
	return created, nil
}

// RemoveCreated removes what MkdirAllTracked created, deepest first; a
// directory that is no longer empty is left alone.
func RemoveCreated(created []string) {
	for k := len(created) - 1; k >= 0; k-- {
		os.Remove(created[k])
	}
}
