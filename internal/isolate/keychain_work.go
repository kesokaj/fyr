package isolate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// EnsureWorkDir creates <home>/.config/fyr/work with 0700 permissions,
// refusing any symlink along the path from home down to work/.
func EnsureWorkDir(home string) (string, error) {
	cur, err := walkWorkDir(home, true)
	if err != nil {
		return "", err
	}
	if err := os.Chmod(cur, 0o700); err != nil {
		return "", err
	}
	return cur, nil
}

// CheckWorkDir is EnsureWorkDir's refusals, read only, in its words: it
// creates and changes nothing, and passes a directory that is not there
// yet, which EnsureWorkDir would create (read-only preflight, ADR-504a).
func CheckWorkDir(home string) error {
	_, err := walkWorkDir(home, false)
	return err
}

// walkWorkDir walks from home down to work/, refusing a home that is no
// directory and any symlink or other non-directory on the way. With mk it
// creates each directory that is missing; without it, the walk passes at
// the first one that is missing.
func walkWorkDir(home string, mk bool) (string, error) {
	cur := filepath.Clean(home)
	st, err := os.Lstat(cur)
	if errors.Is(err, fs.ErrNotExist) {
		if !mk {
			return filepath.Join(cur, filepath.FromSlash(filepath.Dir(WorkTokenRel))), nil
		}
		if err := os.MkdirAll(cur, 0o700); err != nil {
			return "", fmt.Errorf("home %s is not a directory: %w", home, err)
		}
		st, err = os.Lstat(cur)
	}
	if err != nil || !st.IsDir() {
		return "", fmt.Errorf("home %s is not a directory", home)
	}
	for _, part := range strings.Split(filepath.ToSlash(filepath.Dir(WorkTokenRel)), "/") {
		cur = filepath.Join(cur, part)
		if mk {
			if err := mkdirExisting(cur); err != nil {
				return "", err
			}
		}
		st, err := os.Lstat(cur)
		switch {
		case !mk && errors.Is(err, fs.ErrNotExist):
			return cur, nil
		case err != nil:
			return "", err
		case !st.IsDir():
			return "", fmt.Errorf("%s is not a regular directory", cur)
		}
	}
	return cur, nil
}

// mkdirExisting makes dir 0700, passing when something is there already,
// which the walk's Lstat then checks.
func mkdirExisting(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return nil
}

func lockWorkSlots(workDir string, create bool) (func(), error) {
	flags := os.O_RDONLY | syscall.O_NOFOLLOW
	if create {
		flags = os.O_RDWR | os.O_CREATE | syscall.O_NOFOLLOW
	}
	f, err := os.OpenFile(filepath.Join(workDir, workSlotsLock), flags, 0o600)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err == nil && !st.Mode().IsRegular() {
		err = fmt.Errorf("%s is not a regular file", f.Name())
	}
	if err == nil {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

func writeWorkToken(home, wp string, data []byte) error {
	workDir, err := EnsureWorkDir(home)
	if err != nil {
		return err
	}
	unlock, err := lockWorkSlots(workDir, true)
	if err != nil {
		return err
	}
	defer unlock()
	if st, err := os.Lstat(wp); err == nil && !st.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", wp)
	}
	tmp, err := os.CreateTemp(workDir, ".token-*")
	if err != nil {
		return err
	}
	return commitWorkTokenTemp(tmp, wp, data)
}

func commitWorkTokenTemp(tmp *os.File, wp string, data []byte) error {
	name := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, wp); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// SyncWorkToken materializes the container OAuth token file for a docker-mode
// run after all preflight refusals have passed: it returns realHome's TokenRel
// when present, or on darwin extracts the macOS Keychain token and writes it
// atomically (0600 in a 0700 directory) to WorkTokenRel under home (or
// realHome when home is empty), falling back to an existing regular
// WorkTokenRel reached from its home through no symlink, under home and
// then under realHome, when Keychain lookup is unavailable (D7, ADR-503,
// BUG-512). CheckWorkLogin is its read-only twin.
func SyncWorkToken(home, realHome, goos string) (string, error) {
	return workToken(home, realHome, goos, true)
}

// CheckWorkLogin is SyncWorkToken's refusals, read only, in its words:
// preflight checks resolve the login the way the run will, under the
// same home, and create and change nothing (BUG-510); only a write that
// fails on disk is the run's alone. It returns the token path the run
// links, which on the Keychain branch the run writes last.
func CheckWorkLogin(home, realHome, goos string) (string, error) {
	return workToken(home, realHome, goos, false)
}

// workToken is the one resolution behind SyncWorkToken (write) and
// CheckWorkLogin (read only): the check refuses what the run refuses,
// but for a write that fails on disk. An existing WorkTokenRel is walked
// from its home as the Keychain branch's write is, so a symlinked
// directory on the way is refused there too (BUG-512).
func workToken(home, realHome, goos string, write bool) (string, error) {
	p := filepath.Join(realHome, filepath.FromSlash(TokenRel))
	if st, err := os.Lstat(p); err == nil {
		if !st.Mode().IsRegular() {
			return "", fmt.Errorf("no Antigravity login at %s", p)
		}
		return p, nil
	}
	if home == "" {
		home = realHome
	}
	wp := filepath.Join(home, filepath.FromSlash(WorkTokenRel))
	have, err := workTokenFile(home, wp)
	if err != nil {
		return "", err
	}
	rwp := filepath.Join(realHome, filepath.FromSlash(WorkTokenRel))
	rhave, err := workTokenFile(realHome, rwp)
	if err != nil {
		return "", err
	}
	done, err := keychainWorkToken(home, realHome, wp, goos, write)
	switch {
	case err != nil:
		return "", err
	case done, have:
		return wp, nil
	case rhave:
		return rwp, nil
	}
	return "", fmt.Errorf("no Antigravity login at %s", p)
}

// workTokenFile reports whether wp, WorkTokenRel under home, is a regular
// file, refusing a wp that is anything else, a work/ directory above it
// that is no directory, a symlink included, and, when wp is there, any
// symlink from home down to work/ (CheckWorkDir's walk, BUG-512).
func workTokenFile(home, wp string) (bool, error) {
	st, err := os.Lstat(wp)
	if err == nil && !st.Mode().IsRegular() {
		return false, fmt.Errorf("no Antigravity login at %s", wp)
	}
	if dst, derr := os.Lstat(filepath.Dir(wp)); derr == nil && !dst.IsDir() {
		return false, fmt.Errorf("no Antigravity login at %s (%s is not a regular directory)", wp, filepath.Dir(wp))
	}
	if err != nil {
		return false, nil
	}
	if werr := CheckWorkDir(home); werr != nil {
		return false, fmt.Errorf("no Antigravity login at %s (%v)", wp, werr)
	}
	return true, nil
}

// keychainWorkToken is workToken's Keychain branch: on darwin, with
// realHome's Keychain folder and a token the Keychain gives, it decodes the
// token and writes it to wp under home, or without write checks the walk
// the write takes (CheckWorkDir), so both refuse in the same words. done is
// false when there is no Keychain token to use, and the caller falls back.
func keychainWorkToken(home, realHome, wp, goos string, write bool) (done bool, err error) {
	if goos != "darwin" {
		return false, nil
	}
	data, ok, err := readDecodedKeychainToken(realHome)
	if err == nil && !ok {
		return false, nil
	}
	if err == nil {
		if write {
			err = writeWorkToken(home, wp, data)
		} else {
			err = CheckWorkDir(home)
		}
	}
	if err != nil {
		return false, fmt.Errorf("no Antigravity login at %s (%v)", wp, err)
	}
	return true, nil
}
