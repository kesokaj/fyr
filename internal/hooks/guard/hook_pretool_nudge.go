package guard

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kesokaj/fyr/internal/readledger"
	"github.com/kesokaj/fyr/internal/trail"
)

// NudgeInput carries the hook tool-call fields evaluated for [SEARCH_NUDGE].
type NudgeInput struct {
	Home           string
	Dir            string
	ConversationID string
	ToolName       string
	ToolArgs       map[string]any
}

// NudgeDeps supplies disable-check, DB, sentinel, and writer callbacks for [SEARCH_NUDGE].
type NudgeDeps struct {
	HooksDisabled     func(home, dir string) bool
	NormalizeToolName func(raw string) string
	OpenLedgerDB      func(root string) (*sql.DB, func(), error)
	MarkSentinelOnce  func(db *sql.DB, convID string, genID, turnNum int, key string) bool
	WriteDecision     func(out io.Writer, decision, reason string) error
}

// HandlePreToolGrepNudgeCtx handles grep_search PreToolUse calls with a once-per-turn [SEARCH_NUDGE].
func HandlePreToolGrepNudgeCtx(ctx context.Context, in NudgeInput, wdDB *sql.DB, wdRoot string, deps NudgeDeps, out io.Writer) error {
	if ctx.Err() != nil || (deps.HooksDisabled != nil && deps.HooksDisabled(in.Home, in.Dir)) {
		return deps.WriteDecision(out, "allow", "")
	}
	args := in.ToolArgs
	pattern := ExtractStringArg(args, "Query", "query", "Pattern", "pattern", "Regex", "regex")
	var paths []string
	if p := ExtractStringArg(args, "SearchPath", "searchPath", "search_path", "Path", "path", "Dir", "dir", "Glob", "glob"); p != "" {
		paths = append(paths, p)
	}
	paths = append(paths, ExtractStringSliceArg(args, "Includes", "includes", "Include", "include")...)
	if inc := ExtractStringArg(args, "Includes", "includes", "Include", "include"); inc != "" {
		paths = append(paths, inc)
	}
	toolLabel := ""
	if deps.NormalizeToolName != nil {
		toolLabel = deps.NormalizeToolName(in.ToolName)
	}
	if toolLabel == "" {
		toolLabel = "grep_search"
	}
	nudge := evaluateCodeSearchNudgeCtx(ctx, in, wdDB, wdRoot, deps, toolLabel, pattern, paths)
	return deps.WriteDecision(out, "allow", nudge)
}

// CheckBashGrepNudgeCtx returns a [SEARCH_NUDGE] when a bash command runs grep/rg for an indexed symbol.
func CheckBashGrepNudgeCtx(ctx context.Context, in NudgeInput, wdDB *sql.DB, wdRoot string, deps NudgeDeps, cmd string) string {
	if ctx.Err() != nil || strings.TrimSpace(cmd) == "" {
		return ""
	}
	if tool, pat, paths, ok := extractBashGrepQuery(cmd); ok {
		return evaluateCodeSearchNudgeCtx(ctx, in, wdDB, wdRoot, deps, tool, pat, paths)
	}
	return ""
}

func evaluateCodeSearchNudgeCtx(ctx context.Context, in NudgeInput, wdDB *sql.DB, wdRoot string, deps NudgeDeps, toolLabel, pattern string, paths []string) string {
	sym := extractCodeSymbolQuery(pattern)
	if sym == "" || ctx.Err() != nil {
		return ""
	}
	root := resolveSearchNudgeRoot(in.Dir, wdRoot, paths)
	if root == "" || (deps.HooksDisabled != nil && deps.HooksDisabled(in.Home, root)) || isNonCodeSearchScope(root, in.Dir, paths) {
		return ""
	}
	db, cleanup := openIndexedNudgeDB(root, wdDB, wdRoot, deps.OpenLedgerDB)
	if db == nil {
		return ""
	}
	if cleanup != nil {
		defer cleanup()
	}
	indexed, matchedSym := storeCheckIndexedSymbolCtx(ctx, db, sym)
	if !indexed || ctx.Err() != nil || (!matchedSym && !isLikelyCodeIdentifier(sym)) {
		return ""
	}
	convID := readledger.NormalizeConvID(in.ConversationID)
	genID, turnNum := readledger.CurrentGeneration(db, convID)
	if deps.MarkSentinelOnce != nil && !deps.MarkSentinelOnce(db, convID, genID, turnNum, fmt.Sprintf("__search_nudge__:t%d", turnNum)) {
		return ""
	}
	if matchedSym {
		return fmt.Sprintf("[SEARCH_NUDGE] %s is indexed; prefer `fyr search --refs %s` or `fyr search --slice %s` over %s for symbol lookups.", sym, sym, sym, toolLabel)
	}
	return fmt.Sprintf("[SEARCH_NUDGE] search.db is indexed; prefer `fyr search --refs %s` or `fyr search --slice %s` over %s for code-symbol lookups.", sym, sym, toolLabel)
}

