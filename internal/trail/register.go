package trail

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// markerName is the registration marker's file name in the git common
// directory: inside .git/, so every linked worktree shares it and no clone
// carries it (ADR-068, audit-choices D2).
const markerName = "fyr-registered"

// Registered reports whether the operator registered the checkout holding
// root with `fyr index --register` under the fyr state directory stateDir
// (~/.config/fyr): the marker is a regular file whose seal is the HMAC, under
// that directory's registration key, of the checkout's main top level. A
// copy of the checkout elsewhere, a tree whose .git file borrows another
// repository's git dir, or a planted marker carries no valid seal (ADR-068a,
// Phase 2 security review findings 1-2). Every error reads as false, so a
// cross-project consumer that asks leaves the checkout out, as does a
// stateDir that is not absolute.
func Registered(stateDir, root string) bool {
	if !filepath.IsAbs(stateDir) {
		return false
	}
	common, main, _, err := roots(root)
	if err != nil {
		return false
	}
	key, err := readKey(stateDir)
	if err != nil {
		return false
	}
	got, err := readSeal(filepath.Join(common, markerName))
	if err != nil {
		return false
	}
	return hmac.Equal(got, seal(key, main))
}

// Register writes the marker for the checkout holding root, dated now and
// sealed for its main top level under stateDir's registration key, which it
// creates on first use; a rerun rewrites it. It refuses to write through a
// symlink or over anything but a regular file at the marker's name, and
// replaces the file by rename, so a link planted between the check and the
// write is replaced, never followed. It returns the marker's path.
func Register(stateDir, root string, now time.Time) (string, error) {
	if !filepath.IsAbs(stateDir) {
		// A relative one would resolve against the working directory, the
		// very checkout being registered (Phase 2 security review round 2).
		return "", fmt.Errorf("the state directory %s is not an absolute path", stateDir)
	}
	common, main, _, err := roots(root)
	if err != nil {
		return "", err
	}
	path := filepath.Join(common, markerName)
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file; refusing to replace it", path)
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	key, err := ensureKey(stateDir)
	if err != nil {
		return "", err
	}
	body := fmt.Sprintf("registered %s\nseal %s\n", now.Format(time.DateOnly), hex.EncodeToString(seal(key, main)))
	return path, writeReplacing(common, path, body)
}

// seal binds a registration to one checkout's main top level and one fyr
// state directory's key.
func seal(key []byte, main string) []byte {
	m := hmac.New(sha256.New, key)
	io.WriteString(m, "fyr-registered\x00"+main)
	return m.Sum(nil)
}

// readSeal reads the marker's seal line; anything but a small regular file
// holding one is an error.
func readSeal(path string) ([]byte, error) {
	f, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(io.LimitReader(f, 4096))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "seal "); ok {
			return hex.DecodeString(v)
		}
	}
	return nil, fmt.Errorf("%s holds no seal", path)
}

// writeReplacing writes body to a temporary file in dir and renames it over
// path.
func writeReplacing(dir, path, body string) error {
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, werr := io.WriteString(f, body)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return werr
	}
	return os.Rename(f.Name(), path)
}
