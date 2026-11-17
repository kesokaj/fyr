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
	default:
		return handleStopHook(o, dir, input, out)
	}
}

func writeHookAllowFallback(event string, out io.Writer) error {
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
	data, err := os.ReadFile(dotGitFile)
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(string(data))
	raw, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return "", false
	}
	gd := strings.TrimSpace(raw)
	if gd == "" {
		return "", false
	}
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(repoRoot, gd)
	}
	gd = filepath.Clean(gd)
	if cdBytes, err := os.ReadFile(filepath.Join(gd, "commondir")); err == nil {
		cd := strings.TrimSpace(string(cdBytes))
		if cd != "" {
			if !filepath.IsAbs(cd) {
				cd = filepath.Join(gd, cd)
			}
			return filepath.Clean(cd), true
		}
	}
	return gd, true
}

func scratchIgnoredFast(root, commonDir string) bool {
	for _, p := range []string{
		filepath.Join(commonDir, "info", "exclude"),
		filepath.Join(root, ".gitignore"),
	} {
		if ignoreFileCoversScratch(p) {
			return true
		}
	}
	return false
}

func ignoreFileCoversScratch(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	matched := false
	for _, line := range strings.Split(string(data), "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		if strings.HasPrefix(s, "!") {
			return false
		}
		switch s {
		case ".agents/scratch/*", ".agents/scratch/", ".agents/scratch", ".agents/*", ".agents/", ".agents":
			matched = true
		}
	}
	return matched
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
