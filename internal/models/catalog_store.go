package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// CatalogPath returns <home>/.config/fyr/models.json.
func CatalogPath(home string) string {
	home = filepath.Clean(strings.TrimSpace(home))
	return filepath.Join(home, harness.StateDir, "models.json")
}

// CatalogExists reports whether a regular models.json file exists under home.
func CatalogExists(home string) bool {
	clean := filepath.Clean(strings.TrimSpace(home))
	if clean == "" || clean == "." {
		return false
	}
	st, err := os.Lstat(CatalogPath(clean))
	return err == nil && st.Mode().IsRegular()
}

// CheckHomeDir verifies that home and any existing parent directories down to
// <home>/.config/fyr are regular directories (never symlinks).
func CheckHomeDir(home string) error {
	clean := filepath.Clean(strings.TrimSpace(home))
	if clean == "" || clean == "." {
		return errors.New("empty home")
	}
	st, err := os.Lstat(clean)
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
		return fmt.Errorf("refusing non-directory home %s", clean)
	}
	cur := clean
	for _, part := range strings.Split(filepath.ToSlash(harness.StateDir), "/") {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		st, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			break
		}
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			return fmt.Errorf("refusing non-directory state path %s", cur)
		}
	}
	return nil
}

// LoadCatalog reads <home>/.config/fyr/models.json if present.
func LoadCatalog(home string) (Catalog, bool, error) {
	if strings.TrimSpace(home) == "" {
		return Catalog{}, false, nil
	}
	home = filepath.Clean(strings.TrimSpace(home))
	if err := CheckHomeDir(home); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Catalog{}, false, err
	}
	p := CatalogPath(home)
	st, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return Catalog{}, false, nil
	}
	if err != nil {
		return Catalog{}, false, err
	}
	if !st.Mode().IsRegular() {
		return Catalog{}, false, fmt.Errorf("models catalog %s is not a regular file", p)
	}
	if st.Size() > 4<<20 {
		return Catalog{}, false, fmt.Errorf("models catalog %s exceeds 4 MiB", p)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return Catalog{}, false, err
	}
	var cat Catalog
	if err := json.Unmarshal(data, &cat); err != nil {
		return Catalog{}, false, err
	}
	for i := range cat.Entries {
		ApplyModelProfile(&cat.Entries[i])
	}
	return cat, true, nil
}

// SaveCatalog writes cat atomically (0600) to <home>/.config/fyr/models.json, refusing symlinks.
func SaveCatalog(home string, cat Catalog) error {
	if strings.TrimSpace(home) == "" {
		return errors.New("save catalog: empty home")
	}
	home = filepath.Clean(strings.TrimSpace(home))
	if err := ensureHomeDirSafe(home); err != nil {
		return err
	}
	target := CatalogPath(home)
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if st, err := os.Lstat(dir); err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing non-directory state path %s", dir)
	}
	if st, err := os.Lstat(target); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink %s", target)
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("refusing non-regular %s", target)
		}
	}
	data, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, "models.json.*.tmp")
	if err != nil {
		return err
	}
	return writeTempAndRename(tmp, data, target)
}

func writeTempAndRename(tmp *os.File, data []byte, target string) error {
	tmpName := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// PersistCatalogPoolsBestEffort updates quota_updated_at and pools inside an
// existing <home>/.config/fyr/models.json file, ignoring errors.
func PersistCatalogPoolsBestEffort(home string, pools []Pool) {
	if strings.TrimSpace(home) == "" || len(pools) == 0 || !CatalogExists(home) {
		return
	}
	cat, ok, err := LoadCatalog(home)
	if err != nil || !ok {
		return
	}
	cat.QuotaUpdatedAt = time.Now().UTC().Format(time.RFC3339)
	cat.Pools = append([]Pool(nil), pools...)
	_ = SaveCatalog(home, cat)
}

func ensureHomeDirSafe(home string) error {
	clean := filepath.Clean(strings.TrimSpace(home))
	if clean == "" || clean == "." {
		return errors.New("empty home")
	}
	if _, err := os.Lstat(clean); errors.Is(err, fs.ErrNotExist) {
		parent := filepath.Dir(clean)
		if st, perr := os.Lstat(parent); perr == nil && st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink parent %s", parent)
		}
		if err := os.MkdirAll(clean, 0o700); err != nil {
			return err
		}
	}
	return CheckHomeDir(clean)
}
