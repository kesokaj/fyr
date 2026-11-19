package search

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

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

func ensureHookScratchIgnored(root string, nested []string) error {
	_, commonDir, ok := findGitRootFast(root)
	if !ok {
		return errNotCheckout
	}
	if len(nested) == 0 && scratchIgnoredFast(root, commonDir) {
		return nil
	}
	patterns := append([]string{".agents/scratch/*"}, nested...)
	return writePatternsToValidatedExclude(root, commonDir, patterns)
}

func writePatternsToValidatedExclude(root, commonDir string, patterns []string) error {
	if commonDir == "" || !filepath.IsAbs(commonDir) {
		return fmt.Errorf("search: invalid git common dir %q", commonDir)
	}
	if fi, err := os.Lstat(commonDir); err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("search: git common dir %s is not a regular directory", commonDir)
	}
	if !validateCommonDirForRoot(root, commonDir) {
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

func validateCommonDirForRoot(root, commonDir string) bool {
	if root == "" || commonDir == "" {
		return false
	}
	if sameAutoInitPath(commonDir, filepath.Join(root, ".git")) {
		return true
	}
	gitDir, ok := readDotGitFileTarget(filepath.Join(root, ".git"), root)
	if !ok {
		return false
	}
	worktreesDir := filepath.Join(commonDir, "worktrees")
	if !isRegularDirNoSymlink(gitDir) || !isRegularDirNoSymlink(worktreesDir) || !sameAutoInitPath(filepath.Dir(gitDir), worktreesDir) {
		return false
	}
	namedRawBytes, ok := readSmallRegularFile(filepath.Join(gitDir, "gitdir"), 4096)
	if !ok {
		return false
	}
	namedRaw := strings.TrimSpace(string(namedRawBytes))
	if namedRaw == "" || strings.ContainsAny(namedRaw, "\r\n") {
		return false
	}
	named := resolveCleanPath(gitDir, namedRaw)
	if !sameAutoInitPath(filepath.Dir(named), root) {
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
	if fi, err := commonRoot.Lstat(relExclude); err == nil && (!fi.Mode().IsRegular() || fi.Size() > maxFileBytes) {
		return fmt.Errorf("search: %s is not a readable regular file", relExclude)
	}
	existing, err := readAll(commonRoot, relExclude)
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

func scratchIgnoredFast(root, commonDir string) bool {
	ignored := false
	apply := func(path string, fn func(string) bool) {
		if st := ignoreFileScratchState(path, fn); st != 0 {
			ignored = st > 0
		}
	}
	apply(filepath.Join(commonDir, "info", "exclude"), patternCoversRootScratch)
	apply(filepath.Join(root, ".gitignore"), patternCoversRootScratch)
	if isRegularDirNoSymlink(filepath.Join(root, ".agents")) {
		apply(filepath.Join(root, ".agents", ".gitignore"), patternCoversAgentsScratch)
		if isRegularDirNoSymlink(filepath.Join(root, ".agents", "scratch")) {
			apply(filepath.Join(root, ".agents", "scratch", ".gitignore"), patternCoversLocalScratch)
		}
	}
	return ignored
}

func ignoreFileCoversScratch(path string) bool {
	return ignoreFileScratchState(path, patternCoversRootScratch) == 1
}

func ignoreFileScratchState(path string, coverFn func(string) bool) int {
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() || fi.Size() > maxFileBytes {
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

func patternCoversRootScratch(pat string) bool {
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

func ensureExistingCheckoutIgnored(dir string) bool {
	absDir, err := filepath.Abs(strings.TrimSpace(dir))
	if err != nil || isRefusedAutoInitDir(absDir) {
		return false
	}
	root, _, ok := findGitRootFast(absDir)
	if !ok || isRefusedAutoInitDir(root) {
		return false
	}
	if checkScratchNoSymlinks(root) != nil {
		return false
	}
	return ensureHookScratchIgnored(root, nil) == nil
}
