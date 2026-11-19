package search

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/trail"
)

func ensureHookProjectReady(dir string) error {
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	for _, p := range []string{dir, absDir} {
		if fi, err := os.Lstat(p); err != nil {
			return err
		} else if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			return fmt.Errorf("search: %s is not a regular directory", p)
		}
	}
	if isRefusedAutoInitDir(absDir) {
		return fmt.Errorf("search: refusing to auto-initialize system/root directory %s", absDir)
	}
	if err := checkScratchNoSymlinks(absDir); err != nil {
		return err
	}
	root, nested, err := ensureHookGitCheckout(absDir)
	if err != nil {
		return err
	}
	if err := checkScratchNoSymlinks(root); err != nil {
		return err
	}
	return ensureHookScratchIgnored(root, nested)
}

func ensureHookGitCheckout(absDir string) (string, []string, error) {
	if isRefusedAutoInitDir(absDir) {
		return "", nil, fmt.Errorf("search: refusing to auto-initialize system/root directory %s", absDir)
	}
	if root, _, ok := findGitRootFast(absDir); ok {
		if isRefusedAutoInitDir(root) {
			return "", nil, fmt.Errorf("search: refusing to auto-initialize system/root directory %s", root)
		}
		return root, nil, nil
	}
	nested := scanNestedGitDirs(absDir, "", 1)
	if err := initMinimalGitRepo(absDir); err != nil {
		return "", nil, fmt.Errorf("search: git init: %w", err)
	}
	root, _, ok := findGitRootFast(absDir)
	if !ok {
		return "", nil, errNotCheckout
	}
	return root, nested, nil
}

func readDotGitFileTarget(dotGitFile, repoRoot string) (string, bool) {
	fi, err := os.Lstat(dotGitFile)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() || fi.Size() > 4096 {
		return "", false
	}
	data, err := os.ReadFile(dotGitFile)
	if err != nil {
		return "", false
	}
	raw, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return "", false
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\r\n") {
		return "", false
	}
	return resolveCleanPath(repoRoot, raw), true
}

func findGitRootFast(dir string) (string, string, bool) {
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", "", false
	}
	if fi, err := os.Stat(absDir); err != nil || !fi.IsDir() {
		return "", "", false
	}
	for cur := absDir; ; cur = filepath.Dir(cur) {
		dotGit := filepath.Join(cur, ".git")
		if fi, err := os.Lstat(dotGit); err == nil {
			if fi.IsDir() {
				return cur, dotGit, true
			}
			if fi.Mode().IsRegular() {
				if commonDir, ok := resolveGitCommonDirFast(dotGit, cur); ok {
					return cur, commonDir, true
				}
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", "", false
		}
	}
}

func resolveGitCommonDirFast(dotGitFile, repoRoot string) (string, bool) {
	gd, ok := readDotGitFileTarget(dotGitFile, repoRoot)
	if !ok || !isRegularDirNoSymlink(gd) {
		return "", false
	}
	return validateCommonDirBacklink(gd, dotGitFile, repoRoot)
}

func resolveCleanPath(base, raw string) string {
	if raw == "" {
		return ""
	}
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(base, raw)
	}
	return filepath.Clean(raw)
}

func isRegularDirNoSymlink(p string) bool {
	if p == "" {
		return false
	}
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode()&os.ModeSymlink == 0 && fi.IsDir()
}

func validateCommonDirBacklink(gd, dotGitFile, repoRoot string) (string, bool) {
	cdPath := filepath.Join(gd, "commondir")
	fi, err := os.Lstat(cdPath)
	if err == nil {
		if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() || fi.Size() > 4096 {
			return "", false
		}
		cdBytes, readErr := os.ReadFile(cdPath)
		if readErr != nil {
			return "", false
		}
		cd := resolveCleanPath(gd, strings.TrimSpace(string(cdBytes)))
		if !isRegularDirNoSymlink(cd) || filepath.Base(filepath.Dir(gd)) != "worktrees" {
			return "", false
		}
		if !sameAutoInitPath(filepath.Dir(filepath.Dir(gd)), cd) || !gitDirFileMatches(gd, dotGitFile) {
			return "", false
		}
		return cd, true
	}
	if !os.IsNotExist(err) || !hasGitDirOrConfigBacklink(gd, dotGitFile, repoRoot) {
		return "", false
	}
	return gd, true
}

func gitDirFileMatches(gd, dotGitFile string) bool {
	p := filepath.Join(gd, "gitdir")
	fi, err := os.Lstat(p)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() || fi.Size() > 4096 {
		return false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	target := resolveCleanPath(gd, strings.TrimSpace(string(data)))
	return target != "" && sameAutoInitPath(target, dotGitFile)
}

func hasGitDirOrConfigBacklink(gd, dotGitFile, repoRoot string) bool {
	if gitDirFileMatches(gd, dotGitFile) {
		return true
	}
	cfgPath := filepath.Join(gd, "config")
	fi, err := os.Lstat(cfgPath)
	if err == nil && fi.Mode()&os.ModeSymlink == 0 && fi.Mode().IsRegular() && fi.Size() <= maxFileBytes {
		if data, err := os.ReadFile(cfgPath); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if k, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == "worktree" {
					wt := resolveCleanPath(gd, strings.TrimSpace(v))
					if wt != "" && sameAutoInitPath(wt, repoRoot) {
						return true
					}
				}
			}
		}
	}
	rel, err := filepath.Rel(filepath.Clean(repoRoot), filepath.Clean(gd))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func initMinimalGitRepo(absDir string) error {
	gitDir := filepath.Join(absDir, ".git")
	if fi, err := os.Lstat(gitDir); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			return errNotCheckout
		}
	}
	for _, sub := range []string{
		filepath.Join("objects", "info"),
		filepath.Join("objects", "pack"),
		filepath.Join("refs", "heads"),
		filepath.Join("refs", "tags"),
		"info",
	} {
		if err := os.MkdirAll(filepath.Join(gitDir, sub), 0o755); err != nil {
			return err
		}
	}
	files := map[string]string{
		"HEAD":        "ref: refs/heads/main\n",
		"config":      "[core]\n\trepositoryformatversion = 0\n\tfilemode = true\n\tbare = false\n",
		"description": "Unnamed repository; edit this file 'description' to name the repository.\n",
	}
	for name, body := range files {
		p := filepath.Join(gitDir, name)
		if _, err := os.Lstat(p); err == nil {
			continue
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func guardLedgerFast(root, commonDir string) error {
	if trail.CheckScratchNoSymlinks(root) != nil {
		return guardProject(root)
	}
	if fi, err := os.Lstat(projectDB(root)); err == nil && fi.Mode().IsRegular() && scratchIgnoredFast(root, commonDir) {
		return nil
	}
	return guardProject(root)
}
