package isolate

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
)

const (
	modelsCatalogFile   = "models.json"
	maxModelsStageBytes = 4 << 20
)

var forbiddenBundleRoots = []string{
	"/", "/home", "/Users", "/tmp", "/var", "/var/tmp", "/opt", "/srv",
	"/usr", "/usr/local", "/bin", "/sbin", "/etc", "/root",
	"/Applications", "/Library", "/System",
}

// StageModelsCatalog copies a regular models.json from realHome into
// scratchHome under harness.StateDir with mode 0400. Missing source files are
// a no-op; symlinked directories or non-regular files are refused.
func StageModelsCatalog(realHome, scratchHome string) error {
	realClean := filepath.Clean(strings.TrimSpace(realHome))
	scratchClean := filepath.Clean(strings.TrimSpace(scratchHome))
	if realClean == "" || realClean == "." || scratchClean == "" || scratchClean == "." {
		return nil
	}
	ok, err := checkStateDirChain(realClean, false)
	if err != nil || !ok {
		return err
	}
	src := filepath.Join(realClean, harness.StateDir, modelsCatalogFile)
	data, present, err := readRegularModelsFile(src)
	if err != nil || !present {
		return err
	}
	if _, err := checkStateDirChain(scratchClean, true); err != nil {
		return err
	}
	dst := filepath.Join(scratchClean, harness.StateDir, modelsCatalogFile)
	return writeReadOnlyFileAtomic(dst, data, 0o400)
}

func checkStateDirChain(home string, create bool) (bool, error) {
	st, err := os.Lstat(home)
	if errors.Is(err, fs.ErrNotExist) {
		if !create {
			return false, nil
		}
		if err := os.MkdirAll(home, 0o700); err != nil {
			return false, err
		}
		st, err = os.Lstat(home)
	}
	if err != nil {
		return false, err
	}
	if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
		return false, fmt.Errorf("%s is not a regular directory", home)
	}
	cur := home
	for _, part := range strings.Split(filepath.ToSlash(harness.StateDir), "/") {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		if create {
			if err := mkdirExisting(cur); err != nil {
				return false, err
			}
		}
		st, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) && !create {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			return false, fmt.Errorf("%s is not a regular directory", cur)
		}
	}
	return true, nil
}

func readRegularModelsFile(src string) ([]byte, bool, error) {
	st, err := os.Lstat(src)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !st.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%s is not a regular file", src)
	}
	f, err := OpenRegular(src, os.O_RDONLY)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxModelsStageBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > maxModelsStageBytes {
		return nil, false, fmt.Errorf("%s exceeds %d bytes", src, maxModelsStageBytes)
	}
	return data, true, nil
}

func writeReadOnlyFileAtomic(dst string, data []byte, mode os.FileMode) error {
	if st, err := os.Lstat(dst); err == nil && !st.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", dst)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".models-stage-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func appendAgyROPaths(dst []string, realHome, bin string) []string {
	if strings.TrimSpace(bin) == "" {
		return dst
	}
	dst = addPathPair(dst, bin)
	if bundle := agyInstallBundleDir(realHome, bin); bundle != "" {
		dst = addPathPair(dst, bundle)
	}
	return dst
}

func agyInstallBundleDir(realHome, bin string) string {
	resolved := filepath.Clean(bin)
	if r, err := filepath.EvalSymlinks(resolved); err == nil && r != "" {
		resolved = filepath.Clean(r)
	}
	if !filepath.IsAbs(resolved) {
		return ""
	}
	home := normalizeHomePath(effectiveRealHome(realHome))
	for cur := filepath.Dir(resolved); cur != "" && cur != filepath.Dir(cur); cur = filepath.Dir(cur) {
		if strings.HasSuffix(strings.ToLower(filepath.Base(cur)), ".app") {
			if isSafeAgyBundleDir(cur, home) {
				return cur
			}
			return ""
		}
	}
	if !isAgyExecutableName(resolved) {
		return ""
	}
	dir := filepath.Dir(resolved)
	if strings.EqualFold(filepath.Base(dir), "bin") {
		grand := filepath.Dir(dir)
		if hasAgyBundleName(grand) && isSafeAgyBundleDir(grand, home) {
			return grand
		}
		return ""
	}
	if hasAgyBundleName(dir) && isSafeAgyBundleDir(dir, home) {
		return dir
	}
	return ""
}

func normalizeHomePath(home string) string {
	home = strings.TrimSpace(home)
	if home == "" {
		return ""
	}
	clean := filepath.Clean(home)
	if r, err := filepath.EvalSymlinks(clean); err == nil && r != "" {
		return filepath.Clean(r)
	}
	return clean
}

func isAgyExecutableName(p string) bool {
	base := strings.ToLower(filepath.Base(p))
	return base == "agy" || strings.HasPrefix(base, "agy-") || strings.Contains(base, "antigravity")
}

func hasAgyBundleName(dir string) bool {
	base := strings.ToLower(filepath.Base(dir))
	return strings.Contains(base, "antigravity") || base == "agy" || strings.HasPrefix(base, "agy-") || strings.HasPrefix(base, ".agy")
}

func isSafeAgyBundleDir(dir, realHome string) bool {
	clean := filepath.Clean(dir)
	if !filepath.IsAbs(clean) || clean == "/" || filepath.Dir(clean) == clean {
		return false
	}
	if isBroadOrTempDir(clean) {
		return false
	}
	if realHome == "" {
		return true
	}
	return !violatesHomeConfinement(clean, realHome)
}

func isBroadOrTempDir(clean string) bool {
	tmp := filepath.Clean(os.TempDir())
	if clean == tmp {
		return true
	}
	if resTmp, err := filepath.EvalSymlinks(tmp); err == nil && clean == filepath.Clean(resTmp) {
		return true
	}
	for _, b := range forbiddenBundleRoots {
		if clean == b {
			return true
		}
	}
	return false
}

func violatesHomeConfinement(clean, realHome string) bool {
	if within(realHome, clean) {
		return true
	}
	protectedContainers := []string{".local", filepath.Join(".local", "bin"), filepath.Join(".local", "share"), "Applications", "Library"}
	for _, rel := range protectedContainers {
		if within(filepath.Join(realHome, rel), clean) {
			return true
		}
	}
	forbiddenTrees := []string{".config", harness.StateDir, ".ssh", ".gnupg", ".gemini", ".aws", ".kube"}
	for _, rel := range forbiddenTrees {
		target := filepath.Join(realHome, rel)
		if within(clean, target) || within(target, clean) {
			return true
		}
	}
	return false
}
