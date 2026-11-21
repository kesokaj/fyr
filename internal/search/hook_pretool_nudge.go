package search

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
)

func handlePreToolGrepNudgeCtx(ctx context.Context, home, dir string, wdSt *store, wdRoot string, in HookInput, out io.Writer) error {
	if ctx.Err() != nil || hooksDisabledForHome(home, dir) {
		return writeDecision(out, "allow", "")
	}
	args := in.ToolCall.Args
	pattern := extractStringArg(args, "Query", "query", "Pattern", "pattern", "Regex", "regex")
	var paths []string
	if p := extractStringArg(args, "SearchPath", "searchPath", "search_path", "Path", "path", "Dir", "dir", "Glob", "glob"); p != "" {
		paths = append(paths, p)
	}
	paths = append(paths, extractStringSliceArg(args, "Includes", "includes", "Include", "include")...)
	if inc := extractStringArg(args, "Includes", "includes", "Include", "include"); inc != "" {
		paths = append(paths, inc)
	}
	toolLabel := normalizeHookToolName(in.ToolCall.Name)
	if toolLabel == "" {
		toolLabel = "grep_search"
	}
	nudge := evaluateCodeSearchNudgeCtx(ctx, home, dir, wdSt, wdRoot, in, toolLabel, pattern, paths)
	return writeDecision(out, "allow", nudge)
}

func checkBashGrepNudgeCtx(ctx context.Context, home, dir string, wdSt *store, wdRoot string, in HookInput, cmd string) string {
	if ctx.Err() != nil || strings.TrimSpace(cmd) == "" {
		return ""
	}
	if tool, pat, paths, ok := extractBashGrepQuery(cmd); ok {
		return evaluateCodeSearchNudgeCtx(ctx, home, dir, wdSt, wdRoot, in, tool, pat, paths)
	}
	return ""
}

func evaluateCodeSearchNudgeCtx(ctx context.Context, home, dir string, wdSt *store, wdRoot string, in HookInput, toolLabel, pattern string, paths []string) string {
	sym := extractCodeSymbolQuery(pattern)
	if sym == "" || ctx.Err() != nil {
		return ""
	}
	root := resolveSearchNudgeRoot(dir, wdRoot, paths)
	if root == "" || hooksDisabledForHome(home, root) || isNonCodeSearchScope(root, dir, paths) {
		return ""
	}
	st, ownSt := openIndexedNudgeStore(root, wdSt, wdRoot)
	if st == nil {
		return ""
	}
	if ownSt {
		defer st.close()
	}
	indexed, matchedSym := storeCheckIndexedSymbolCtx(ctx, st, sym)
	if !indexed || ctx.Err() != nil || (!matchedSym && !isLikelyCodeIdentifier(sym)) {
		return ""
	}
	convID := normalizeLedgerConvID(in.ConversationID)
	genID, turnNum := st.currentGeneration(convID)
	if !markSessionSentinelOnce(st, convID, genID, turnNum, fmt.Sprintf("__search_nudge__:t%d", turnNum)) {
		return ""
	}
	if matchedSym {
		return fmt.Sprintf("[SEARCH_NUDGE] %s is indexed; prefer `fyr search --refs %s` or `fyr search --slice %s` over %s for symbol lookups.", sym, sym, sym, toolLabel)
	}
	return fmt.Sprintf("[SEARCH_NUDGE] search.db is indexed; prefer `fyr search --refs %s` or `fyr search --slice %s` over %s for code-symbol lookups.", sym, sym, toolLabel)
}

func resolveSearchNudgeRoot(dir, wdRoot string, paths []string) string {
	if wdRoot != "" && isRegularFileFast(projectDB(wdRoot)) {
		return wdRoot
	}
	if root, _, ok := findGitRootFast(dir); ok && isRegularFileFast(projectDB(root)) {
		return root
	}
	for _, p := range paths {
		if filepath.IsAbs(p) {
			if root, _, ok := findGitRootFast(existingAncestorDir(p)); ok && isRegularFileFast(projectDB(root)) {
				return root
			}
		}
	}
	return ""
}

func openIndexedNudgeStore(root string, wdSt *store, wdRoot string) (*store, bool) {
	if wdSt != nil && wdRoot != "" && sameAutoInitPath(root, wdRoot) {
		return wdSt, false
	}
	if !isRegularFileFast(projectDB(root)) || checkScratchNoSymlinks(root) != nil {
		return nil, false
	}
	st, _, err := openLedgerStore(root)
	return st, err == nil && st != nil
}

