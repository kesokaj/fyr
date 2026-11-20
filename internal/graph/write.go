package graph

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteFile writes data to path with mode 0600, refusing symlinks via Lstat and
// O_NOFOLLOW. Without force, an existing file is refused with "(pass --force)".
func WriteFile(path string, data []byte, force bool) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("graph: output path is empty")
	}
	dir := filepath.Dir(path)
	if err := ensureRealParentDir(dir); err != nil {
		return err
	}
	prevInfo, err := checkExistingTarget(path, force)
	if err != nil {
		return err
	}
	createExcl := !force || prevInfo == nil
	f, err := openTargetNoFollow(path, createExcl)
	if err != nil {
		if !force && errors.Is(err, os.ErrExist) {
			return fmt.Errorf("refusing to overwrite %s (pass --force)", path)
		}
		if force && prevInfo == nil && errors.Is(err, os.ErrExist) {
			return fmt.Errorf("refusing concurrently created target %s", path)
		}
		return err
	}
	return writeAndCloseTarget(f, path, data, force && prevInfo != nil, prevInfo)
}

func ensureRealParentDir(dir string) error {
	cur := filepath.Clean(dir)
	var missing []string
	for {
		fi, err := os.Lstat(cur)
		if err == nil {
			if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
				return fmt.Errorf("refusing symlinked or non-directory parent %s", cur)
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return err
		}
		missing = append(missing, filepath.Base(cur))
		cur = parent
	}
	if len(missing) == 0 {
		return nil
	}
	return mkdirNoSymlinks(cur, missing)
}

func mkdirNoSymlinks(ancestor string, missingRev []string) error {
	r, err := os.OpenRoot(ancestor)
	if err != nil {
		return err
	}
	for i := len(missingRev) - 1; i >= 0; i-- {
		seg := missingRev[i]
		if err := r.Mkdir(seg, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			_ = r.Close()
			return err
		}
		next, err := r.OpenRoot(seg)
		_ = r.Close()
		if err != nil {
			return fmt.Errorf("parent path %s is not a real directory: %w", seg, err)
		}
		r = next
	}
	return r.Close()
}

func checkExistingTarget(path string, force bool) (os.FileInfo, error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refusing symlink target %s", path)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("refusing non-regular file %s", path)
	}
	if !force {
		return nil, fmt.Errorf("refusing to overwrite %s (pass --force)", path)
	}
	return fi, nil
}

func writeAndCloseTarget(f *os.File, path string, data []byte, force bool, prevInfo os.FileInfo) error {
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	if !fi.Mode().IsRegular() || (force && prevInfo != nil && !os.SameFile(prevInfo, fi)) {
		_ = f.Close()
		return fmt.Errorf("refusing non-regular or swapped target %s", path)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if force {
		if err := f.Truncate(0); err != nil {
			_ = f.Close()
			return err
		}
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		if !force {
			_ = os.Remove(path)
		}
		return err
	}
	return f.Close()
}
