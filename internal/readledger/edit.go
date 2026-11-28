package readledger

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

type ledgerSpanRow struct {
	startLine, endLine int
	hitCount, turnNum  int
}

type editSpan struct {
	startLine, endLine int
}

// CheckPreWriteHashWithEdit verifies disk content before a write/edit and invalidates
// only stamps overlapping the edited line range (recording any predicted post-edit hash as pending).
func CheckPreWriteHashWithEdit(h Hooks, sessionDir, home, toolDir, conversationID, targetPath string, args map[string]any) (ReadDecision, error) {
	ledgerDir := sessionDir
	if h.ResolveLedgerDir != nil {
		ledgerDir = h.ResolveLedgerDir(sessionDir, toolDir)
	}
	st, root, err := h.OpenSessionStore(ledgerDir, home)
	if err != nil {
		return ReadDecision{}, err
	}
	defer st.Close()
	conversationID = NormalizeConvID(conversationID)
	resolved := CanonicalizePath(toolDir, targetPath)
	sumHex, data, totalLines, ok := ReadFileDigestAndLines(resolved)
	genID, turnNum := CurrentGeneration(st.DB(), conversationID)
	if !ok {
		return ReadDecision{Allowed: true, ResolvedPath: resolved, GenerationID: genID}, nil
	}
	var conflictSHA string
	if err := st.WithImmediateTx(func() error {
		conflictSHA = verifyAndAdvancePreWriteTx(st.DB(), h.Now, conversationID, genID, resolved, sumHex, string(data), args, turnNum)
		return nil
	}); err != nil {
		return ReadDecision{}, err
	}
	if conflictSHA != "" {
		reason := formatModifiedNotice(h, root, resolved, string(data), conflictSHA, sumHex)
		return ReadDecision{Allowed: false, Reason: reason, ResolvedPath: resolved, StartLine: 1, EndLine: totalLines, SHA256: sumHex, GenerationID: genID}, nil
	}
	return ReadDecision{Allowed: true, ResolvedPath: resolved, StartLine: 1, EndLine: totalLines, SHA256: sumHex, GenerationID: genID}, nil
}

func formatModifiedNotice(h Hooks, root, resolved, text, conflictSHA, sumHex string) string {
	relPath := RelDisplayPath(root, resolved)
	var skel string
	if h.SkeletonSummary != nil {
		skel = h.SkeletonSummary(root, resolved, text)
	}
	if h.FormatModified != nil {
		return h.FormatModified(relPath, conflictSHA, sumHex, skel)
	}
	return fmt.Sprintf("[MODIFIED]\nPath: %s\n", relPath)
}

func verifyAndAdvancePreWriteTx(db *sql.DB, nowFn func() time.Time, conversationID string, genID int, resolved, sumHex, text string, args map[string]any, turnNum int) string {
	var verifiedSHA, pendingSHA string
	vErr := db.QueryRow(`SELECT sha256 FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND start_line >= 0 AND sha256 != '' ORDER BY (start_line = 0 AND end_line = 0) DESC, updated_at DESC LIMIT 1`,
		conversationID, genID, resolved).Scan(&verifiedSHA)
	pErr := db.QueryRow(`SELECT sha256 FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND start_line = ? AND end_line = ? AND sha256 != '' LIMIT 1`,
		conversationID, genID, resolved, PendingLine, PendingLine).Scan(&pendingSHA)
	if pErr == nil && pendingSHA != "" && sumHex == pendingSHA {
		_, _ = db.Exec(`DELETE FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND start_line = ? AND end_line = ?`,
			conversationID, genID, resolved, PendingLine, PendingLine)
		_ = UpsertStamp(db, conversationID, genID, resolved, 0, 0, sumHex, turnNum, hooksNow(nowFn).Unix())
	} else if vErr == nil && verifiedSHA != "" && sumHex == verifiedSHA {
		_, _ = db.Exec(`DELETE FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND ((start_line = ? AND end_line = ?) OR (start_line > 0 AND sha256 != ?))`,
			conversationID, genID, resolved, PendingLine, PendingLine, sumHex)
	} else if vErr == nil && verifiedSHA != "" {
		_, _ = db.Exec(`DELETE FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND (start_line > 0 OR (start_line = ? AND end_line = ?))`,
			conversationID, genID, resolved, PendingLine, PendingLine)
		return verifiedSHA
	}
	applyPreWriteEditInvalidation(db, nowFn, conversationID, genID, resolved, sumHex, text, args, turnNum)
	return ""
}

