package search

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SessionState holds the compaction generation and transcript metrics for a conversation.
type SessionState struct {
	ConversationID                                          string
	GenerationID, LastTranscriptBytes, TurnCount, EstTokens int
	Compacted                                               bool
}

// CapsulePivot represents a file range pre-seeded into the session read ledger by a capsule.
type CapsulePivot struct {
	Path               string
	StartLine, EndLine int
}

// ReadDecision reports whether a file read or write is allowed by the session read ledger.
type ReadDecision struct {
	Allowed                                    bool
	Reason, ResolvedPath, SHA256               string
	StartLine, EndLine, GenerationID, HitCount int
}

// BashFileRead describes a single-file read extracted from a shell command string.
type BashFileRead struct {
	Tool, Path                    string
	StartLine, EndLine, TailLines int
}

// CanonicalizePath resolves targetPath against cwd and evaluates symlinks so git worktrees never collide.
func CanonicalizePath(cwd, targetPath string) string {
	p := strings.TrimSpace(targetPath)
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) && cwd != "" {
		p = filepath.Join(cwd, p)
	}
	if !filepath.IsAbs(p) {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
	}
	p = filepath.Clean(p)
	if eval, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.ToSlash(filepath.Clean(eval))
	}
	if evalDir, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.ToSlash(filepath.Join(evalDir, filepath.Base(p)))
	}
	return filepath.ToSlash(p)
}

func openLedgerStore(dir string) (*store, string, error) {
	if dir == "" {
		dir = "."
	}
	root, err := checkoutRoot(dir)
	if err != nil {
		return nil, "", errNotCheckout
	}
	if err := guardProject(root); err != nil {
		return nil, "", err
	}
	st, err := openStore(projectDB(root), io.Discard)
	return st, root, err
}

func (s *store) currentGeneration(conversationID string) (int, int) {
	var genID, turnCount int
	if err := s.db.QueryRow(`SELECT generation_id, turn_count FROM session_meta WHERE conversation_id = ?`, conversationID).Scan(&genID, &turnCount); err != nil {
		return 0, 1
	}
	return genID, max(1, turnCount)
}

// ParseCapsulePivots extracts Hop-0 and full-body Hop-1 file ranges from a rendered slice capsule.
func ParseCapsulePivots(capsule string) []CapsulePivot {
	var out []CapsulePivot
	for _, line := range strings.Split(capsule, "\n") {
		if (!strings.HasPrefix(line, "[Hop 0] ") && !strings.HasPrefix(line, "[Hop 1] ")) || strings.Contains(line, " snippet ") {
			continue
		}
		if spec, _, ok := strings.Cut(line[strings.IndexByte(line, ']')+2:], " "); ok {
			if colon := strings.LastIndexByte(spec, ':'); colon > 0 {
				if sStr, eStr, ok := strings.Cut(spec[colon+1:], "-"); ok {
					s, err1 := strconv.Atoi(sStr)
					e, err2 := strconv.Atoi(eStr)
					if err1 == nil && err2 == nil && s > 0 && e >= s {
						out = append(out, CapsulePivot{Path: spec[:colon], StartLine: s, EndLine: e})
					}
				}
			}
		}
	}
	return out
}