func resolveSearchNudgeRoot(dir, wdRoot string, paths []string) string {
	if wdRoot != "" && IsRegularFileFast(ProjectDB(wdRoot)) {
		return wdRoot
	}
	if root, _, ok := FindGitRootFast(dir); ok && IsRegularFileFast(ProjectDB(root)) {
		return root
	}
	for _, p := range paths {
		if filepath.IsAbs(p) {
			if root, _, ok := FindGitRootFast(ExistingAncestorDir(p)); ok && IsRegularFileFast(ProjectDB(root)) {
				return root
			}
		}
	}
	return ""
}

func openIndexedNudgeDB(root string, wdDB *sql.DB, wdRoot string, openLedgerDB func(root string) (*sql.DB, func(), error)) (*sql.DB, func()) {
	if wdDB != nil && wdRoot != "" && SameAutoInitPath(root, wdRoot) {
		return wdDB, nil
	}
	if openLedgerDB == nil || !IsRegularFileFast(ProjectDB(root)) || trail.CheckScratchNoSymlinks(root) != nil {
		return nil, nil
	}
	db, cleanup, err := openLedgerDB(root)
	if err != nil || db == nil {
		return nil, nil
	}
	return db, cleanup
}

func storeCheckIndexedSymbolCtx(ctx context.Context, db *sql.DB, sym string) (bool, bool) {
	var hasAny, hit int
	if err := db.QueryRowContext(ctx, `SELECT 1 FROM symbols LIMIT 1`).Scan(&hasAny); err != nil || hasAny != 1 {
		return false, false
	}
	lookup := sym
	if _, after, ok := strings.Cut(sym, "."); ok && after != "" {
		lookup = after
	}
	err := db.QueryRowContext(ctx, `SELECT 1 FROM symbols WHERE name = ? LIMIT 1`, lookup).Scan(&hit)
	return true, err == nil && hit == 1
}

func extractBashGrepQuery(cmd string) (string, string, []string, bool) {
	for _, stmt := range splitBashTopPipelines(cmd) {
		firstStage := firstPipeStageText(stmt)
		toks := TokenizeShellSegment(firstStage)
		cmdTok, rest := UnwrapSegmentCommand(toks)
		if cmdTok == "grep" || cmdTok == "rg" || cmdTok == "egrep" || cmdTok == "fgrep" {
			pat, paths, ok := parseGrepCommandArgs(rest)
			if ok {
				return cmdTok, pat, paths, true
			}
		}
	}
	return "", "", nil, false
}

func splitBashTopPipelines(cmd string) []string {
	var out, heredocs []string
	var cur strings.Builder
	for i, q := 0, byte(0); i < len(cmd); i++ {
		if q != 0 {
			i, q = stepQuotedAtom(cmd, i, q, &cur)
			continue
		}
		var flush bool
		i, q, heredocs, flush = stepShellSplitCharWith(cmd, i, heredocs, &cur, matchPipelineListSep)
		if flush {
			if s := strings.TrimSpace(cur.String()); s != "" {
				out = append(out, s)
			}
			cur.Reset()
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}

func matchPipelineListSep(cmd string, i int) (int, bool) {
	if cmd[i] == '\n' || cmd[i] == ';' {
		return i + 1, true
	}
	if (cmd[i] == '&' || cmd[i] == '|') && i+1 < len(cmd) && cmd[i+1] == cmd[i] {
		return i + 2, true
	}
	return i, false
}

func firstPipeStageText(stmt string) string {
	cut := len(stmt)
	readledger.ScanUnquotedShellChars(stmt, func(i int, ch byte) bool {
		if ch == '|' {
			cut = i
			return true
		}
		return false
	})
	return strings.TrimSpace(stmt[:cut])
}

var grepValFlags = []string{
	"-A", "-B", "-C", "-m", "-t", "--type", "-T", "--type-not",
	"--max-count", "--context", "--after-context", "--before-context",
	"-j", "--threads", "--color", "--colors", "--sort", "--engine",
	"--exclude", "--exclude-dir",
}

func parseGrepCommandArgs(args []string) (string, []string, bool) {
	var pat string
	var paths []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			appendGrepPositionals(args[i+1:], &pat, &paths)
			return pat, paths, pat != ""
		}
		next, handled, abort := stepGrepFlagArg(args, i, &pat, &paths)
		if abort {
			return "", nil, false
		}
		if handled {
			i = next
			continue
		}
		appendGrepPositional(a, &pat, &paths)
	}
	return pat, paths, pat != ""
}

