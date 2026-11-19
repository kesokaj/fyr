package search

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type ledgerSpanRow struct {
	startLine, endLine int
	hitCount, turnNum  int
}

type editSpan struct {
	startLine, endLine int
}

func openSessionLedgerStore(sessionDir, home string) (*store, string, error) {
	st, root, err := openLedgerStore(sessionDir)
	if err == nil {
		return st, root, nil
	}
	if errors.Is(err, errNotIgnored) && ensureExistingCheckoutIgnored(sessionDir) {
		st, root, err = openLedgerStore(sessionDir)
		if err == nil {
			return st, root, nil
		}
	}
	h := strings.TrimSpace(home)
	if !errors.Is(err, errNotCheckout) || h == "" {
		return nil, "", err
	}
	dbPath := globalDB(h)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, "", err
	}
	st, err = openStore(dbPath, io.Discard)
	return st, "", err
}

// CheckAndRecordReadInSession records or deduplicates a read in the session's ledger store
// while canonicalizing targetPath relative to toolDir (supporting external checkouts).
func CheckAndRecordReadInSession(sessionDir, home, toolDir, conversationID, targetPath string, startLine, endLine, turnNum int) (ReadDecision, error) {
	st, root, err := openSessionLedgerStore(resolveSessionLedgerDir(sessionDir, toolDir), home)
	if err != nil {
		return ReadDecision{}, err
	}
	defer st.close()
	conversationID = normalizeLedgerConvID(conversationID)
	resolved := CanonicalizePath(toolDir, targetPath)
	return st.checkAndRecordResolvedRead(root, conversationID, resolved, startLine, endLine, turnNum)
}

const ledgerPendingLine = -6

func (s *store) checkAndRecordResolvedRead(root, conversationID, resolved string, startLine, endLine, turnNum int) (ReadDecision, error) {
	sumHex, data, totalLines, ok := readFileDigestAndLines(resolved)
	if !ok {
		return ReadDecision{Allowed: true, ResolvedPath: resolved}, nil
	}
	reqStart, reqEnd := normalizeLineRange(startLine, endLine, totalLines)
	genID, defTurn := s.currentGeneration(conversationID)
	if turnNum <= 0 {
		turnNum = defTurn
	}
	var hitCount int
	var hit bool
	if err := s.withImmediateTx(func() error {
		var err error
		hit, hitCount, err = s.recordResolvedReadTx(conversationID, genID, resolved, reqStart, reqEnd, sumHex, turnNum)
		return err
	}); err != nil {
		return ReadDecision{}, err
	}
	if hit {
		relPath := relDisplayPath(root, resolved)
		skel := s.fileSkeletonSummary(root, resolved, string(data))
		reason := formatUnchangedLedgerNotice(relPath, reqStart, reqEnd, sumHex, hitCount, skel)
		return ReadDecision{Allowed: false, Reason: reason, ResolvedPath: resolved, StartLine: reqStart, EndLine: reqEnd, SHA256: sumHex, GenerationID: genID, HitCount: hitCount}, nil
	}
	return ReadDecision{Allowed: true, ResolvedPath: resolved, StartLine: reqStart, EndLine: reqEnd, SHA256: sumHex, GenerationID: genID, HitCount: 1}, nil
}

func (s *store) recordResolvedReadTx(conversationID string, genID int, resolved string, reqStart, reqEnd int, sumHex string, turnNum int) (bool, int, error) {
	now := nowFunc().Unix()
	_, _ = s.db.Exec(`DELETE FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND sha256 != '' AND sha256 != ?`, conversationID, genID, resolved, sumHex)
	var hitStart, hitEnd, hitCount int
	err := s.db.QueryRow(`SELECT start_line, end_line, hit_count FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND sha256 = ? AND start_line > 0 AND start_line <= ? AND end_line >= ? ORDER BY (end_line - start_line) DESC LIMIT 1`,
		conversationID, genID, resolved, sumHex, reqStart, reqEnd).Scan(&hitStart, &hitEnd, &hitCount)
	if err == nil {
		_, _ = s.db.Exec(`UPDATE read_ledger SET hit_count = ?, turn_num = ?, updated_at = ? WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND start_line = ? AND end_line = ?`,
			hitCount+1, turnNum, now, conversationID, genID, resolved, hitStart, hitEnd)
		s.recordLedgerBlock(conversationID, turnNum, now)
		return true, hitCount + 1, nil
	}
	if err := s.upsertReadStamp(conversationID, genID, resolved, reqStart, reqEnd, sumHex, turnNum, now); err != nil {
		return false, 0, err
	}
	return false, 1, nil
}

