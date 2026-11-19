package gate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/kesokaj/fyr/internal/trail"
)

// StampRelPath matches trail.VerifyStampRelPath for stop-hook compatibility.
const StampRelPath = trail.VerifyStampRelPath

// Stamp records a passing verification run bound to a Go source tree digest.
type Stamp struct {
	TreeSHA256 string `json:"treeSha256"`
	UpdatedAt  string `json:"updatedAt"`
	Branch     string `json:"branch,omitempty"`
	Head       string `json:"head,omitempty"`
	OK         bool   `json:"ok"`
}

func ensureStampNotSymlink(root string) error {
	p := filepath.Join(root, filepath.FromSlash(StampRelPath))
	info, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return refusef("stamp path %s is not a regular file", p)
	}
	return nil
}

func removeStaleStamp(root string) error {
	if err := checkScratchNoSymlinks(root); err != nil {
		return err
	}
	if err := ensureStampNotSymlink(root); err != nil {
		return err
	}
	err := os.Remove(filepath.Join(root, filepath.FromSlash(StampRelPath)))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ReadStamp reads and validates StampRelPath under root without following symlinks.
func ReadStamp(root string) (Stamp, error) {
	st, err := trail.ReadVerifyStamp(root)
	if err != nil {
		if errors.Is(err, trail.ErrStampSymlink) || errors.Is(err, trail.ErrStampNotRegular) {
			return Stamp{}, refusef("%v", err)
		}
		return Stamp{}, err
	}
	return Stamp{
		TreeSHA256: st.TreeSHA256,
		UpdatedAt:  st.UpdatedAt,
		Branch:     st.Branch,
		Head:       st.Head,
		OK:         st.OK,
	}, nil
}

// WriteStamp atomically writes StampRelPath under root with 0600 permissions.
func WriteStamp(root string, st Stamp) (string, error) {
	if err := checkScratchNoSymlinks(root); err != nil {
		return "", err
	}
	if err := ensureStampNotSymlink(root); err != nil {
		return "", err
	}
	scratchDir := filepath.Join(root, ".agents", "scratch")
	if err := os.MkdirAll(scratchDir, 0o755); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(scratchDir, "verify.stamp.*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmpName) }
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", err
	}
	dst := filepath.Join(root, filepath.FromSlash(StampRelPath))
	if err := os.Rename(tmpName, dst); err != nil {
		_ = os.Remove(tmpName)
		return "", err
	}
	return dst, nil
}