// RegisterCapsulePivots records Hop-0/Hop-1 capsule ranges in read_ledger for conversationID.
func RegisterCapsulePivots(dir, conversationID string, turnNum int, pivots []CapsulePivot) error {
	if len(pivots) == 0 {
		return nil
	}
	st, root, err := openLedgerStore(dir)
	if err != nil {
		return err
	}
	defer st.close()
	if conversationID == "" {
		conversationID = "default"
	}
	genID, defTurn := st.currentGeneration(conversationID)
	if turnNum <= 0 {
		turnNum = defTurn
	}
	now := nowFunc().Unix()
	for _, p := range pivots {
		resolved := CanonicalizePath(root, p.Path)
		if sumHex, _, totalLines, ok := readFileDigestAndLines(resolved); ok {
			start, end := normalizeLineRange(p.StartLine, p.EndLine, totalLines)
			if err := st.upsertReadStamp(conversationID, genID, resolved, start, end, sumHex, turnNum, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// CheckAndRecordRead checks whether a file range was already read unchanged in this generation,
// returning [UNCHANGED] + symbol skeleton and consuming the stamp on the 2nd read.
func CheckAndRecordRead(dir, conversationID, targetPath string, startLine, endLine, turnNum int) (ReadDecision, error) {
	st, root, err := openLedgerStore(dir)
	if err != nil {
		return ReadDecision{}, err
	}
	defer st.close()
	if conversationID == "" {
		conversationID = "default"
	}
	resolved := CanonicalizePath(dir, targetPath)
	sumHex, data, totalLines, ok := readFileDigestAndLines(resolved)
	if !ok {
		return ReadDecision{Allowed: true, ResolvedPath: resolved}, nil
	}
	reqStart, reqEnd := normalizeLineRange(startLine, endLine, totalLines)
	genID, defTurn := st.currentGeneration(conversationID)
	if turnNum <= 0 {
		turnNum = defTurn
	}
	_, _ = st.db.Exec(`DELETE FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND sha256 != '' AND sha256 != ?`, conversationID, genID, resolved, sumHex)
	var hitCount int
	err = st.db.QueryRow(`SELECT hit_count FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND sha256 = ? AND start_line > 0 AND start_line <= ? AND end_line >= ? ORDER BY (end_line - start_line) DESC LIMIT 1`,
		conversationID, genID, resolved, sumHex, reqStart, reqEnd).Scan(&hitCount)
	if err == nil {
		_, _ = st.db.Exec(`DELETE FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND sha256 = ? AND start_line > 0 AND start_line <= ? AND end_line >= ?`,
			conversationID, genID, resolved, sumHex, reqStart, reqEnd)
		st.recordLedgerBlock(conversationID, turnNum, nowFunc().Unix())
		relPath := relDisplayPath(root, resolved)
		skel := st.fileSkeletonSummary(root, resolved, string(data))
		reason := fmt.Sprintf("[UNCHANGED] %s:%d-%d (sha256 %s) already in context; stamp consumed (retry to force re-read).", relPath, reqStart, reqEnd, sumHex[:min(12, len(sumHex))])
		if skel != "" {
			reason += "\n# " + relPath + "\n" + skel
		}
		return ReadDecision{Allowed: false, Reason: reason, ResolvedPath: resolved, StartLine: reqStart, EndLine: reqEnd, SHA256: sumHex, GenerationID: genID, HitCount: hitCount + 1}, nil
	}
	if err := st.upsertReadStamp(conversationID, genID, resolved, reqStart, reqEnd, sumHex, turnNum, nowFunc().Unix()); err != nil {
		return ReadDecision{}, err
	}
	return ReadDecision{Allowed: true, ResolvedPath: resolved, StartLine: reqStart, EndLine: reqEnd, SHA256: sumHex, GenerationID: genID, HitCount: 1}, nil
}

func (s *store) recordLedgerBlock(conversationID string, turnNum int, now int64) {
	_, _ = s.db.Exec(`CREATE TABLE IF NOT EXISTS read_ledger_stats(conversation_id TEXT, turn_num INT, saved_count INT, updated_at INT, PRIMARY KEY(conversation_id, turn_num))`)
	_, _ = s.db.Exec(`INSERT INTO read_ledger_stats(conversation_id, turn_num, saved_count, updated_at) VALUES (?, ?, 1, ?) ON CONFLICT(conversation_id, turn_num) DO UPDATE SET saved_count = read_ledger_stats.saved_count + 1, updated_at = excluded.updated_at`,
		conversationID, turnNum, now)
}

func (s *store) upsertReadStamp(conversationID string, genID int, resolved string, start, end int, sumHex string, turnNum int, now int64) error {
	const q = `INSERT INTO read_ledger(conversation_id, generation_id, resolved_path, start_line, end_line, sha256, turn_num, hit_count, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?) ON CONFLICT(conversation_id, generation_id, resolved_path, start_line, end_line) DO UPDATE SET sha256 = excluded.sha256, turn_num = excluded.turn_num, hit_count = 1, updated_at = excluded.updated_at`
	if _, err := s.db.Exec(q, conversationID, genID, resolved, start, end, sumHex, turnNum, now); err != nil {
		return err
	}
	if start > 0 && sumHex != "" {
		_, err := s.db.Exec(q, conversationID, genID, resolved, 0, 0, sumHex, turnNum, now)
		return err
	}
	return nil
}

// CheckPreWriteHash verifies that a previously read file has not been modified on disk before a write/edit.
func CheckPreWriteHash(dir, conversationID, targetPath string) (ReadDecision, error) {
	st, root, err := openLedgerStore(dir)
	if err != nil {
		return ReadDecision{}, err
	}
	defer st.close()
	if conversationID == "" {
		conversationID = "default"
	}
	resolved := CanonicalizePath(dir, targetPath)
	sumHex, data, totalLines, ok := readFileDigestAndLines(resolved)
	genID, turnNum := st.currentGeneration(conversationID)
	if !ok {
		return ReadDecision{Allowed: true, ResolvedPath: resolved, GenerationID: genID}, nil
	}
	var stampedSHA string
	err = st.db.QueryRow(`SELECT sha256 FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND sha256 != '' ORDER BY updated_at DESC LIMIT 1`,
		conversationID, genID, resolved).Scan(&stampedSHA)
	_, _ = st.db.Exec(`DELETE FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND start_line > 0`, conversationID, genID, resolved)
	if err == nil && stampedSHA != sumHex {
		skel := st.fileSkeletonSummary(root, resolved, string(data))
		reason := fmt.Sprintf("[MODIFIED] %s changed on disk since last read (was %s, now %s); re-read before editing.",
			relDisplayPath(root, resolved), stampedSHA[:min(12, len(stampedSHA))], sumHex[:12])
		if skel != "" {
			reason += "\n" + skel
		}
		return ReadDecision{Allowed: false, Reason: reason, ResolvedPath: resolved, StartLine: 1, EndLine: totalLines, SHA256: sumHex, GenerationID: genID}, nil
	}
	_ = st.upsertReadStamp(conversationID, genID, resolved, 0, 0, "", turnNum, nowFunc().Unix())
	return ReadDecision{Allowed: true, ResolvedPath: resolved, StartLine: 1, EndLine: totalLines, SHA256: sumHex, GenerationID: genID}, nil
}

func readFileDigestAndLines(resolvedPath string) (string, []byte, int, bool) {
	fi, err := os.Stat(resolvedPath)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxFileBytes {
		return "", nil, 0, false
	}
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return "", nil, 0, false
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), data, max(1, len(splitLines(string(data)))), true
}

func normalizeLineRange(startLine, endLine, totalLines int) (int, int) {
	totalLines = max(1, totalLines)
	if startLine < 0 {
		startLine, endLine = max(1, totalLines+startLine+1), totalLines
	}
	if startLine <= 0 {
		startLine = 1
	}
	if endLine <= 0 || endLine > totalLines {
		endLine = totalLines
	}
	return startLine, max(startLine, endLine)
}

func (s *store) fileSkeletonSummary(_, resolvedPath, text string) string {
	syms, _ := extractSymbolsAndRefs(resolvedPath, text)
	var lines []string
	for _, sy := range syms {
		lines = append(lines, fmt.Sprintf("%d-%d  %s", sy.startLine, sy.endLine, sy.sig))
	}
	return strings.Join(lines, "\n")
}

func relDisplayPath(root, resolvedPath string) string {
	if root != "" {
		if rel, err := filepath.Rel(filepath.FromSlash(CanonicalizePath("", root)), filepath.FromSlash(resolvedPath)); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(resolvedPath)
}

// CheckAndRecordBashRead parses cmd with ParseBashFileRead and routes single-file reads through CheckAndRecordRead.
func CheckAndRecordBashRead(dir, conversationID, cmd string, turnNum int) (ReadDecision, bool, error) {
	bfr, ok := ParseBashFileRead(cmd)
	if !ok {
		return ReadDecision{}, false, nil
	}
	dec, err := CheckAndRecordRead(dir, conversationID, bfr.Path, bfr.StartLine, bfr.EndLine, turnNum)
	return dec, true, err
}

// ParseBashFileRead detects single-file cat, head, tail, sed -n, nl, and bat commands without pipes or redirects.
func ParseBashFileRead(cmd string) (BashFileRead, bool) {
	tokens, ok := tokenizeSimpleShell(cmd)
	if !ok || len(tokens) < 2 {
		return BashFileRead{}, false
	}
	switch tool := filepath.Base(tokens[0]); tool {
	case "cat", "nl", "bat", "batcat":
		return parseBashSimpleOrBat(tool, tokens[1:])
	case "head", "tail":
		return parseBashHeadOrTail(tool, tokens[1:])
	case "sed":
		return parseBashSed(tokens[1:])
	}
	return BashFileRead{}, false
}

func tokenizeSimpleShell(cmd string) ([]string, bool) {
	var tokens []string
	var cur strings.Builder
	inToken, quote := false, byte(0)
	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		if quote != 0 {
			nextIdx, ok := stepQuotedShellByte(cmd, i, quote, &cur)
			if !ok {
				return nil, false
			}
			if nextIdx < 0 {
				quote = 0
			} else {
				i = nextIdx
			}
			continue
		}
		if strings.IndexByte("|&;<>()`$*?[\n\r", ch) >= 0 || (ch == '\\' && i+1 >= len(cmd)) {
			return nil, false
		}
		switch ch {
		case '\'', '"':
			quote, inToken = ch, true
		case '\\':
			i, inToken = i+1, true
			cur.WriteByte(cmd[i])
		case ' ', '\t':
			if inToken {
				tokens, inToken = append(tokens, cur.String()), false
				cur.Reset()
			}
		default:
			inToken = true
			cur.WriteByte(ch)
		}
	}
	if inToken {
		tokens = append(tokens, cur.String())
	}
	return tokens, quote == 0 && len(tokens) > 0
}

func stepQuotedShellByte(cmd string, i int, quote byte, cur *strings.Builder) (int, bool) {
	if cmd[i] == quote {
		return -1, true
	}
	if quote == '"' && (cmd[i] == '`' || (cmd[i] == '$' && i+1 < len(cmd) && (cmd[i+1] == '(' || cmd[i+1] == '{'))) {
		return i, false
	}
	if quote == '"' && cmd[i] == '\\' && i+1 < len(cmd) {
		cur.WriteByte(cmd[i+1])
		return i + 1, true
	}
	cur.WriteByte(cmd[i])
	return i, true
}

func parseBashSimpleOrBat(rawTool string, args []string) (BashFileRead, bool) {
	tool := rawTool
	if tool == "batcat" {
		tool = "bat"
	}
	start, end := 1, 0
	var files []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			files = append(files, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			nextIdx, s, e, ok := stepSimpleOrBatFlag(tool, args, i, start, end)
			if !ok {
				return BashFileRead{}, false
			}
			i, start, end = nextIdx, s, e
			continue
		}
		files = append(files, a)
	}
	if len(files) != 1 || files[0] == "-" {
		return BashFileRead{}, false
	}
	return BashFileRead{Tool: tool, Path: files[0], StartLine: start, EndLine: end}, true
}

func stepSimpleOrBatFlag(tool string, args []string, i, start, end int) (int, int, int, bool) {
	a := args[i]
	if tool == "cat" {
		ok := strings.Contains("|--number|--number-nonblank|--squeeze-blank|--show-all|--show-ends|--show-tabs|--show-nonprinting|", "|"+a+"|") ||
			(!strings.HasPrefix(a, "--") && len(a) > 1 && strings.TrimLeft(a[1:], "nbsveEtTAu") == "")
		return i, start, end, ok
	}
	if tool == "nl" {
		if strings.Contains("|-b|-n|-s|-w|-v|-i|-l|", "|"+a+"|") && i+1 < len(args) {
			return i + 1, start, end, true
		}
		return i, start, end, true
	}
	if (a == "-r" || a == "--line-range") && i+1 < len(args) {
		s, e, ok := parseBatLineRange(args[i+1])
		return i + 1, s, e, ok
	}
	if strings.HasPrefix(a, "--line-range=") || (strings.HasPrefix(a, "-r") && len(a) > 2) {
		s, e, ok := parseBatLineRange(strings.TrimPrefix(strings.TrimPrefix(a, "--line-range="), "-r"))
		return i, s, e, ok
	}
	if a == "-r" || a == "--line-range" {
		return i, 0, 0, false
	}
	if strings.Contains("|--style|--color|--paging|--decorations|--wrap|--theme|-l|--language|", "|"+a+"|") && i+1 < len(args) {
		return i + 1, start, end, true
	}
	return i, start, end, true
}

func parseBatLineRange(spec string) (int, int, bool) {
	sStr, eStr, ok := strings.Cut(spec, ":")
	if !ok {
		return 0, 0, false
	}
	start, end := 1, 0
	if sStr != "" {
		if n, err := strconv.Atoi(sStr); err != nil || n <= 0 {
			return 0, 0, false
		} else {
			start = n
		}
	}
	if eStr != "" {
		if n, err := strconv.Atoi(eStr); err != nil || n < start {
			return 0, 0, false
		} else {
			end = n
		}
	}
	return start, end, true
}

func parseBashHeadOrTail(tool string, args []string) (BashFileRead, bool) {
	rawCount := "10"
	var files []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			files = append(files, args[i+1:]...)
			i = len(args)
		case a == "-n" || a == "--lines":
			if i+1 >= len(args) {
				return BashFileRead{}, false
			}
			i, rawCount = i+1, args[i+1]
		case strings.HasPrefix(a, "-n") || strings.HasPrefix(a, "--lines="):
			rawCount = strings.TrimPrefix(strings.TrimPrefix(a, "-n"), "--lines=")
		case strings.HasPrefix(a, "-") && len(a) > 1:
			if _, err := strconv.Atoi(a[1:]); err == nil {
				rawCount = a[1:]
			} else if !strings.Contains("|-q|-v|--quiet|--silent|--verbose|", "|"+a+"|") {
				return BashFileRead{}, false
			}
		default:
			files = append(files, a)
		}
	}
	if len(files) != 1 || files[0] == "-" {
		return BashFileRead{}, false
	}
	return buildHeadOrTailRead(tool, files[0], rawCount)
}

