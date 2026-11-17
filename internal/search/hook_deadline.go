package search

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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
		uh, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		h = uh
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

func isRegularFileFast(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode().IsRegular()
}

func isCoordinatorOnlyMode(home, root string) bool {
	if root != "" && checkScratchNoSymlinks(root) == nil {
		if isRegularFileFast(filepath.Join(root, ".agents", "scratch", "coordinator-only")) {
			return true
		}
	}
	if h := strings.TrimSpace(home); h != "" {
		stateDir := filepath.Join(h, harness.StateDir)
		if fi, err := os.Lstat(stateDir); err == nil && fi.Mode()&os.ModeSymlink == 0 && fi.IsDir() {
			if isRegularFileFast(filepath.Join(stateDir, "coordinator-only")) {
				return true
			}
		}
	}
	return false
}

func resolveExplicitHookDirs(o Options, in HookInput) (string, string) {
	firstDir := ""
	cands := append(append([]string{}, in.WorkspacePaths...), in.Cwd, o.Dir)
	for _, raw := range cands {
		p := strings.TrimSpace(raw)
		if p == "" {
			continue
		}
		if firstDir == "" {
			firstDir = p
		}
		if root, _, ok := findGitRootFast(p); ok {
			return root, root
		}
	}
	return "", firstDir
}