func storeCheckIndexedSymbolCtx(ctx context.Context, st *store, sym string) (bool, bool) {
	var hasAny, hit int
	if err := st.db.QueryRowContext(ctx, `SELECT 1 FROM symbols LIMIT 1`).Scan(&hasAny); err != nil || hasAny != 1 {
		return false, false
	}
	lookup := sym
	if _, after, ok := strings.Cut(sym, "."); ok && after != "" {
		lookup = after
	}
	err := st.db.QueryRowContext(ctx, `SELECT 1 FROM symbols WHERE name = ? LIMIT 1`, lookup).Scan(&hit)
	return true, err == nil && hit == 1
}

func extractBashGrepQuery(cmd string) (string, string, []string, bool) {
	for _, stmt := range splitBashTopPipelines(cmd) {
		firstStage := firstPipeStageText(stmt)
		toks := tokenizeShellSegment(firstStage)
		cmdTok, rest := unwrapSegmentCommand(toks)
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
		i, q, heredocs, flush = stepPipelineSplitChar(cmd, i, heredocs, &cur)
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

func stepPipelineSplitChar(cmd string, i int, heredocs []string, cur *strings.Builder) (int, byte, []string, bool) {
	if cmd[i] == '\\' && i+1 < len(cmd) {
		cur.WriteString(cmd[i : i+2])
		return i + 1, 0, heredocs, false
	}
	if next, handled := writeOpaqueAtomOrComment(cmd, i, cur); handled {
		if next == i {
			return skipLineComment(cmd, i), 0, heredocs, false
		}
		return next - 1, 0, heredocs, false
	}
	if cmd[i] == '$' && i+1 < len(cmd) && cmd[i+1] == '\'' {
		cur.WriteString("$'")
		return i + 1, '$', heredocs, false
	}
	if cmd[i] == '\'' || cmd[i] == '"' {
		cur.WriteByte(cmd[i])
		return i, cmd[i], heredocs, false
	}
	if cmd[i] == '<' && i+1 < len(cmd) && cmd[i+1] == '<' && (i+2 >= len(cmd) || cmd[i+2] != '<') {
		delim, next := parseHeredocDelim(cmd, i+2)
		if delim != "" {
			heredocs = append(heredocs, delim)
		}
		cur.WriteString(cmd[i:next])
		return next - 1, 0, heredocs, false
	}
	if next, sep := matchPipelineListSep(cmd, i); sep {
		if cmd[i] == '\n' && len(heredocs) > 0 {
			next, heredocs = skipHeredocBodies(cmd, next, heredocs), nil
		}
		return next - 1, 0, heredocs, true
	}
	cur.WriteByte(cmd[i])
	return i, 0, heredocs, false
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
	scanUnquotedShellChars(stmt, func(i int, ch byte) bool {
		if ch == '|' {
			cut = i
			return true
		}
		return false
	})
	return strings.TrimSpace(stmt[:cut])
}

func parseGrepCommandArgs(args []string) (string, []string, bool) {
	var pat string
	var paths []string
	addPos := func(tok string) {
		if pat == "" {
			pat = tok
		} else {
			paths = append(paths, tok)
		}
	}
	valFlags := []string{"-A", "-B", "-C", "-m", "-t", "--type", "-T", "--type-not", "--max-count", "--context", "--after-context", "--before-context", "-j", "--threads", "--color", "--colors", "--sort", "--engine", "--exclude", "--exclude-dir"}
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--":
			for _, rem := range args[i+1:] {
				addPos(rem)
			}
			return pat, paths, pat != ""
		case a == "-f" || a == "--file" || strings.HasPrefix(a, "--file="):
			return "", nil, false
		case a == "-e" || a == "--regexp":
			if i+1 >= len(args) {
				return "", nil, false
			}
			i++
			if pat == "" {
				pat = args[i]
			}
		case strings.HasPrefix(a, "--regexp="):
			if pat == "" {
				pat = strings.TrimPrefix(a, "--regexp=")
			}
		case strings.HasPrefix(a, "--include="):
			paths = append(paths, strings.TrimPrefix(a, "--include="))
		case (a == "-g" || a == "--glob" || a == "--include") && i+1 < len(args):
			i++
			paths = append(paths, args[i])
		case slices.Contains(valFlags, a) && i+1 < len(args):
			i++
		case strings.HasPrefix(a, "-") && a != "-":
		default:
			addPos(a)
		}
	}
	return pat, paths, pat != ""
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
		if c := s[i]; !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '.') {
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
	canonRoot, canonPath := CanonicalizePath("", root), CanonicalizePath(dir, p)
	if canonRoot == "" || canonPath == "" {
		return "", false
	}
	rel, err := filepath.Rel(filepath.FromSlash(canonRoot), filepath.FromSlash(canonPath))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}