func buildHeadOrTailRead(tool, filePath, rawCount string) (BashFileRead, bool) {
	if tool == "head" {
		if strings.HasPrefix(rawCount, "-") {
			return BashFileRead{}, false
		}
		n, err := strconv.Atoi(strings.TrimPrefix(rawCount, "+"))
		return BashFileRead{Tool: "head", Path: filePath, StartLine: 1, EndLine: n}, err == nil && n > 0
	}
	if strings.HasPrefix(rawCount, "+") {
		n, err := strconv.Atoi(rawCount[1:])
		return BashFileRead{Tool: "tail", Path: filePath, StartLine: n, EndLine: 0}, err == nil && n > 0
	}
	n, err := strconv.Atoi(strings.TrimPrefix(rawCount, "-"))
	return BashFileRead{Tool: "tail", Path: filePath, StartLine: -n, EndLine: 0, TailLines: n}, err == nil && n > 0
}

func parseBashSed(args []string) (BashFileRead, bool) {
	quiet := false
	var scripts, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			pos, i = append(pos, args[i+1:]...), len(args)
		case a == "-n" || a == "--quiet" || a == "--silent":
			quiet = true
		case a == "-e":
			if i+1 >= len(args) {
				return BashFileRead{}, false
			}
			i, scripts = i+1, append(scripts, args[i+1])
		case strings.HasPrefix(a, "-"):
			if strings.ContainsAny(a, "if") {
				return BashFileRead{}, false
			}
			quiet = quiet || strings.ContainsRune(a, 'n')
			if strings.HasSuffix(a, "e") && i+1 < len(args) {
				i, scripts = i+1, append(scripts, args[i+1])
			}
		default:
			pos = append(pos, a)
		}
	}
	if len(scripts) == 0 && len(pos) == 2 {
		scripts, pos = []string{pos[0]}, pos[1:]
	}
	if !quiet || len(scripts) != 1 || len(pos) != 1 || pos[0] == "-" {
		return BashFileRead{}, false
	}
	start, end, ok := parseSedRangeScript(scripts[0])
	return BashFileRead{Tool: "sed", Path: pos[0], StartLine: start, EndLine: end}, ok
}

func parseSedRangeScript(script string) (int, int, bool) {
	script = strings.TrimSpace(script)
	if semi := strings.IndexByte(script, ';'); semi > 0 {
		tail := strings.TrimSpace(script[semi+1:])
		if _, err := strconv.Atoi(strings.TrimSuffix(tail, "q")); !strings.HasSuffix(tail, "q") || err != nil {
			return 0, 0, false
		}
		script = strings.TrimSpace(script[:semi])
	}
	if !strings.HasSuffix(script, "p") {
		return 0, 0, false
	}
	addr := strings.TrimSuffix(script, "p")
	if startStr, endStr, ok := strings.Cut(addr, ","); ok {
		start, err := strconv.Atoi(strings.TrimSpace(startStr))
		if err != nil || start <= 0 {
			return 0, 0, false
		}
		if strings.TrimSpace(endStr) == "$" {
			return start, 0, true
		}
		end, err := strconv.Atoi(strings.TrimSpace(endStr))
		return start, end, err == nil && end >= start
	}
	line, err := strconv.Atoi(strings.TrimSpace(addr))
	return line, line, err == nil && line > 0
}
