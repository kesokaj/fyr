package guard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/trail"
)

// MaxFileBytes caps regular files read by git-guard helpers.
const MaxFileBytes = 1 << 20

// ErrNotCheckout is returned when a directory is not inside a valid git checkout.
var ErrNotCheckout = errors.New("search: not inside a git checkout; use grep")

// ProjectDB returns the repo-local search.db path under root.
func ProjectDB(root string) string {
	return filepath.Join(root, ".agents", "scratch", "search.db")
}

// IsRefusedAutoInitDir reports whether absDir is a system, temp, or home directory that must never be auto-initialized.
func IsRefusedAutoInitDir(absDir string) bool {
	clean := filepath.Clean(absDir)
	if clean == "/" || clean == "." {
		return true
	}
	refused := []string{"/", os.TempDir(), os.Getenv("HOME")}
	if uh, err := os.UserHomeDir(); err == nil && uh != "" {
		refused = append(refused, uh)
	}
	for _, r := range refused {
		r = strings.TrimSpace(r)
		if r != "" && SameAutoInitPath(clean, filepath.Clean(r)) {
			return true
		}
	}
	return false
}

// SameAutoInitPath compares two filesystem paths directly or after resolving symlinks.
func SameAutoInitPath(a, b string) bool {
	if a == b {
		return true
	}
	ea, errA := filepath.EvalSymlinks(a)
	eb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && filepath.Clean(ea) == filepath.Clean(eb)
}

// EnsureHookProjectReady validates dir, initializes a minimal git repo when absent, and ensures scratch ignore rules.
func EnsureHookProjectReady(dir string) error {
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
	if IsRefusedAutoInitDir(absDir) {
		return fmt.Errorf("search: refusing to auto-initialize system/root directory %s", absDir)
	}
	if err := trail.CheckScratchNoSymlinks(absDir); err != nil {
		return err
	}
	root, nested, err := EnsureHookGitCheckout(absDir)
	if err != nil {
		return err
	}
	if err := trail.CheckScratchNoSymlinks(root); err != nil {
		return err
	}
	return EnsureHookScratchIgnored(root, nested)
}

// EnsureHookGitCheckout finds or safely initializes a git checkout at absDir.
func EnsureHookGitCheckout(absDir string) (string, []string, error) {
	if IsRefusedAutoInitDir(absDir) {
		return "", nil, fmt.Errorf("search: refusing to auto-initialize system/root directory %s", absDir)
	}
	if root, _, ok := FindGitRootFast(absDir); ok {
		if IsRefusedAutoInitDir(root) {
			return "", nil, fmt.Errorf("search: refusing to auto-initialize system/root directory %s", root)
		}
		return root, nil, nil
	}
	nested := scanNestedGitDirs(absDir, "", 1)
	if err := initMinimalGitRepo(absDir); err != nil {
		return "", nil, fmt.Errorf("search: git init: %w", err)
	}
	root, _, ok := FindGitRootFast(absDir)
	if !ok {
		return "", nil, ErrNotCheckout
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

// FindGitRootFast locates the enclosing git checkout root and common dir without spawning git.
func FindGitRootFast(dir string) (string, string, bool) {
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
		if commonDir, ok := probeDotGitCommonDir(cur); ok {
			return cur, commonDir, true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", "", false
		}
	}
}

func probeDotGitCommonDir(cur string) (string, bool) {
	dotGit := filepath.Join(cur, ".git")
	fi, err := os.Lstat(dotGit)
	if err != nil {
		return "", false
	}
	if fi.IsDir() {
		return dotGit, true
	}
	if fi.Mode().IsRegular() {
		return ResolveGitCommonDirFast(dotGit, cur)
	}
	return "", false
}

// ResolveGitCommonDirFast validates a .git file target and returns its git common dir.
func ResolveGitCommonDirFast(dotGitFile, repoRoot string) (string, bool) {
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
	if err != nil {
		if !os.IsNotExist(err) || !hasGitDirOrConfigBacklink(gd, dotGitFile, repoRoot) {
			return "", false
		}
		return gd, true
	}
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
	if !SameAutoInitPath(filepath.Dir(filepath.Dir(gd)), cd) || !gitDirFileMatches(gd, dotGitFile) {
		return "", false
	}
	return cd, true
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
	return target != "" && SameAutoInitPath(target, dotGitFile)
}

func hasGitDirOrConfigBacklink(gd, dotGitFile, repoRoot string) bool {
	if gitDirFileMatches(gd, dotGitFile) || configWorktreeMatchesRoot(gd, repoRoot) {
		return true
	}
	rel, err := filepath.Rel(filepath.Clean(repoRoot), filepath.Clean(gd))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func configWorktreeMatchesRoot(gd, repoRoot string) bool {
	data, ok := ReadSmallRegularFile(filepath.Join(gd, "config"), MaxFileBytes)
	if !ok {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, hasEq := strings.Cut(line, "=")
		if !hasEq || strings.TrimSpace(k) != "worktree" {
			continue
		}
		if wt := resolveCleanPath(gd, strings.TrimSpace(v)); wt != "" && SameAutoInitPath(wt, repoRoot) {
			return true
		}
	}
	return false
}

func initMinimalGitRepo(absDir string) error {
	gitDir := filepath.Join(absDir, ".git")
	if fi, err := os.Lstat(gitDir); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			return ErrNotCheckout
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

// GuardLedgerFast validates scratch symlinks and ignore state before opening search.db.
func GuardLedgerFast(root, commonDir string, guardProjectFn func(string) error) error {
	if trail.CheckScratchNoSymlinks(root) != nil {
		return guardProjectFn(root)
	}
	if fi, err := os.Lstat(ProjectDB(root)); err == nil && fi.Mode().IsRegular() && ScratchIgnoredFast(root, commonDir) {
		return nil
	}
	return guardProjectFn(root)
}
