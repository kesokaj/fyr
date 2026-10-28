package trail

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// keyName is the registration key's file in the fyr state directory: the
// secret every marker's seal is made with, so a marker this home did not
// write never counts (ADR-068a).
const keyName = "registration.key"

// readKey reads stateDir's registration key; a missing, linked or odd file
// is an error.
func readKey(stateDir string) ([]byte, error) {
	f, err := openRegular(filepath.Join(stateDir, keyName))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return nil, err
	}
	if len(b) < 16 {
		return nil, fmt.Errorf("%s is too short to be a key", f.Name())
	}
	return b, nil
}

// ensureKey reads stateDir's registration key, creating the directory (0700)
// and the key (0600, random) first when there is none. The key is written to
// a temporary file and hard-linked into place, which never replaces an
// existing key, so two first registrations at once agree on one.
func ensureKey(stateDir string) ([]byte, error) {
	if key, err := readKey(stateDir); err == nil {
		return key, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(stateDir, "."+keyName+"-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	_, werr := io.WriteString(f, rand.Text()+"\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return nil, werr
	}
	if err := os.Link(f.Name(), filepath.Join(stateDir, keyName)); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	return readKey(stateDir)
}
