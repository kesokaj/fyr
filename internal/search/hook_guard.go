package search

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/trail"
)

const (
	sandboxPreamblePrefix = "The project root is the current directory,"
	sandboxPreambleEnd    = "This is a print-mode run (`agy -p`): the turn's end is the run's end."
	preInvocationSentinel = "__pre_invocation__"
)

// StripSandboxPreamble removes the fyr eval / fyr work print-mode preamble
// when present at the start of prompt so anchor checks and prefix gates see
// the operator's actual prompt.
func StripSandboxPreamble(prompt string) string {
	s := strings.TrimSpace(prompt)
	if !strings.HasPrefix(s, sandboxPreamblePrefix) {
		return prompt
	}
	if idx := strings.Index(s, sandboxPreambleEnd); idx >= 0 {
		return strings.TrimSpace(s[idx+len(sandboxPreambleEnd):])
	}
	return prompt
}

// hooksDisabled reports whether dir (or its git checkout root) contains
// .agents/scratch/no-hooks, used by paired A/B eval control scenarios.
func hooksDisabled(dir string) bool {
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	if _, err := os.Stat(filepath.Join(dir, ".agents", "scratch", "no-hooks")); err == nil {
		return true
	}
	if root, err := checkoutRoot(dir); err == nil {
		if _, err := os.Stat(filepath.Join(root, ".agents", "scratch", "no-hooks")); err == nil {
			return true
		}
	}
	return false
}

// hasAutonomousWaiver reports whether lowPrompt explicitly forbids interactive
// questions in unattended / eval sessions.
func hasAutonomousWaiver(lowPrompt string) bool {
	for _, w := range []string{
		"do not ask me anything",
		"don't ask me anything",
		"i will not answer",
		"the scope is confirmed",
		"no go needed",
		"without asking",
	} {
		if strings.Contains(lowPrompt, w) {
			return true
		}
	}
	return false
}

// checkTurnOrMidTurn stats transcriptPath, checks compaction, and determines
// whether this PreInvocation is a mid-turn tool-loop continuation.
func checkTurnOrMidTurn(dir, conversationID, transcriptPath string, invocationNum int, rawPrompt string) (SessionState, bool, error) {
	st, _, err := openLedgerStore(dir)
	if err != nil {
		return SessionState{}, false, err
	}
	defer st.close()
	if conversationID == "" {
		conversationID = "default"
	}
	curBytes, hasSize := statTranscriptBytes(transcriptPath)
	promptSum := sha256PromptHex(rawPrompt)
	var genID, lastBytes, turnCount, estTokens int
	err = st.db.QueryRow(`SELECT generation_id, last_transcript_bytes, turn_count, est_tokens FROM session_meta WHERE conversation_id = ?`, conversationID).
		Scan(&genID, &lastBytes, &turnCount, &estTokens)
	if errors.Is(err, sql.ErrNoRows) {
		est := curBytes / 4
		_, err = st.db.Exec(`INSERT INTO session_meta(conversation_id, generation_id, last_transcript_bytes, turn_count, est_tokens) VALUES (?, 0, ?, 1, ?)`, conversationID, curBytes, est)
		upsertPreInvocationStamp(st, conversationID, 0, 1, promptSum)
		return SessionState{ConversationID: conversationID, GenerationID: 0, LastTranscriptBytes: curBytes, TurnCount: 1, EstTokens: est}, false, err
	}
	if err != nil {
		return SessionState{}, false, err
	}
	return advanceOrSkipTurn(st, conversationID, genID, lastBytes, turnCount, estTokens, curBytes, hasSize, invocationNum, promptSum)
}

