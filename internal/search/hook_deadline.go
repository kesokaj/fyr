package search

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
)

type hookRunResult struct {
	data []byte
	err  error
}

func runHookWithDeadline(o Options, event string, in io.Reader, out io.Writer) error {
	if in == nil {
		in = os.Stdin
	}
	deadline := o.hookDeadline
	if deadline <= 0 {
		deadline = defaultHookDeadline
	}
	dispatchFn := o.hookDispatch
	if dispatchFn == nil {
		dispatchFn = dispatchHookEvent
	}
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	ch := make(chan hookRunResult, 1)
	go runHookWorker(ctx, o, event, in, dispatchFn, ch)

	select {
	case res := <-ch:
		if res.err == nil && len(res.data) > 0 {
			_, _ = out.Write(res.data)
			return nil
		}
		return writeHookAllowFallback(event, out)
	case <-ctx.Done():
		return writeHookAllowFallback(event, out)
	}
}

func runHookWorker(ctx context.Context, o Options, event string, in io.Reader, dispatchFn func(context.Context, Options, string, string, HookInput, io.Writer) error, ch chan<- hookRunResult) {
	var buf bytes.Buffer
	defer func() {
		if r := recover(); r != nil {
			select {
			case ch <- hookRunResult{}:
			default:
			}
		}
	}()
	if ctx.Err() != nil {
		return
	}
	input, decErr := decodeHookInput(o, in)
	if ctx.Err() != nil {
		return
	}
	dir := resolveHookDir(o, input)
	if decErr != nil || hooksDisabledForHome(o.Home, dir) {
		_ = writeHookAllowFallback(event, &buf)
		select {
		case ch <- hookRunResult{data: buf.Bytes()}:
		case <-ctx.Done():
		}
		return
	}
	err := dispatchFn(ctx, o, event, dir, input, &buf)
	select {
	case ch <- hookRunResult{data: buf.Bytes(), err: err}:
	case <-ctx.Done():
	}
}

func dispatchHookEvent(ctx context.Context, o Options, event, dir string, input HookInput, out io.Writer) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch event {
	case "session-start":
		return handleSessionStart(o, dir, input, out)
	case "pre-invocation":
		return handlePreInvocation(o, dir, input, out)
	case "pre-tool-use":
		return handlePreToolUse(o, dir, input, out)
	case "post-tool-use":
		return handlePostToolUse(o, dir, input, out)
	default:
		return handleStopHook(o, dir, input, out)
	}
}

func writeHookAllowFallback(event string, out io.Writer) error {
	if event == "post-tool-use" {
		_, _ = out.Write([]byte("{}\n"))
		return nil
	}
	if event == "pre-invocation" || event == "session-start" {
		return writePreInvocation(out, "", "")
	}
	return writeDecision(out, "allow", "")
}

func hooksDisabledGlobal(home string) bool {
	h := strings.TrimSpace(home)
	if h == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(h, harness.StateDir, "no-hooks"))
	return err == nil
}

func hooksDisabledForHome(home, dir string) bool {
	return hooksDisabledGlobal(home) || hooksDisabled(dir)
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
	gd := resolveCleanPath(repoRoot, strings.TrimSpace(raw))
	if !isRegularDirNoSymlink(gd) {
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
	for _, d := range []string{".agents", filepath.Join(".agents", "scratch")} {
		if fi, err := os.Lstat(filepath.Join(root, d)); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return guardProject(root)
		}
	}
	if fi, err := os.Lstat(projectDB(root)); err == nil && fi.Mode().IsRegular() && scratchIgnoredFast(root, commonDir) {
		return nil
	}
	return guardProject(root)
}