func appendGrepPositionals(items []string, pat *string, paths *[]string) {
	for _, rem := range items {
		appendGrepPositional(rem, pat, paths)
	}
}

func appendGrepPositional(tok string, pat *string, paths *[]string) {
	if *pat == "" {
		*pat = tok
	} else {
		*paths = append(*paths, tok)
	}
}

func stepGrepFlagArg(args []string, i int, pat *string, paths *[]string) (int, bool, bool) {
	a := args[i]
	switch {
	case a == "-f" || a == "--file" || strings.HasPrefix(a, "--file="):
		return i, true, true
	case a == "-e" || a == "--regexp":
		if i+1 >= len(args) {
			return i, true, true
		}
		if *pat == "" {
			*pat = args[i+1]
		}
		return i + 1, true, false
	case strings.HasPrefix(a, "--regexp="):
		if *pat == "" {
			*pat = strings.TrimPrefix(a, "--regexp=")
		}
		return i, true, false
	case strings.HasPrefix(a, "--include="):
		*paths = append(*paths, strings.TrimPrefix(a, "--include="))
		return i, true, false
	case (a == "-g" || a == "--glob" || a == "--include") && i+1 < len(args):
		*paths = append(*paths, args[i+1])
		return i + 1, true, false
	case slices.Contains(grepValFlags, a) && i+1 < len(args):
		return i + 1, true, false
	case strings.HasPrefix(a, "-") && a != "-":
		return i, true, false
	default:
		return i, false, false
	}
}

func extractCodeSymbolQuery(raw string) string {
	s := strings.TrimSpace(raw)
	for _, pair := range [][2]string{{"^", "$"}, {`\b`, `\b`}, {`\<`, `\>`}} {
		s = strings.TrimSuffix(strings.TrimPrefix(s, pair[0]), pair[1])
	}
	s = strings.TrimSpace(s)
	for _, pfx := range []string{"func ", `func\s+`, "type ", `type\s+`, "def ", `def\s+`, "class ", `class\s+`, "interface ", `interface\s+`, "struct ", `struct\s+`} {
		if strings.HasPrefix(s, pfx) {
			s = strings.TrimSpace(s[len(pfx):])
			break
		}
	}
	s = strings.TrimSuffix(s, "(")
	if len(s) < 4 || len(s) > 80 || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") || strings.Contains(s, "..") {
		return ""
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '.' {
			return ""
		}
	}
	return s
}

func isLikelyCodeIdentifier(sym string) bool {
	if len(sym) < 5 {
		return false
	}
	if strings.ContainsAny(sym, "_.") {
		return true
	}
	hasUpper, hasLower := false, false
	for i := 0; i < len(sym); i++ {
		hasUpper = hasUpper || (sym[i] >= 'A' && sym[i] <= 'Z')
		hasLower = hasLower || (sym[i] >= 'a' && sym[i] <= 'z')
	}
	return hasUpper && hasLower
}

func isNonCodeSearchScope(root, dir string, paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	nonCodeExts := []string{".log", ".txt", ".jsonl", ".csv", ".tsv", ".out", ".env", ".lock", ".sum", ".md", ".json", ".yaml", ".yml", ".toml", ".html", ".css", ".svg"}
	checked := 0
	for _, raw := range paths {
		p := filepath.ToSlash(strings.TrimSpace(raw))
		if p == "" || strings.HasPrefix(p, "!") {
			continue
		}
		checked++
		if !isNonCodePathTarget(root, dir, p, nonCodeExts) {
			return false
		}
	}
	return checked > 0
}

func isNonCodePathTarget(root, dir, p string, nonCodeExts []string) bool {
	checkPath := p
	if filepath.IsAbs(p) {
		rel, inside := relPathWithinRoot(root, dir, p)
		if !inside {
			return true
		}
		checkPath = rel
	}
	low := strings.ToLower(strings.TrimPrefix(checkPath, "./"))
	if low == ".agents/scratch" || strings.HasPrefix(low, ".agents/scratch/") || strings.Contains(low, "/.agents/scratch/") || strings.Contains(low, "/logs/") {
		return true
	}
	ext := strings.ToLower(filepath.Ext(low))
	return ext != "" && slices.Contains(nonCodeExts, ext)
}

func relPathWithinRoot(root, dir, p string) (string, bool) {
	canonRoot, canonPath := readledger.CanonicalizePath("", root), readledger.CanonicalizePath(dir, p)
	if canonRoot == "" || canonPath == "" {
		return "", false
	}
	rel, err := filepath.Rel(filepath.FromSlash(canonRoot), filepath.FromSlash(canonPath))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}