func applyPreWriteEditInvalidation(db *sql.DB, nowFn func() time.Time, conversationID string, genID int, resolved, verifiedSHA, text string, args map[string]any, turnNum int) {
	now := hooksNow(nowFn).Unix()
	if args != nil {
		if postSHA, occs, delta, ok := computeEditEffects(text, args); ok {
			updateStampsAcrossEdit(db, conversationID, genID, resolved, occs, delta, postSHA, now)
			_ = UpsertStamp(db, conversationID, genID, resolved, 0, 0, verifiedSHA, turnNum, now)
			_ = UpsertStamp(db, conversationID, genID, resolved, PendingLine, PendingLine, postSHA, turnNum, now)
			return
		}
	}
	_, _ = db.Exec(`DELETE FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND start_line > 0`, conversationID, genID, resolved)
	if args != nil && !ledgerBoolArg(args, "Append", "append") {
		if content, ok := ledgerStringArg(args, "Content", "content", "FileContent", "file_content"); ok {
			sentinelSHA := ledgerSHA256Hex(content)
			_ = UpsertStamp(db, conversationID, genID, resolved, 0, 0, verifiedSHA, turnNum, now)
			_ = UpsertStamp(db, conversationID, genID, resolved, PendingLine, PendingLine, sentinelSHA, turnNum, now)
			return
		}
	}
	_, _ = db.Exec(`DELETE FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND start_line = ? AND end_line = ?`,
		conversationID, genID, resolved, PendingLine, PendingLine)
	_ = UpsertStamp(db, conversationID, genID, resolved, 0, 0, "", turnNum, now)
}

func updateStampsAcrossEdit(db *sql.DB, conversationID string, genID int, resolved string, occs []editSpan, delta int, postSHA string, now int64) {
	existing := listPositiveLedgerSpans(db, conversationID, genID, resolved)
	if len(existing) == 0 {
		return
	}
	_, _ = db.Exec(`DELETE FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND start_line > 0`, conversationID, genID, resolved)
	for _, r := range existing {
		newStart, newEnd, keep := shiftEditSpan(r, occs, delta)
		if !keep {
			continue
		}
		_, _ = db.Exec(`INSERT OR REPLACE INTO read_ledger(conversation_id, generation_id, resolved_path, start_line, end_line, sha256, turn_num, hit_count, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			conversationID, genID, resolved, newStart, newEnd, postSHA, r.turnNum, r.hitCount, now)
	}
}

func shiftEditSpan(r ledgerSpanRow, occs []editSpan, delta int) (int, int, bool) {
	if spansOverlapAny(r.startLine, r.endLine, occs) {
		return 0, 0, false
	}
	if delta == 0 {
		return r.startLine, r.endLine, true
	}
	if len(occs) > 1 {
		if r.endLine >= occs[0].startLine {
			return 0, 0, false
		}
		return r.startLine, r.endLine, true
	}
	if r.startLine > occs[0].endLine {
		return r.startLine + delta, r.endLine + delta, true
	}
	return r.startLine, r.endLine, true
}

func listPositiveLedgerSpans(db *sql.DB, conversationID string, genID int, resolved string) []ledgerSpanRow {
	rows, err := db.Query(`SELECT start_line, end_line, hit_count, turn_num FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND start_line > 0 ORDER BY start_line, end_line`,
		conversationID, genID, resolved)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []ledgerSpanRow
	for rows.Next() {
		var r ledgerSpanRow
		if rows.Scan(&r.startLine, &r.endLine, &r.hitCount, &r.turnNum) == nil {
			out = append(out, r)
		}
	}
	return out
}

func spansOverlapAny(start, end int, occs []editSpan) bool {
	for _, o := range occs {
		if end >= o.startLine && start <= o.endLine {
			return true
		}
	}
	return false
}

func computeEditEffects(text string, args map[string]any) (string, []editSpan, int, bool) {
	target, hasTarget := ledgerStringArg(args, "Target", "target", "old_string", "OldString", "TargetContent", "targetContent")
	repl, hasRepl := ledgerStringArg(args, "Replacement", "replacement", "new_string", "NewString", "ReplacementContent", "replacementContent")
	if !hasTarget || target == "" || !hasRepl || !strings.Contains(text, target) {
		return "", nil, 0, false
	}
	occs := locateTargetSpans(text, target)
	if len(occs) == 0 {
		return "", nil, 0, false
	}
	allowMultiple := ledgerBoolArg(args, "AllowMultiple", "allowMultiple", "allow_multiple", "ReplaceAll", "replace_all")
	if len(occs) > 1 && !allowMultiple {
		return "", nil, 0, false
	}
	delta := strings.Count(repl, "\n") - strings.Count(target, "\n")
	postSHA := ledgerSHA256Hex(strings.ReplaceAll(text, target, repl))
	return postSHA, occs, delta, true
}

func locateTargetSpans(text, target string) []editSpan {
	var out []editSpan
	line := 1
	pos := 0
	for {
		idx := strings.Index(text[pos:], target)
		if idx < 0 {
			break
		}
		matchStart := pos + idx
		line += strings.Count(text[pos:matchStart], "\n")
		spanNewlines := strings.Count(target, "\n")
		endLine := line + spanNewlines
		if strings.HasSuffix(target, "\n") && endLine > line {
			endLine--
		}
		out = append(out, editSpan{startLine: line, endLine: endLine})
		line += spanNewlines
		pos = matchStart + len(target)
	}
	return out
}

func ledgerStringArg(args map[string]any, keys ...string) (string, bool) {
	for _, k := range keys {
		if v, ok := args[k]; ok {
			if s, ok := v.(string); ok {
				return s, true
			}
		}
	}
	return "", false
}

func ledgerBoolArg(args map[string]any, keys ...string) bool {
	for _, k := range keys {
		if v, ok := args[k]; ok {
			if b, ok := v.(bool); ok {
				return b
			}
		}
	}
	return false
}

func ledgerSHA256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
