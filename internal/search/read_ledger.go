package search

import (
	"bytes"
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
	root, commonDir, ok := findGitRootFast(dir)
	if !ok {
		return nil, "", errNotCheckout
	}
	if err := guardLedgerFast(root, commonDir); err != nil {
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
	return RegisterCapsulePivotsWithHome(dir, "", conversationID, turnNum, pivots)
}

type resolvedCapsulePivot struct {
	resolved, sumHex string
	start, end       int
}

// RegisterCapsulePivotsWithHome records Hop-0/Hop-1 capsule ranges in read_ledger,
// routing through openSessionLedgerStore so auto-ignore and globalDB fallback apply.
func RegisterCapsulePivotsWithHome(dir, home, conversationID string, turnNum int, pivots []CapsulePivot) error {
	if len(pivots) == 0 {
		return nil
	}
	st, root, err := openSessionLedgerStore(dir, home)
	if err != nil {
		return err
	}
	defer st.close()
	base := root
	if base == "" {
		base = dir
	}
	resolved := make([]resolvedCapsulePivot, 0, len(pivots))
	for _, p := range pivots {
		rp := CanonicalizePath(base, p.Path)
		if sumHex, _, totalLines, ok := readFileDigestAndLines(rp); ok {
			s, e := normalizeLineRange(p.StartLine, p.EndLine, totalLines)
			resolved = append(resolved, resolvedCapsulePivot{resolved: rp, sumHex: sumHex, start: s, end: e})
		}
	}
	if len(resolved) == 0 {
		return nil
	}
	return st.upsertCapsulePivotsTx(conversationID, turnNum, resolved)
}

func (s *store) upsertCapsulePivotsTx(conversationID string, turnNum int, pivots []resolvedCapsulePivot) error {
	conversationID = normalizeLedgerConvID(conversationID)
	genID, defTurn := s.currentGeneration(conversationID)
	if turnNum <= 0 {
		turnNum = defTurn
	}
	now := nowFunc().Unix()
	return s.withImmediateTx(func() error {
		for _, p := range pivots {
			if err := s.upsertReadStamp(conversationID, genID, p.resolved, p.start, p.end, p.sumHex, turnNum, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// CheckAndRecordRead checks whether a file range was already read unchanged in this generation,
// returning [UNCHANGED] + symbol skeleton while keeping the persistent hash-keyed stamp active.
func CheckAndRecordRead(dir, conversationID, targetPath string, startLine, endLine, turnNum int) (ReadDecision, error) {
	return CheckAndRecordReadInSession(dir, "", dir, conversationID, targetPath, startLine, endLine, turnNum)
}

func (s *store) recordLedgerBlock(conversationID string, turnNum int, now int64) {
	const ins = `INSERT INTO read_ledger_stats(conversation_id, turn_num, saved_count, updated_at) VALUES (?, ?, 1, ?) ON CONFLICT(conversation_id, turn_num) DO UPDATE SET saved_count = read_ledger_stats.saved_count + 1, updated_at = excluded.updated_at`
	if _, err := s.db.Exec(ins, conversationID, turnNum, now); err != nil {
		_, _ = s.db.Exec(`CREATE TABLE IF NOT EXISTS read_ledger_stats(conversation_id TEXT, turn_num INT, saved_count INT, updated_at INT, PRIMARY KEY(conversation_id, turn_num))`)
		_, _ = s.db.Exec(ins, conversationID, turnNum, now)
	}
}

func (s *store) upsertReadStamp(conversationID string, genID int, resolved string, start, end int, sumHex string, turnNum int, now int64) error {
	if start > 0 && end >= start && sumHex != "" {
		var minS, maxE int
		if err := s.db.QueryRow(
			`SELECT COALESCE(MIN(start_line), 0), COALESCE(MAX(end_line), 0) FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND sha256 = ? AND start_line > 0 AND start_line <= ? AND end_line >= ?`,
			conversationID, genID, resolved, sumHex, end+1, start-1,
		).Scan(&minS, &maxE); err == nil && minS > 0 {
			start, end = min(start, minS), max(end, maxE)
			_, _ = s.db.Exec(
				`DELETE FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND sha256 = ? AND start_line > 0 AND start_line <= ? AND end_line >= ?`,
				conversationID, genID, resolved, sumHex, end, start,
			)
		}
	}
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
	trimmed := bytes.TrimSuffix(data, []byte("\n"))
	totalLines := max(1, bytes.Count(trimmed, []byte("\n"))+1)
	return hex.EncodeToString(sum[:]), data, totalLines, true
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
	syms, _ := extractSymbolsAndRefsForIndex(resolvedPath, text)
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
