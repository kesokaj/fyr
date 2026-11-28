package guard

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kesokaj/fyr/internal/trail"
)

// ReadSmallRegularFile reads a regular non-symlink file capped at maxBytes.
func ReadSmallRegularFile(path string, maxBytes int64) ([]byte, bool) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxBytes {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

func readRootFile(fsRoot *os.Root, rel string) ([]byte, error) {
	f, err := fsRoot.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func scanNestedGitDirs(base, rel string, depth int) []string {
	entries, err := os.ReadDir(filepath.Join(base, filepath.FromSlash(rel)))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if name == ".git" || name == ".agents" || strings.ContainsAny(name, "\r\n") || e.Type()&os.ModeSymlink != 0 || !e.IsDir() {
			continue
		}
		subRel := name
		if rel != "" {
			subRel = rel + "/" + name
		}
		subAbs := filepath.Join(base, filepath.FromSlash(subRel))
		if _, err := os.Lstat(filepath.Join(subAbs, ".git")); err == nil {
			out = append(out, "/"+subRel+"/")
			continue
		}
		if depth < 3 {
			out = append(out, scanNestedGitDirs(base, subRel, depth+1)...)
		}
	}
	sort.Strings(out)
	return out
}

// EnsureHookScratchIgnored ensures .agents/scratch/* and nested git directories are excluded in git info/exclude.
func EnsureHookScratchIgnored(root string, nested []string) error {
	_, commonDir, ok := FindGitRootFast(root)
	if !ok {
		return ErrNotCheckout
	}
	if len(nested) == 0 && ScratchIgnoredFast(root, commonDir) {
		return nil
	}
	patterns := append([]string{".agents/scratch/*"}, nested...)
	return WritePatternsToValidatedExclude(root, commonDir, patterns)
}

// WritePatternsToValidatedExclude appends ignore patterns to a validated git common dir's info/exclude.
func WritePatternsToValidatedExclude(root, commonDir string, patterns []string) error {
	if commonDir == "" || !filepath.IsAbs(commonDir) {
		return fmt.Errorf("search: invalid git common dir %q", commonDir)
	}
	if fi, err := os.Lstat(commonDir); err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("search: git common dir %s is not a regular directory", commonDir)
	}
	if !ValidateCommonDirForRoot(root, commonDir) {
		return fmt.Errorf("search: git common dir %s does not belong to checkout %s", commonDir, root)
	}
	infoDir := filepath.Join(commonDir, "info")
	if fi, err := os.Lstat(infoDir); err == nil && (fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir()) {
		return fmt.Errorf("search: %s is a symlink or not a directory", infoDir)
	}
	excludeFile := filepath.Join(infoDir, "exclude")
	if fi, err := os.Lstat(excludeFile); err == nil && (fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular()) {
		return fmt.Errorf("search: %s is a symlink or not a regular file", excludeFile)
	}
	return writePatternsToCommonExclude(commonDir, patterns)
}

// ValidateCommonDirForRoot verifies commonDir belongs to the checkout at root.
func ValidateCommonDirForRoot(root, commonDir string) bool {
	if root == "" || commonDir == "" {
		return false
	}
	if SameAutoInitPath(commonDir, filepath.Join(root, ".git")) {
		return true
	}
	gitDir, ok := readDotGitFileTarget(filepath.Join(root, ".git"), root)
	if !ok {
		return false
	}
	worktreesDir := filepath.Join(commonDir, "worktrees")
	if !isRegularDirNoSymlink(gitDir) || !isRegularDirNoSymlink(worktreesDir) || !SameAutoInitPath(filepath.Dir(gitDir), worktreesDir) {
		return false
	}
	namedRawBytes, ok := ReadSmallRegularFile(filepath.Join(gitDir, "gitdir"), 4096)
	if !ok {
		return false
	}
	namedRaw := strings.TrimSpace(string(namedRawBytes))
	if namedRaw == "" || strings.ContainsAny(namedRaw, "\r\n") {
		return false
	}
	named := resolveCleanPath(gitDir, namedRaw)
	if !SameAutoInitPath(filepath.Dir(named), root) {
		return false
	}
	headInfo, err := os.Lstat(filepath.Join(commonDir, "HEAD"))
	return err == nil && headInfo.Mode()&os.ModeSymlink == 0 && headInfo.Mode().IsRegular()
}