func parseWorkerFilesHeader(prompt string) ([]string, bool) {
	var files []string
	seen := map[string]bool{}
	hasHeader, inBullets := false, false
	addTok := func(tok string) {
		clean := normalizeSlashPath(strings.Trim(tok, "`'\",;()[]"))
		if low := strings.ToLower(clean); clean != "" && low != "none" && low != "<none>" && low != "(none)" && !seen[clean] {
			seen[clean] = true
			files = append(files, clean)
		}
	}
	for _, raw := range strings.Split(strings.ReplaceAll(prompt, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(strings.ToLower(line), "files:") {
			hasHeader = true
			rest := strings.TrimSpace(line[len("files:"):])
			inBullets = rest == ""
			for _, part := range strings.FieldsFunc(rest, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
				addTok(part)
			}
			continue
		}
		if inBullets && (strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ")) {
			if fields := strings.Fields(line[2:]); len(fields) > 0 {
				addTok(fields[0])
			}
		} else if line != "" {
			inBullets = false
		}
	}
	return files, hasHeader
}

func checkCoordinatorWriteGuard(o Options, dir, toolDir string, in HookInput, target string) string {
	root, commonDir, isGit := resolveWriteGuardGitRoot(toolDir, dir, target)
	if isCoordinatorWriteExemptPath(root, toolDir, target) {
		return ""
	}
	if isGit && filepath.Clean(commonDir) != filepath.Clean(filepath.Join(root, ".git")) {
		return ""
	}
	if hasBangOrWorkerSignal(in.LastUserInput) {
		return ""
	}
	lastVet := readLatestVetHistoryFast(root, in.ConversationID)
	if lastVet != nil && hasBangOrWorkerSignal(lastVet.RawPrompt) {
		return ""
	}
	if lastVet != nil && (lastVet.Directive == "COORDINATE_SUBTASKS" || lastVet.SubtaskCount >= 2) {
		return fmt.Sprintf("[COORDINATOR_GUARD] active COORDINATE_SUBTASKS turn (%d subtasks) blocks direct Coordinator source edits; dispatch an isolated worker (invoke_subagent / swarm with Files: and workspace=\"share\") or prefix prompt with `!` to bypass.", max(2, lastVet.SubtaskCount))
	}
	if isCoordinatorOnlyMode(o.Home, root) {
		return "[COORDINATOR_GUARD] coordinator-only mode blocks direct Coordinator source edits; dispatch an isolated worker (invoke_subagent / swarm with Files: and workspace=\"share\") or prefix prompt with `!` to bypass."
	}
	return ""
}

func resolveWriteGuardGitRoot(toolDir, dir, target string) (string, string, bool) {
	var cands []string
	if strings.TrimSpace(target) != "" {
		if resolved := resolveTargetWithAncestors(toolDir, target); resolved != "" {
			if anc := existingAncestorDir(filepath.FromSlash(resolved)); anc != "" {
				cands = append(cands, anc)
			}
		}
	}
	cands = append(cands, toolDir, dir)
	for _, d := range cands {
		if strings.TrimSpace(d) == "" {
			continue
		}
		if root, commonDir, ok := findGitRootFast(d); ok {
			return root, commonDir, true
		}
	}
	return "", "", false
}

func existingAncestorDir(path string) string {
	for cur := filepath.Clean(path); ; {
		if fi, err := os.Stat(cur); err == nil && fi.IsDir() {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

func hasBangOrWorkerSignal(raw string) bool {
	trimmed := strings.TrimSpace(StripSandboxPreamble(strings.TrimSpace(raw)))
	return trimmed != "" && (strings.HasPrefix(trimmed, "!") || isWorkerBriefPrompt(trimmed))
}

func readLatestVetHistoryFast(root, convID string) *VetHistoryRow {
	if root == "" || checkScratchNoSymlinks(root) != nil || !isRegularFileFast(projectDB(root)) {
		return nil
	}
	st, _, err := openLedgerStore(root)
	if err != nil || st == nil {
		return nil
	}
	defer st.close()
	row, err := st.lastVetHistory(convID, 0)
	if err != nil {
		return nil
	}
	return row
}

func lookupWorkerCapsuleContext(o Options, explicitRoot, explicitDir, rawPrompt string, headerFiles []string) ([]string, []string, []MatchedRecord, *SkillMeta) {
	promptFiles := extractPromptAndSliceFiles(rawPrompt, headerFiles)
	astLines, astFiles := lookupWorkerHop0AST(explicitRoot, rawPrompt, promptFiles)
	allFiles := extractPromptAndSliceFiles(rawPrompt, append(append([]string{}, promptFiles...), astFiles...))
	recs := lookupWorkerRecords(o, explicitDir, rawPrompt, allFiles)
	skill := lookupWorkerTopSkill(o, rawPrompt, allFiles, len(astLines))
	return astLines, allFiles, recs, skill
}

func lookupWorkerHop0AST(explicitRoot, prompt string, files []string) ([]string, []string) {
	if explicitRoot == "" || checkScratchNoSymlinks(explicitRoot) != nil || !isRegularFileFast(projectDB(explicitRoot)) {
		return nil, nil
	}
	st, _, err := openLedgerStore(explicitRoot)
	if err != nil || st == nil {
		return nil, nil
	}
	defer st.close()
	sc := &scope{st: st, projects: []string{explicitRoot}}
	g, err := sc.loadSymGraph()
	if err != nil || len(g.syms) == 0 {
		return nil, nil
	}
	hop0Idxs := selectWorkerHop0Idxs(sc, g, prompt, matchGraphFiles(g, files))
	var lines, astFiles []string
	seen := map[string]bool{}
	for _, idx := range hop0Idxs {
		s := g.syms[idx]
		if clean := printable(fmt.Sprintf("[Hop 0] %s:%d-%d (%s) — %s", s.Path, s.StartLine, s.EndLine, s.QualName(), compactSig(s.Sig, 90))); clean != "" {
			lines = append(lines, clean)
		}
		if s.Path != "" && !seen[s.Path] {
			seen[s.Path] = true
			astFiles = append(astFiles, s.Path)
		}
	}
	return lines, astFiles
}

func selectWorkerHop0Idxs(sc *scope, g *symGraph, prompt string, active map[string]bool) []int {
	ranked, confident := rankAndCheckConfidence(g, prompt, active)
	if !confident {
		ranked, confident = sc.ftsFallbackRanked(g, prompt, active)
	}
	if !confident && len(active) == 0 {
		return nil
	}
	if !confident {
		ranked = g.rank(prompt, active)
	}
	low := strings.ToLower(prompt)
	sort.SliceStable(ranked, func(i, j int) bool {
		mi := len(ranked[i].Name) >= 4 && strings.Contains(low, strings.ToLower(ranked[i].Name))
		mj := len(ranked[j].Name) >= 4 && strings.Contains(low, strings.ToLower(ranked[j].Name))
		return mi && !mj
	})
	if confident {
		return g.selectSliceTiers(prompt, ranked, 0).hop0
	}
	var idxs []int
	for _, s := range ranked {
		if active[s.Path] {
			if idxs = append(idxs, s.Idx); len(idxs) >= 5 {
				break
			}
		}
	}
	return idxs
}

func matchGraphFiles(g *symGraph, files []string) map[string]bool {
	active := map[string]bool{}
	if len(files) == 0 {
		return active
	}
	allPaths := make([]string, 0, len(g.byPath))
	for p := range g.byPath {
		allPaths = append(allPaths, p)
	}
	sort.Strings(allPaths)
	for _, raw := range files {
		n := normalizeSlashPath(raw)
		switch {
		case n == "":
		case len(g.byPath[n]) > 0:
			active[n] = true
		case isDirPathToken(n):
			for _, p := range allPaths {
				if strings.HasPrefix(p, n+"/") {
					active[p] = true
				}
			}
		case !strings.Contains(n, "/"):
			if uniq := uniqueGraphBaseMatch(allPaths, n); uniq != "" {
				active[uniq] = true
			}
		}
	}
	return active
}

func uniqueGraphBaseMatch(allPaths []string, base string) string {
	var uniq string
	for _, p := range allPaths {
		if filepath.Base(p) == base {
			if uniq != "" && uniq != p {
				return ""
			}
			uniq = p
		}
	}
	return uniq
}

func lookupWorkerRecords(o Options, explicitDir, prompt string, files []string) []MatchedRecord {
	var out []MatchedRecord
	seen := map[string]bool{}
	add := func(r MatchedRecord) {
		if r.ID == "" || seen[r.ID] || (r.Kind == "memory" && r.Validity > 0 && r.Validity < 0.40) {
			return
		}
		seen[r.ID] = true
		out = append(out, r)
	}
	if explicitDir != "" && o.BrainTurnContext != nil {
		bc := o.BrainTurnContext(explicitDir, o.Home, prompt, files)
		for _, r := range append(append(append([]MatchedRecord{}, bc.Bugs...), bc.ADRs...), bc.Memories...) {
			add(r)
		}
	}
	if len(out) < 3 && explicitDir != "" && o.BrainRefsForDir != nil {
		for _, f := range files {
			for _, lr := range o.BrainRefsForDir(explicitDir, f) {
				kind := "record"
				if strings.HasPrefix(lr.ID, "BUG-") {
					kind = "bug"
				} else if strings.HasPrefix(lr.ID, "ADR-") {
					kind = "adr"
				}
				add(MatchedRecord{ID: lr.ID, Kind: kind, Status: lr.Status, Title: lr.Title})
			}
		}
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

func lookupWorkerTopSkill(o Options, prompt string, files []string, symbolHits int) *SkillMeta {
	var catalog []SkillMeta
	if o.SkillCatalog != nil {
		catalog = o.SkillCatalog(o.Home)
	}
	skills, _ := MatchSkills(prompt, ClassifyIntent(prompt, symbolHits), files, catalog)
	for i := range skills {
		if strings.TrimSpace(skills[i].Digest) != "" || strings.TrimSpace(skills[i].Description) != "" {
			sk := skills[i]
			return &sk
		}
	}
	return nil
}

func capLineUTF8(s string, maxB int) string {
	return truncateBytesUTF8(strings.Join(strings.Fields(printable(s)), " "), maxB)
}

func gatherWorkerFenceTexts(rawPrompt string, inferred []string, recs []MatchedRecord, skill *SkillMeta, astLines []string) []string {
	texts := make([]string, 0, 2+len(inferred)+len(recs)*3+len(astLines))
	texts = append(texts, rawPrompt)
	texts = append(texts, inferred...)
	for _, r := range recs {
		texts = append(texts, r.ID, r.Title, r.Body)
	}
	if skill != nil {
		texts = append(texts, skill.Name, skill.Digest, skill.Description)
	}
	return append(texts, astLines...)
}
