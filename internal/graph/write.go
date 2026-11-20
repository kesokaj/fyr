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
	f, err := openTargetNoFollow(path, force)
	if err != nil {
		if !force && errors.Is(err, os.ErrExist) {
			return fmt.Errorf("refusing to overwrite %s (pass --force)", path)
		}
		return err
	}
	return writeAndCloseTarget(f, path, data, force, prevInfo)
}

func ensureRealParentDir(dir string) error {
	if fi, err := os.Lstat(dir); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlinked parent directory %s", dir)
		}
		if !fi.IsDir() {
			return fmt.Errorf("parent path %s is not a directory", dir)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("parent path %s is not a real directory", dir)
	}
	return nil
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
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		if !force {
			_ = os.Remove(path)
		}
		return err
	}
	return f.Close()
}