// CheckPreWriteHashWithEdit verifies disk content before a write/edit and invalidates
// only stamps overlapping the edited line range (recording any predicted post-edit hash as pending).
func CheckPreWriteHashWithEdit(sessionDir, home, toolDir, conversationID, targetPath string, args map[string]any) (ReadDecision, error) {
	st, root, err := openSessionLedgerStore(resolveSessionLedgerDir(sessionDir, toolDir), home)
	if err != nil {
		return ReadDecision{}, err
	}
	defer st.close()
	conversationID = normalizeLedgerConvID(conversationID)
	resolved := CanonicalizePath(toolDir, targetPath)
	sumHex, data, totalLines, ok := readFileDigestAndLines(resolved)
	genID, turnNum := st.currentGeneration(conversationID)
	if !ok {
		return ReadDecision{Allowed: true, ResolvedPath: resolved, GenerationID: genID}, nil
	}
	var conflictSHA string
	if err := st.withImmediateTx(func() error {
		conflictSHA = st.verifyAndAdvancePreWriteTx(conversationID, genID, resolved, sumHex, string(data), args, turnNum)
		return nil
	}); err != nil {
		return ReadDecision{}, err
	}
	if conflictSHA != "" {
		skel := st.fileSkeletonSummary(root, resolved, string(data))
		reason := formatModifiedLedgerNotice(relDisplayPath(root, resolved), conflictSHA, sumHex, skel)
		return ReadDecision{Allowed: false, Reason: reason, ResolvedPath: resolved, StartLine: 1, EndLine: totalLines, SHA256: sumHex, GenerationID: genID}, nil
	}
	return ReadDecision{Allowed: true, ResolvedPath: resolved, StartLine: 1, EndLine: totalLines, SHA256: sumHex, GenerationID: genID}, nil
}

func (s *store) verifyAndAdvancePreWriteTx(conversationID string, genID int, resolved, sumHex, text string, args map[string]any, turnNum int) string {
	var verifiedSHA, pendingSHA string
	vErr := s.db.QueryRow(`SELECT sha256 FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND start_line >= 0 AND sha256 != '' ORDER BY (start_line = 0 AND end_line = 0) DESC, updated_at DESC LIMIT 1`,
		conversationID, genID, resolved).Scan(&verifiedSHA)
	pErr := s.db.QueryRow(`SELECT sha256 FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND start_line = ? AND end_line = ? AND sha256 != '' LIMIT 1`,
		conversationID, genID, resolved, ledgerPendingLine, ledgerPendingLine).Scan(&pendingSHA)
	if pErr == nil && pendingSHA != "" && sumHex == pendingSHA {
		_, _ = s.db.Exec(`DELETE FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND start_line = ? AND end_line = ?`,
			conversationID, genID, resolved, ledgerPendingLine, ledgerPendingLine)
		_ = s.upsertReadStamp(conversationID, genID, resolved, 0, 0, sumHex, turnNum, nowFunc().Unix())
	} else if vErr == nil && verifiedSHA != "" && sumHex == verifiedSHA {
		_, _ = s.db.Exec(`DELETE FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND ((start_line = ? AND end_line = ?) OR (start_line > 0 AND sha256 != ?))`,
			conversationID, genID, resolved, ledgerPendingLine, ledgerPendingLine, sumHex)
	} else if vErr == nil && verifiedSHA != "" {
		_, _ = s.db.Exec(`DELETE FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND (start_line > 0 OR (start_line = ? AND end_line = ?))`,
			conversationID, genID, resolved, ledgerPendingLine, ledgerPendingLine)
		return verifiedSHA
	}
	s.applyPreWriteEditInvalidation(conversationID, genID, resolved, sumHex, text, args, turnNum)
	return ""
}

func (s *store) applyPreWriteEditInvalidation(conversationID string, genID int, resolved, verifiedSHA, text string, args map[string]any, turnNum int) {
	now := nowFunc().Unix()
	if args != nil {
		if postSHA, occs, delta, ok := computeEditEffects(text, args); ok {
			s.updateStampsAcrossEdit(conversationID, genID, resolved, occs, delta, postSHA, now)
			_ = s.upsertReadStamp(conversationID, genID, resolved, 0, 0, verifiedSHA, turnNum, now)
			_ = s.upsertReadStamp(conversationID, genID, resolved, ledgerPendingLine, ledgerPendingLine, postSHA, turnNum, now)
			return
		}
	}
	_, _ = s.db.Exec(`DELETE FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND start_line > 0`, conversationID, genID, resolved)
	if args != nil && !extractBoolArg(args, "Append", "append") {
		if content, ok := extractRawStringArg(args, "Content", "content", "FileContent", "file_content"); ok {
			sentinelSHA := sha256HexString(content)
			_ = s.upsertReadStamp(conversationID, genID, resolved, 0, 0, verifiedSHA, turnNum, now)
			_ = s.upsertReadStamp(conversationID, genID, resolved, ledgerPendingLine, ledgerPendingLine, sentinelSHA, turnNum, now)
			return
		}
	}
	_, _ = s.db.Exec(`DELETE FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND start_line = ? AND end_line = ?`,
		conversationID, genID, resolved, ledgerPendingLine, ledgerPendingLine)
	_ = s.upsertReadStamp(conversationID, genID, resolved, 0, 0, "", turnNum, now)
}

