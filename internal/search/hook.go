package search

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	hookDefaultBudget    = 1200
	hookMaxDirtyFastPath = 15
	hookKneeTokens       = 180000
	hookMinIdentLen      = 6
	hookConfidenceRatio  = 3.0
)

// HookInput is the JSON payload Antigravity sends to a lifecycle hook on stdin.
type HookInput struct {
	ConversationID        string       `json:"conversationId"`
	WorkspacePaths        []string     `json:"workspacePaths"`
	Cwd                   string       `json:"cwd,omitempty"`
	TranscriptPath        string       `json:"transcriptPath"`
	ArtifactDirectoryPath string       `json:"artifactDirectoryPath"`
	ModelName             string       `json:"modelName"`
	LastUserInput         string       `json:"lastUserInput"`
	InvocationNum         int          `json:"invocationNum"`
	InitialNumSteps       int          `json:"initialNumSteps"`
	StepIdx               int          `json:"stepIdx"`
	ToolCall              HookToolCall `json:"toolCall"`
	ExecutionNum          int          `json:"executionNum"`
	TerminationReason     string       `json:"terminationReason"`
	Error                 string       `json:"error"`
	FullyIdle             bool         `json:"fullyIdle"`
}

// HookToolCall is the tool name and argument map passed to a PreToolUse hook.
type HookToolCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// PreInvocationOutput is the JSON response written by a PreInvocation hook.
type PreInvocationOutput struct {
	InjectSteps         []HookInjectStep `json:"injectSteps,omitempty"`
	TerminationBehavior string           `json:"terminationBehavior"`
}

// HookInjectStep wraps one injected systemMessage step in PreInvocationOutput.
type HookInjectStep struct {
	SystemMessage *HookSystemMessage `json:"systemMessage,omitempty"`
}