func writePatternsToCommonExclude(commonDir string, patterns []string) error {
	commonRoot, err := os.OpenRoot(commonDir)
	if err != nil {
		return err
	}
	defer commonRoot.Close()
	if err := commonRoot.Mkdir("info", 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	relExclude := filepath.Join("info", "exclude")
	if fi, err := commonRoot.Lstat(relExclude); err == nil && (!fi.Mode().IsRegular() || fi.Size() > MaxFileBytes) {
		return fmt.Errorf("search: %s is not a readable regular file", relExclude)
	}
	existing, err := readRootFile(commonRoot, relExclude)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	add := filterNewExcludePatterns(existing, patterns)
	if len(add) == 0 {
		return nil
	}
	f, err := commonRoot.OpenFile(relExclude, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	prefix := ""
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		prefix = "\n"
	}
	_, err = io.WriteString(f, prefix+strings.Join(add, "\n")+"\n")
	return err
}

func filterNewExcludePatterns(existing []byte, patterns []string) []string {
	seen := map[string]bool{}
	for _, line := range strings.Split(string(existing), "\n") {
		seen[strings.TrimSpace(line)] = true
	}
	var add []string
	for _, p := range patterns {
		if p = strings.TrimSpace(p); p != "" && !seen[p] {
			seen[p] = true
			add = append(add, p)
		}
	}
	return add
}

// ScratchIgnoredFast reports whether .agents/scratch/search.db is ignored by git exclude or .gitignore files.
func ScratchIgnoredFast(root, commonDir string) bool {
	ignored := false
	apply := func(path string, fn func(string) bool) {
		if st := IgnoreFileScratchState(path, fn); st != 0 {
			ignored = st > 0
		}
	}
	apply(filepath.Join(commonDir, "info", "exclude"), PatternCoversRootScratch)
	apply(filepath.Join(root, ".gitignore"), PatternCoversRootScratch)
	if isRegularDirNoSymlink(filepath.Join(root, ".agents")) {
		apply(filepath.Join(root, ".agents", ".gitignore"), patternCoversAgentsScratch)
		if isRegularDirNoSymlink(filepath.Join(root, ".agents", "scratch")) {
			apply(filepath.Join(root, ".agents", "scratch", ".gitignore"), patternCoversLocalScratch)
		}
	}
	return ignored
}

// IgnoreFileScratchState scans one ignore file and returns +1 (ignored), -1 (negated), or 0 (unmentioned).
func IgnoreFileScratchState(path string, coverFn func(string) bool) int {
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() || fi.Size() > MaxFileBytes {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	state := 0
	for _, line := range strings.Split(string(data), "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		if strings.HasPrefix(s, "!") {
			if coverFn(strings.TrimSpace(s[1:])) {
				state = -1
			}
			continue
		}
		if coverFn(s) {
			state = 1
		}
	}
	return state
}

// PatternCoversRootScratch reports whether a root .gitignore pattern ignores .agents/scratch/search.db.
func PatternCoversRootScratch(pat string) bool {
	p := strings.TrimPrefix(strings.TrimSpace(pat), "/")
	switch p {
	case ".agents", ".agents/", ".agents/*", ".agents/**", ".agents/**/*",
		".agents/scratch", ".agents/scratch/", ".agents/scratch/*", ".agents/scratch/**", ".agents/scratch/**/*",
		".agents/scratch/search.db", ".agents/scratch/*.db",
		"**/.agents", "**/.agents/", "**/.agents/*", "**/.agents/**",
		"**/.agents/scratch", "**/.agents/scratch/", "**/.agents/scratch/*", "**/.agents/scratch/**",
		"*", "**", "*.db":
		return true
	}
	return false
}

func patternCoversAgentsScratch(pat string) bool {
	p := strings.TrimPrefix(strings.TrimSpace(pat), "/")
	switch p {
	case "scratch", "scratch/", "scratch/*", "scratch/**", "scratch/**/*",
		"scratch/search.db", "scratch/*.db",
		"**/scratch", "**/scratch/", "**/scratch/*", "**/scratch/**",
		"*", "**":
		return true
	}
	return false
}

func patternCoversLocalScratch(pat string) bool {
	p := strings.TrimPrefix(strings.TrimSpace(pat), "/")
	switch p {
	case "*", "**", "search.db", "*.db", "search.*":
		return true
	}
	return false
}

// EnsureExistingCheckoutIgnored ensures .agents/scratch/* is ignored for an already-existing git checkout.
func EnsureExistingCheckoutIgnored(dir string) bool {
	absDir, err := filepath.Abs(strings.TrimSpace(dir))
	if err != nil || IsRefusedAutoInitDir(absDir) {
		return false
	}
	root, _, ok := FindGitRootFast(absDir)
	if !ok || IsRefusedAutoInitDir(root) {
		return false
	}
	if trail.CheckScratchNoSymlinks(root) != nil {
		return false
	}
	return EnsureHookScratchIgnored(root, nil) == nil
}
