package search

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

const (
	hookDefaultBudget    = 1800
	hookMaxDirtyFastPath = 15
	hookKneeTokens       = 180000
	hookMinIdentLen      = 6
	hookConfidenceRatio  = 3.0
)

// HookInput is the JSON payload Antigravity sends to a lifecycle hook on stdin.
type HookInput struct {
	ConversationID        string       `json:"conversationId"`
	WorkspacePaths        []string     `json:"workspacePaths"`
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
	input, decErr := decodeHookInput(in)
	if decErr != nil {
		if event == "pre-invocation" {
			return writePreInvocation(out, "", "")
		}
		return writeDecision(out, "allow", "")
	}
	dir := resolveHookDir(o, input)
	switch event {
	case "pre-invocation":
		return handlePreInvocation(o, dir, input, out)
	case "pre-tool-use":
		return handlePreToolUse(dir, input, out)
	default:
		return handleStopHook(dir, input, out)
	}
}

func decodeHookInput(in io.Reader) (HookInput, error) {
	data, err := io.ReadAll(io.LimitReader(in, 4<<20))
	if err != nil {
		return HookInput{}, err
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return HookInput{}, nil
	}
	var input HookInput
	if err := json.Unmarshal(trimmed, &input); err != nil {
		return HookInput{}, err
	}
	return input, nil
}