// HookSystemMessage holds the model-visible systemMessage and zero-token UI metadata.
type HookSystemMessage struct {
	SystemMessage string         `json:"systemMessage"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

// HookDecisionOutput is the JSON response written by PreToolUse and Stop hooks.
type HookDecisionOutput struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}

func normalizeHookEvent(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "pre-invocation", "preinvocation":
		return "pre-invocation"
	case "pre-tool-use", "pretooluse":
		return "pre-tool-use"
	case "stop":
		return "stop"
	default:
		return ""
	}
}

// RunHook executes the Antigravity lifecycle hook named in o.Hook, reading JSON
// from in and writing the hook response JSON to out. It always fails open (returns nil).
func RunHook(o Options, in io.Reader, out io.Writer) (err error) {
	event := normalizeHookEvent(o.Hook)
	if event == "" {
		return usageError{"search: --hook must be one of: pre-invocation pre-tool-use stop; see fyr help search"}
	}
	defer func() {
		if r := recover(); r != nil {
			if event == "pre-invocation" {
				_ = writeStoreDegraded(out, fmt.Errorf("panic: %v", r))
			} else {
				_ = writeDecision(out, "allow", "")
			}
			err = nil
		}
	}()
	if in == nil {
		in = os.Stdin
	}
	input, decErr := decodeHookInput(o, in)
	dir := resolveHookDir(o, input)
	if decErr != nil || hooksDisabled(dir) {
		if event == "pre-invocation" {
			return writePreInvocation(out, "", "")
		}
		return writeDecision(out, "allow", "")
	}
	switch event {
	case "pre-invocation":
		return handlePreInvocation(o, dir, input, out)
	case "pre-tool-use":
		return handlePreToolUse(dir, input, out)
	default:
		return handleStopHook(o, dir, input, out)
	}
}

func decodeHookInput(o Options, in io.Reader) (HookInput, error) {
	data, err := io.ReadAll(io.LimitReader(in, 4<<20))
	if err != nil {
		return HookInput{}, err
	}
	return parseAndNormalizeHookInput(o, data)
}

func resolveHookDir(o Options, in HookInput) string {
	firstWP := ""
	for _, wp := range in.WorkspacePaths {
		p := strings.TrimSpace(wp)
		if p == "" {
			continue
		}
		if firstWP == "" {
			firstWP = p
		}
		if _, err := checkoutRoot(p); err == nil {
			return p
		}
	}
	if firstWP != "" {
		return firstWP
	}
	if cwd := strings.TrimSpace(in.Cwd); cwd != "" {
		return cwd
	}
	if o.Dir != "" {
		return o.Dir
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

func handlePreInvocation(o Options, dir string, in HookInput, out io.Writer) error {
	rawPrompt := StripSandboxPreamble(strings.TrimSpace(in.LastUserInput))
	cleanPrompt := strings.TrimSpace(strings.TrimPrefix(rawPrompt, "!"))
	var (
		state             SessionState
		skip, fastPath    bool
		sc                *scope
		dirtySummary      string
		slice             SliceResult
		sections, banners []string
	)
	err := ensureHookProjectReady(dir)
	if err == nil {
		state, skip, err = checkTurnOrMidTurn(dir, in.ConversationID, in.TranscriptPath, in.InvocationNum, rawPrompt)
	}
	if err == nil && skip {
		return writePreInvocation(out, "", "")
	}
	if err == nil {
		sc, dirtySummary, fastPath, err = prepareHookScope(dir)
	}
	if err != nil {
		if in.InvocationNum > 1 {
			return writePreInvocation(out, "", "")
		}
		banners = append(banners, storeDegradedBanner(err))
	} else {
		defer sc.close()
		sections, banners, slice = buildHealthyHookTurn(o, sc, dir, in.ConversationID, cleanPrompt, state, dirtySummary, fastPath)
	}
	sysMsg, banner := buildHookEnvelope(o, sc, dir, in.ConversationID, in, state.TurnCount, rawPrompt, cleanPrompt, slice, sections, banners)
	return writePreInvocation(out, sysMsg, banner)
}

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

func prepareHookScope(dir string) (*scope, string, bool, error) {
	if err := ensureHookProjectReady(dir); err != nil {
		return nil, "", false, err
	}
	root, err := checkoutRoot(dir)
	if err != nil {
		return nil, "", false, errNotCheckout
	}
	if err := guardProject(root); err != nil {
		return nil, "", false, err
	}
	st, err := openStore(projectDB(root), io.Discard)
	if err != nil {
		return nil, "", false, err
	}
	view, leftOut, err := gitView(root)
	if err != nil {
		st.close()
		return nil, "", false, err
	}
	dirtyPaths := collectDirtyPaths(view)
	var existingFiles int
	_ = st.db.QueryRow(`SELECT count(*) FROM files WHERE project = ?`, root).Scan(&existingFiles)
	diffN, _ := st.diffCount(source{project: root, dir: root, view: view})
	if existingFiles > 0 && diffN > hookMaxDirtyFastPath {
		summary := formatDirtyFastPath(dirtyPaths, diffN)
		return &scope{st: st, projects: []string{root}, leftOut: leftOut}, summary, true, nil
	}
	if _, err := st.apply(source{project: root, dir: root, view: view}); err != nil {
		st.close()
		return nil, "", false, err
	}
	return &scope{st: st, projects: []string{root}, leftOut: leftOut}, "", false, nil
}

func collectDirtyPaths(view []entry) []string {
	var out []string
	for _, e := range view {
		if e.state == "modified" {
			out = append(out, "M "+e.path)
		} else if e.state == "untracked" {
			out = append(out, "?? "+e.path)
		}
	}
	return out
}

func formatDirtyFastPath(dirtyPaths []string, diffN int) string {
	var b strings.Builder
	count := max(len(dirtyPaths), diffN)
	fmt.Fprintf(&b, "# fast-path: %d modified/untracked files (> %d threshold; serving cached AST snapshot)\n", count, hookMaxDirtyFastPath)
	limit := min(20, len(dirtyPaths))
	for i := 0; i < limit; i++ {
		b.WriteString("  " + dirtyPaths[i] + "\n")
	}
	if len(dirtyPaths) > limit {
		fmt.Fprintf(&b, "  ... and %d more\n", len(dirtyPaths)-limit)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (sc *scope) runVet(o Options, out io.Writer) error {
	prompt := StripSandboxPreamble(strings.TrimSpace(o.Vet))
	cleanPrompt := strings.TrimSpace(strings.TrimPrefix(prompt, "!"))
	slice := sc.buildVetSlice(o, cleanPrompt, SessionState{}, false)
	var prev *VetHistoryRow
	var recentSkills map[string]bool
	var recentPivots []string
	if sc != nil && sc.st != nil && o.Session != "" {
		prev, _ = sc.st.lastVetHistory(o.Session, 0)
		recentSkills, recentPivots, _ = sc.st.recentSuggestedSkills(o.Session, 0, 3)
	}
	intent := ClassifyIntent(cleanPrompt, slice.SymbolHits)
	var brainCtx TurnBrainContext
	if o.BrainTurnContext != nil {
		brainCtx = o.BrainTurnContext(o.Dir, o.Home, cleanPrompt, slice.Files)
	}
	var catalog []SkillMeta
	if o.SkillCatalog != nil {
		catalog = o.SkillCatalog(o.Home)
	}
	res := vetPromptCoreWithHistory(prompt, intent, slice, brainCtx, catalog, prev, recentSkills, recentPivots)
	sysMsg, banner := BuildTurnEnvelope(&res, slice, brainCtx, nil, nil)
	res.SystemMessage = sysMsg
	res.UserMarkdownBanner = banner
	if sc != nil && sc.st != nil && o.Session != "" && prompt != "" {
		nextTurn := 1
		if prev != nil {
			nextTurn = prev.TurnNum + 1
		}
		pivotFiles := extractPromptAndSliceFiles(cleanPrompt, slice.Files)
		_ = sc.st.recordVetHistory(o.Session, nextTurn, prompt, res, pivotFiles)
	}
	_, err := io.WriteString(out, FormatVetResult(res))
	return err
}

func (sc *scope) buildVetSlice(o Options, prompt string, state SessionState, preseedLedger bool) SliceResult {
	if sc == nil || sc.st == nil || prompt == "" {
		return SliceResult{}
	}
	g, err := sc.loadSymGraph()
	if err != nil || len(g.syms) == 0 {
		return SliceResult{}
	}
	hits, _ := countMatchingSymbols(g, prompt)
	active := sc.sessionActiveFiles(o.Session, o.ActiveFiles)
	ranked, confident := rankAndCheckConfidence(g, prompt, active)
	if !confident && !state.Compacted {
		return SliceResult{SymbolHits: hits, Files: nil}
	}
	pivotFileSet := map[string]bool{}
	capsule, pivots := sc.renderHookOrVetCapsule(o, g, prompt, ranked, pivotFileSet)
	if capsule != "" && preseedLedger {
		_ = RegisterCapsulePivots(o.Dir, o.Session, state.TurnCount, pivots)
	}
	return SliceResult{
		Capsule:    capsule,
		Pivots:     len(pivots),
		SymbolHits: hits,
		Files:      sortedMapKeys(pivotFileSet),
		Confident:  confident,
	}
}

func countMatchingSymbols(g *symGraph, prompt string) (int, map[string]bool) {
	terms := parseQueryTerms(prompt)
	hits := 0
	fileSet := map[string]bool{}
	for _, s := range g.syms {
		if matchesPromptTerms(s, terms) {
			hits++
			fileSet[s.Path] = true
		}
	}
	return hits, fileSet
}

func (sc *scope) renderHookOrVetCapsule(o Options, g *symGraph, prompt string, ranked []RankedSymbol, fileSet map[string]bool) (string, []CapsulePivot) {
	sliceOpts := o
	sliceOpts.Slice = prompt
	if sliceOpts.Budget <= 0 || sliceOpts.Budget == defaultSliceBudget {
		sliceOpts.Budget = hookDefaultBudget
	}
	if sliceOpts.Hops <= 0 {
		sliceOpts.Hops = defaultSliceHops
	}
	if o.BrainRefsForDir != nil && o.Dir != "" {
		sliceOpts.BrainRefs = func(target string) []LinkedRecord { return o.BrainRefsForDir(o.Dir, target) }
	}
	tiers := g.selectSliceTiers(prompt, ranked, sliceOpts.Hops)
	capsule := strings.TrimSpace(sc.renderSliceCapsule(sliceOpts, g, tiers, max(200, sliceOpts.Budget*4)))
	if capsule == "" {
		return "", nil
	}
	pivots := ParseCapsulePivots(capsule)
	for _, p := range pivots {
		if p.Path != "" {
			fileSet[p.Path] = true
		}
	}
	return capsule, pivots
}

func rankAndCheckConfidence(g *symGraph, prompt string, active map[string]bool) ([]RankedSymbol, bool) {
	if prompt == "" || len(g.syms) == 0 {
		return nil, false
	}
	var longTerms []string
	for _, t := range parseQueryTerms(prompt) {
		if len(t) >= hookMinIdentLen {
			longTerms = append(longTerms, t)
		}
	}
	if len(longTerms) == 0 {
		return nil, false
	}
	matched := map[int]bool{}
	for i, s := range g.syms {
		if matchesPromptTerms(s, longTerms) {
			matched[i] = true
		}
	}
	if len(matched) == 0 {
		return nil, false
	}
	ranked := g.rank(prompt, active)
	if len(ranked) == 0 {
		return nil, false
	}
	median := medianRankScore(g, ranked, matched)
	return ranked, median > 0 && ranked[0].Score >= hookConfidenceRatio*median
}

func medianRankScore(g *symGraph, ranked []RankedSymbol, matched map[int]bool) float64 {
	if len(ranked) >= 4 {
		m := ranked[len(ranked)/2].Score
		if ranked[0].Score >= hookConfidenceRatio*m {
			return m
		}
	}
	neighbors := map[int]bool{}
	for idx := range matched {
		neighbors[idx] = true
		for _, e := range g.out[idx] {
			neighbors[e.toIdx] = true
		}
		for _, e := range g.in[idx] {
			if e.fromIdx >= 0 {
				neighbors[e.fromIdx] = true
			}
		}
	}
	var bg []float64
	for _, s := range ranked {
		if !neighbors[s.Idx] {
			bg = append(bg, s.Score)
		}
	}
	if len(bg) > 0 {
		return bg[len(bg)/2]
	}
	return ranked[0].Score / (hookConfidenceRatio + 1.0)
}

func handlePreToolUse(dir string, in HookInput, out io.Writer) error {
	switch normalizeHookToolName(in.ToolCall.Name) {
	case "read_file":
		return handlePreToolRead(dir, in, out)
	case "run_command":
		return handlePreToolBash(dir, in, out)
	case "write_or_edit":
		return handlePreToolWrite(dir, in, out)
	case "invoke_subagent", "invokesubagent":
		return handlePreToolSubagent(in, out)
	default:
		return writeDecision(out, "allow", "")
	}
}

func handlePreToolRead(dir string, in HookInput, out io.Writer) error {
	args := in.ToolCall.Args
	target := extractToolTargetPath(args)
	toolDir := resolveToolProjectDir(dir, in, target)
	if target == "" || (toolDir != dir && hooksDisabled(toolDir)) {
		return writeDecision(out, "allow", "")
	}
	start, end, record := clampReadRange(toolDir, target, args)
	if !record {
		return writeDecision(out, "allow", "")
	}
	dec, err := checkLedgerWithIgnoreRetry(toolDir, func() (ReadDecision, error) {
		return CheckAndRecordRead(toolDir, in.ConversationID, target, start, end, 0)
	})
	if err == nil && !dec.Allowed {
		return writeDecision(out, "deny", dec.Reason)
	}
	return writeDecision(out, "allow", "")
}

func handlePreToolBash(dir string, in HookInput, out io.Writer) error {
	cmd := extractStringArg(in.ToolCall.Args, "CommandLine", "commandLine", "command_line", "Command", "command", "cmd")
	bfr, ok := ParseBashFileRead(cmd)
	toolDir := resolveToolProjectDir(dir, in, bfr.Path)
	if !ok || (toolDir != dir && hooksDisabled(toolDir)) {
		return writeDecision(out, "allow", "")
	}
	dec, err := checkLedgerWithIgnoreRetry(toolDir, func() (ReadDecision, error) {
		return CheckAndRecordRead(toolDir, in.ConversationID, bfr.Path, bfr.StartLine, bfr.EndLine, 0)
	})
	if err == nil && !dec.Allowed {
		return writeDecision(out, "deny", dec.Reason)
	}
	return writeDecision(out, "allow", "")
}

func handlePreToolWrite(dir string, in HookInput, out io.Writer) error {
	target := extractToolTargetPath(in.ToolCall.Args)
	toolDir := resolveToolProjectDir(dir, in, target)
	if target == "" || (toolDir != dir && hooksDisabled(toolDir)) {
		return writeDecision(out, "allow", "")
	}
	dec, err := checkLedgerWithIgnoreRetry(toolDir, func() (ReadDecision, error) {
		return CheckPreWriteHash(toolDir, in.ConversationID, target)
	})
	if err == nil && !dec.Allowed {
		return writeDecision(out, "deny", dec.Reason)
	}
	if err == nil {
		if mErr := markPreWriteInLedger(toolDir, in.ConversationID, dec.ResolvedPath); mErr != nil && !errors.Is(mErr, errNotCheckout) {
			return writeDecision(out, "allow", "fyr brain: pre-write ledger unavailable ("+oneLineErr(mErr)+")")
		}
	}
	return writeDecision(out, "allow", "")
}

func checkLedgerWithIgnoreRetry(dir string, fn func() (ReadDecision, error)) (ReadDecision, error) {
	dec, err := fn()
	if errors.Is(err, errNotIgnored) && ensureExistingCheckoutIgnored(dir) {
		return fn()
	}
	return dec, err
}

func handleStopHook(o Options, dir string, in HookInput, out io.Writer) error {
	writeHookProcessArtifact(o.Home, nil, dir, in, "5/6 Verify & Judge", nil)
	if err := RecordStopCoActivity(dir, in.ConversationID); err != nil && !errors.Is(err, errNotCheckout) {
		return writeDecision(out, "allow", "fyr brain: co-activity store unavailable ("+oneLineErr(err)+")")
	}
	return writeDecision(out, "allow", "")
}

func storeDegradedBanner(err error) string {
	return fmt.Sprintf("⚠️ **fyr brain:** store unavailable (%s) — capsule/memory skipped; run /fyr-index-project or fyr index", oneLineErr(err))
}

func writeStoreDegraded(out io.Writer, err error) error {
	return writePreInvocation(out, "", storeDegradedBanner(err))
}

// writePreInvocation serializes the PreInvocation hook response. Antigravity CLI
// v1.5.3's utils.scrubMessage panics on any populated map<string,string> field
// (HookSystemMessage.metadata), so userMarkdown is prepended to SystemMessage and
// Metadata is left nil (BUG-523).
func writePreInvocation(out io.Writer, sysMsg, userMarkdown string) error {
	var resp PreInvocationOutput
	if sysMsg != "" || userMarkdown != "" {
		combined := sysMsg
		if userMarkdown != "" && combined != "" {
			combined = userMarkdown + "\n" + combined
		} else if userMarkdown != "" {
			combined = userMarkdown
		}
		resp.InjectSteps = []HookInjectStep{{SystemMessage: &HookSystemMessage{SystemMessage: combined}}}
	}
	if data, err := json.Marshal(resp); err == nil {
		_, _ = out.Write(append(data, '\n'))
	}
	return nil
}

func writeDecision(out io.Writer, decision, reason string) error {
	if data, err := json.Marshal(HookDecisionOutput{Decision: decision, Reason: reason}); err == nil {
		_, _ = out.Write(append(data, '\n'))
	}
	return nil
}

func oneLineErr(err error) string {
	if err == nil {
		return "unknown error"
	}
	s := strings.Join(strings.Fields(err.Error()), " ")
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}