func advanceOrSkipTurn(st *store, conversationID string, genID, lastBytes, turnCount, estTokens, curBytes int, hasSize bool, invocationNum int, promptSum string) (SessionState, bool, error) {
	compacted := hasSize && lastBytes > 0 && curBytes < lastBytes*80/100
	var prevSum string
	_ = st.db.QueryRow(`SELECT sha256 FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND start_line = -3 LIMIT 1`,
		conversationID, genID, preInvocationSentinel).Scan(&prevSum)
	if !compacted && promptSum != "" && prevSum == promptSum && invocationNum >= 1 {
		if hasSize {
			lastBytes, estTokens = curBytes, curBytes/4
			_, _ = st.db.Exec(`UPDATE session_meta SET last_transcript_bytes = ?, est_tokens = ? WHERE conversation_id = ?`, lastBytes, estTokens, conversationID)
		}
		return SessionState{ConversationID: conversationID, GenerationID: genID, LastTranscriptBytes: lastBytes, TurnCount: turnCount, EstTokens: estTokens}, true, nil
	}
	if compacted {
		genID++
	}
	if compacted || hasSize {
		lastBytes, estTokens = curBytes, curBytes/4
	}
	turnCount++
	_, err := st.db.Exec(`UPDATE session_meta SET generation_id = ?, last_transcript_bytes = ?, turn_count = ?, est_tokens = ? WHERE conversation_id = ?`, genID, lastBytes, turnCount, estTokens, conversationID)
	upsertPreInvocationStamp(st, conversationID, genID, turnCount, promptSum)
	return SessionState{ConversationID: conversationID, GenerationID: genID, LastTranscriptBytes: lastBytes, TurnCount: turnCount, EstTokens: estTokens, Compacted: compacted}, false, err
}

func statTranscriptBytes(transcriptPath string) (int, bool) {
	if fi, err := os.Stat(strings.TrimSpace(transcriptPath)); err == nil && fi.Mode().IsRegular() {
		return int(fi.Size()), true
	}
	return 0, false
}

func sha256PromptHex(rawPrompt string) string {
	if rawPrompt == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(rawPrompt))
	return hex.EncodeToString(sum[:])
}

func upsertPreInvocationStamp(st *store, conversationID string, genID, turnCount int, promptSum string) {
	if promptSum == "" {
		return
	}
	_ = st.upsertReadStamp(conversationID, genID, preInvocationSentinel, -3, -3, promptSum, turnCount, nowFunc().Unix())
}

func isRefusedAutoInitDir(absDir string) bool {
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
		if r != "" && sameAutoInitPath(clean, filepath.Clean(r)) {
			return true
		}
	}
	return false
}

func sameAutoInitPath(a, b string) bool {
	if a == b {
		return true
	}
	ea, errA := filepath.EvalSymlinks(a)
	eb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && filepath.Clean(ea) == filepath.Clean(eb)
}

func checkScratchNoSymlinks(root string) error {
	for _, d := range []string{".agents", filepath.Join(".agents", "scratch")} {
		if fi, err := os.Lstat(filepath.Join(root, d)); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("search: %s is a symlink; refusing to write through it", filepath.ToSlash(d))
		}
	}
	return nil
}

func ensureHookGitCheckout(absDir string) (string, []string, error) {
	root, err := checkoutRoot(absDir)
	if err == nil {
		if isRefusedAutoInitDir(root) {
			return "", nil, fmt.Errorf("search: refusing to auto-initialize system/root directory %s", root)
		}
		return root, nil, nil
	}
	nested := scanNestedGitDirs(absDir, "", 1)
	if err := trail.Command(absDir, "init", "-q").Run(); err != nil {
		return "", nil, fmt.Errorf("search: git init: %w", err)
	}
	root, err = checkoutRoot(absDir)
	if err != nil {
		return "", nil, errNotCheckout
	}
	return root, nested, nil
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

func ensureHookScratchIgnored(root string, nested []string) error {
	ignoreErr := trail.Command(root, "check-ignore", "-q", ".agents/scratch/search.db").Run()
	if ignoreErr == nil && len(nested) == 0 {
		return nil
	}
	var ee *exec.ExitError
	if ignoreErr != nil && (!errors.As(ignoreErr, &ee) || ee.ExitCode() != 1) {
		return fmt.Errorf("search: git check-ignore: %w", ignoreErr)
	}
	patterns := append([]string{".agents/scratch/*"}, nested...)
	return appendGitInfoExclude(root, patterns)
}

func appendGitInfoExclude(root string, patterns []string) error {
	out, err := git(root, nil, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	commonDir := filepath.Clean(strings.TrimSpace(string(out)))
	if commonDir == "" || !filepath.IsAbs(commonDir) {
		return fmt.Errorf("search: invalid git common dir %q", commonDir)
	}
	if fi, err := os.Lstat(commonDir); err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("search: git common dir %s is not a regular directory", commonDir)
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
