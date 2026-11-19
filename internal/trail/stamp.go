package trail

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// VerifyStampRelPath is the repository-relative path of the verification stamp
// written by `fyr verify` and read by the stop hook and `fyr close`.
const VerifyStampRelPath = ".agents/scratch/verify.stamp"

const maxVerifyStampBytes = 4096

// ErrStampSymlink marks a symlink encountered in .agents, .agents/scratch, or verify.stamp.
var ErrStampSymlink = errors.New("symlinked stamp or scratch path")

// ErrStampNotRegular marks a non-directory scratch segment or non-regular stamp file.
var ErrStampNotRegular = errors.New("stamp or scratch path is not a regular file")

// VerifyStamp represents the JSON contents of VerifyStampRelPath, supporting
// both camelCase and legacy snake_case tree digest keys.
type VerifyStamp struct {
	TreeSHA256   string `json:"treeSha256"`
	TreeSHA256Sn string `json:"tree_sha256,omitempty"`
	UpdatedAt    string `json:"updatedAt"`
	Branch       string `json:"branch,omitempty"`
	Head         string `json:"head,omitempty"`
	OK           bool   `json:"ok"`
}

// CheckScratchNoSymlinks ensures root, .agents, and .agents/scratch are not
// symlinks and are directories when present.
func CheckScratchNoSymlinks(root string) error {
	for _, rel := range []string{"", ".agents", filepath.Join(".agents", "scratch")} {
		p := root
		if rel != "" {
			p = filepath.Join(root, rel)
		}
		info, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s is a symlink", ErrStampSymlink, p)
		}
		if !info.IsDir() {
			return fmt.Errorf("%w: expected directory at %s", ErrStampNotRegular, p)
		}
	}
	return nil
}

// ReadVerifyStamp safely reads and decodes VerifyStampRelPath under root
// without following symlinks and bounded to 4096 bytes.
func ReadVerifyStamp(root string) (VerifyStamp, error) {
	if strings.TrimSpace(root) == "" {
		return VerifyStamp{}, os.ErrInvalid
	}
	if err := CheckScratchNoSymlinks(root); err != nil {
		return VerifyStamp{}, err
	}
	p := filepath.Join(root, filepath.FromSlash(VerifyStampRelPath))
	f, err := OpenRegular(p)
	if err != nil {
		return VerifyStamp{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxVerifyStampBytes+1))
	if err != nil {
		return VerifyStamp{}, err
	}
	if len(data) > maxVerifyStampBytes {
		return VerifyStamp{}, fmt.Errorf("parse %s: exceeds %d bytes", VerifyStampRelPath, maxVerifyStampBytes)
	}
	var st VerifyStamp
	if err := json.Unmarshal(data, &st); err != nil {
		return VerifyStamp{}, fmt.Errorf("parse %s: %w", VerifyStampRelPath, err)
	}
	return st, nil
}