func (s *store) updateStampsAcrossEdit(conversationID string, genID int, resolved string, occs []editSpan, delta int, postSHA string, now int64) {
	existing := s.listPositiveLedgerSpans(conversationID, genID, resolved)
	if len(existing) == 0 {
		return
	}
	_, _ = s.db.Exec(`DELETE FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND start_line > 0`, conversationID, genID, resolved)
	minEditStart, maxEditEnd := occs[0].startLine, occs[len(occs)-1].endLine
	multiOcc := len(occs) > 1
	for _, r := range existing {
		if spansOverlapAny(r.startLine, r.endLine, occs) {
			continue
		}
		newStart, newEnd := r.startLine, r.endLine
		if delta != 0 {
			if multiOcc && newEnd >= minEditStart {
				continue
			}
			if !multiOcc && newStart > maxEditEnd {
				newStart, newEnd = newStart+delta, newEnd+delta
			}
		}
		_, _ = s.db.Exec(`INSERT OR REPLACE INTO read_ledger(conversation_id, generation_id, resolved_path, start_line, end_line, sha256, turn_num, hit_count, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			conversationID, genID, resolved, newStart, newEnd, postSHA, r.turnNum, r.hitCount, now)
	}
}

func (s *store) listPositiveLedgerSpans(conversationID string, genID int, resolved string) []ledgerSpanRow {
	rows, err := s.db.Query(`SELECT start_line, end_line, hit_count, turn_num FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND start_line > 0 ORDER BY start_line, end_line`,
		conversationID, genID, resolved)
	if err != nil {
		return nil
	}
	defer rows.Close()
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
		if !(end < o.startLine || start > o.endLine) {
			return true
		}
	}
	return false
}

func computeEditEffects(text string, args map[string]any) (string, []editSpan, int, bool) {
	target, hasTarget := extractRawStringArg(args, "Target", "target", "old_string", "OldString", "TargetContent", "targetContent")
	repl, hasRepl := extractRawStringArg(args, "Replacement", "replacement", "new_string", "NewString", "ReplacementContent", "replacementContent")
	if !hasTarget || target == "" || !hasRepl || !strings.Contains(text, target) {
		return "", nil, 0, false
	}
	occs := locateTargetSpans(text, target)
	if len(occs) == 0 {
		return "", nil, 0, false
	}
	allowMultiple := extractBoolArg(args, "AllowMultiple", "allowMultiple", "allow_multiple", "ReplaceAll", "replace_all")
	if len(occs) > 1 && !allowMultiple {
		return "", nil, 0, false
	}
	delta := strings.Count(repl, "\n") - strings.Count(target, "\n")
	postSHA := sha256HexString(strings.ReplaceAll(text, target, repl))
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

func extractRawStringArg(args map[string]any, keys ...string) (string, bool) {
	for _, k := range keys {
		if v, ok := args[k]; ok {
			if s, ok := v.(string); ok {
				return s, true
			}
		}
	}
	return "", false
}

func extractBoolArg(args map[string]any, keys ...string) bool {
	for _, k := range keys {
		if v, ok := args[k]; ok {
			if b, ok := v.(bool); ok {
				return b
			}
		}
	}
	return false
}

func resolveSessionLedgerDir(sessionDir, toolDir string) string {
	if _, _, ok := findGitRootFast(sessionDir); ok {
		return sessionDir
	}
	if toolRoot, commonDir, ok := findGitRootFast(toolDir); ok {
		if _, err := os.Stat(projectDB(toolRoot)); err == nil || scratchIgnoredFast(toolRoot, commonDir) {
			return toolRoot
		}
	}
	return sessionDir
}

func normalizeLedgerConvID(conversationID string) string {
	id := strings.TrimSpace(conversationID)
	if id == "" {
		return "default"
	}
	return id
}

func sha256HexString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