func resolveHookDir(o Options, in HookInput) string {
	for _, wp := range in.WorkspacePaths {
		if p := strings.TrimSpace(wp); p != "" {
			return p
		}
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
	state, err := CheckCompaction(dir, in.ConversationID, in.TranscriptPath)
	if err != nil {
		return writeStoreDegraded(out, err)
	}
	sc, dirtySummary, fastPath, err := prepareHookScope(dir)
	if err != nil {
		return writeStoreDegraded(out, err)
	}
	defer sc.close()

	var sections, banners []string
	if state.Compacted {
		if o.BrainReprime != nil {
			if rep := strings.TrimSpace(o.BrainReprime(dir, o.Home)); rep != "" {
				sections = append(sections, rep)
			}
		}
		banners = append(banners, fmt.Sprintf("♻️ compacted (gen %d): re-primed memory & capsule", state.GenerationID))
	}
	if fastPath && dirtySummary != "" {
		sections = append(sections, dirtySummary)
		banners = append(banners, "⚡ >15 dirty files: serving cached AST snapshot")
	}
	rawPrompt := strings.TrimSpace(in.LastUserInput)
	cleanPrompt := strings.TrimSpace(strings.TrimPrefix(rawPrompt, "!"))
	sliceOpts := o
	sliceOpts.Dir = dir
	sliceOpts.Session = in.ConversationID
	slice := sc.buildVetSlice(sliceOpts, cleanPrompt, state, true)
	if slice.Capsule != "" {
		banners = append(banners, fmt.Sprintf("🧠 Turn-0 capsule (%d pivots pre-seeded)", slice.Pivots))
	}
	if state.EstTokens >= hookKneeTokens {
		sections = append(sections, fmt.Sprintf("[CONTEXT_KNEE_WARNING] Session context is ~%dk tokens (>= 180K knee); wrap up current step and run /handoff or /compact.", state.EstTokens/1000))
		banners = append(banners, fmt.Sprintf("⚠️ %dk tokens (>= 180K knee)", state.EstTokens/1000))
	}
	sysMsg, banner := buildHookEnvelope(o, dir, rawPrompt, cleanPrompt, slice, sections, banners)
	return writePreInvocation(out, sysMsg, banner)
}

func buildHookEnvelope(o Options, dir, rawPrompt, cleanPrompt string, slice SliceResult, sections, banners []string) (string, string) {
	if rawPrompt == "" && len(sections) == 0 && slice.Capsule == "" {
		return "", ""
	}
	intent := ClassifyIntent(cleanPrompt, slice.SymbolHits)
	var brainCtx TurnBrainContext
	if o.BrainTurnContext != nil {
		brainCtx = o.BrainTurnContext(dir, o.Home, cleanPrompt, slice.Files)
	}
	var catalog []SkillMeta
	if o.SkillCatalog != nil {
		catalog = o.SkillCatalog(o.Home)
	}
	vetRes := vetPromptCore(rawPrompt, intent, slice, brainCtx, catalog)
	return BuildTurnEnvelope(&vetRes, slice, brainCtx, sections, banners)
}

func prepareHookScope(dir string) (*scope, string, bool, error) {
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
	if len(dirtyPaths) > hookMaxDirtyFastPath || (existingFiles > 0 && diffN > hookMaxDirtyFastPath) {
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
	prompt := strings.TrimSpace(o.Vet)
	cleanPrompt := strings.TrimSpace(strings.TrimPrefix(prompt, "!"))
	slice := sc.buildVetSlice(o, cleanPrompt, SessionState{}, false)
	intent := ClassifyIntent(cleanPrompt, slice.SymbolHits)
	var brainCtx TurnBrainContext
	if o.BrainTurnContext != nil {
		brainCtx = o.BrainTurnContext(o.Dir, o.Home, cleanPrompt, slice.Files)
	}
	var catalog []SkillMeta
	if o.SkillCatalog != nil {
		catalog = o.SkillCatalog(o.Home)
	}
	res := VetPrompt(prompt, intent, slice, brainCtx, catalog)
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
	hits, fileSet := countMatchingSymbols(g, prompt)
	active := sc.sessionActiveFiles(o.Session, o.ActiveFiles)
	ranked, confident := rankAndCheckConfidence(g, prompt, active)
	if !confident && !state.Compacted {
		return SliceResult{SymbolHits: hits, Files: sortedMapKeys(fileSet)}
	}
	capsule, pivots := sc.renderHookOrVetCapsule(o, g, prompt, ranked, fileSet)
	if capsule != "" && preseedLedger {
		_ = RegisterCapsulePivots(o.Dir, o.Session, state.TurnCount, pivots)
	}
	return SliceResult{
		Capsule:    capsule,
		Pivots:     len(pivots),
		SymbolHits: hits,
		Files:      sortedMapKeys(fileSet),
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
	name := strings.TrimSpace(in.ToolCall.Name)
	args := in.ToolCall.Args
	switch name {
	case "read_file", "view_file", "Read":
		target := extractStringArg(args, "Path", "path", "AbsolutePath", "absolute_path", "FilePath", "file_path", "TargetFile", "target_file")
		if target == "" {
			return writeDecision(out, "allow", "")
		}
		start, end := extractLineRangeArgs(args)
		dec, err := CheckAndRecordRead(dir, in.ConversationID, target, start, end, 0)
		if err == nil && !dec.Allowed {
			return writeDecision(out, "deny", dec.Reason)
		}
	case "run_command", "Bash", "bash":
		cmd := extractStringArg(args, "CommandLine", "command_line", "Command", "command", "cmd")
		if cmd == "" {
			return writeDecision(out, "allow", "")
		}
		dec, matched, err := CheckAndRecordBashRead(dir, in.ConversationID, cmd, 0)
		if err == nil && matched && !dec.Allowed {
			return writeDecision(out, "deny", dec.Reason)
		}
	case "edit_file", "replace_file_content", "multi_replace_file_content", "write_file", "write_to_file", "Edit", "Write":
		target := extractStringArg(args, "Path", "path", "TargetFile", "target_file", "AbsolutePath", "absolute_path", "FilePath", "file_path")
		if target == "" {
			return writeDecision(out, "allow", "")
		}
		dec, err := CheckPreWriteHash(dir, in.ConversationID, target)
		if err == nil && !dec.Allowed {
			return writeDecision(out, "deny", dec.Reason)
		}
		if err == nil {
			if mErr := markPreWriteInLedger(dir, in.ConversationID, dec.ResolvedPath); mErr != nil && !errors.Is(mErr, errNotCheckout) {
				return writeDecision(out, "allow", "fyr brain: pre-write ledger unavailable ("+oneLineErr(mErr)+")")
			}
		}
	}
	return writeDecision(out, "allow", "")
}

func handleStopHook(dir string, in HookInput, out io.Writer) error {
	if err := RecordStopCoActivity(dir, in.ConversationID); err != nil && !errors.Is(err, errNotCheckout) {
		return writeDecision(out, "allow", "fyr brain: co-activity store unavailable ("+oneLineErr(err)+")")
	}
	return writeDecision(out, "allow", "")
}

func extractStringArg(args map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := args[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func extractLineRangeArgs(args map[string]any) (int, int) {
	start := extractIntArg(args, "StartLine", "start_line", "StartLineNumber", "start_line_number", "Offset", "offset")
	end := extractIntArg(args, "EndLine", "end_line", "EndLineNumber", "end_line_number")
	limit := extractIntArg(args, "Limit", "limit")
	if end == 0 && limit > 0 {
		if start > 0 {
			end = start + limit - 1
		} else {
			start, end = 1, limit
		}
	}
	return start, end
}

func extractIntArg(args map[string]any, keys ...string) int {
	for _, k := range keys {
		v, ok := args[k]
		if !ok {
			continue
		}
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case int64:
			return int(n)
		case string:
			if parsed, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
				return parsed
			}
		}
	}
	return 0
}

func writeStoreDegraded(out io.Writer, err error) error {
	banner := fmt.Sprintf("⚠️ **fyr brain:** store unavailable (%s) — capsule/memory skipped; run fyr doctor", oneLineErr(err))
	return writePreInvocation(out, "", banner)
}

func writePreInvocation(out io.Writer, sysMsg, userMarkdown string) error {
	var resp PreInvocationOutput
	if sysMsg != "" || userMarkdown != "" {
		msg := &HookSystemMessage{SystemMessage: sysMsg}
		if userMarkdown != "" {
			msg.Metadata = map[string]any{"user_markdown": userMarkdown}
		}
		resp.InjectSteps = []HookInjectStep{{SystemMessage: msg}}
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return nil
	}
	_, _ = out.Write(append(data, '\n'))
	return nil
}

func writeDecision(out io.Writer, decision, reason string) error {
	data, err := json.Marshal(HookDecisionOutput{Decision: decision, Reason: reason})
	if err != nil {
		return nil
	}
	_, _ = out.Write(append(data, '\n'))
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
