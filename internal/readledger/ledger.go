package readledger

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// MaxFileBytes is the largest regular file hashed by the read ledger.
	MaxFileBytes = 1 << 20
	// PendingLine is the synthetic start/end line row storing a predicted post-edit sha256.
	PendingLine = -6
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

// ResolvedCapsulePivot is a pre-digested capsule pivot ready for transactional upsert.
type ResolvedCapsulePivot struct {
	Resolved, SumHex string
	Start, End       int
}

// ReadDecision reports whether a file read or write is allowed by the session read ledger.
type ReadDecision struct {
	Allowed                                    bool
	Reason, ResolvedPath, SHA256               string
	StartLine, EndLine, GenerationID, HitCount int
}

// DBStore adapts a caller-owned SQLite store so readledger runs transactional SQL
// without importing internal/search or modernc.org/sqlite directly.
type DBStore interface {
	DB() *sql.DB
	WithImmediateTx(fn func() error) error
	Close()
}

// Hooks injects caller-owned store resolution, symbol skeletons, notice formatting, and clock.
type Hooks struct {
	OpenSessionStore func(sessionDir, home string) (DBStore, string, error)
	ResolveLedgerDir func(sessionDir, toolDir string) string
	SkeletonSummary  func(root, resolvedPath, text string) string
	FormatUnchanged  func(relPath string, start, end int, sumHex string, hitCount int, skel string) string
	FormatModified   func(relPath, prevSHA, curSHA, skel string) string
	Now              func() time.Time
}

func hooksNow(fn func() time.Time) time.Time {
	if fn != nil {
		return fn()
	}
	return time.Now()
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

// CurrentGeneration reads the compaction generation and turn count for conversationID.
func CurrentGeneration(db *sql.DB, conversationID string) (int, int) {
	var genID, turnCount int
	if err := db.QueryRow(`SELECT generation_id, turn_count FROM session_meta WHERE conversation_id = ?`, conversationID).Scan(&genID, &turnCount); err != nil {
		return 0, 1
	}
	return genID, max(1, turnCount)
}

// ParseCapsulePivots extracts Hop-0 and full-body Hop-1 file ranges from a rendered slice capsule.
func ParseCapsulePivots(capsule string) []CapsulePivot {
	var out []CapsulePivot
	for _, line := range strings.Split(capsule, "\n") {
		if p, ok := parseCapsulePivotLine(line); ok {
			out = append(out, p)
		}
	}
	return out
}

func parseCapsulePivotLine(line string) (CapsulePivot, bool) {
	if (!strings.HasPrefix(line, "[Hop 0] ") && !strings.HasPrefix(line, "[Hop 1] ")) || strings.Contains(line, " snippet ") {
		return CapsulePivot{}, false
	}
	spec, _, ok := strings.Cut(line[strings.IndexByte(line, ']')+2:], " ")
	if !ok {
		return CapsulePivot{}, false
	}
	colon := strings.LastIndexByte(spec, ':')
	if colon <= 0 {
		return CapsulePivot{}, false
	}
	sStr, eStr, ok := strings.Cut(spec[colon+1:], "-")
	if !ok {
		return CapsulePivot{}, false
	}
	s, err1 := strconv.Atoi(sStr)
	e, err2 := strconv.Atoi(eStr)
	if err1 != nil || err2 != nil || s <= 0 || e < s {
		return CapsulePivot{}, false
	}
	return CapsulePivot{Path: spec[:colon], StartLine: s, EndLine: e}, true
}

// ResolveCapsulePivots canonicalizes and hashes readable capsule pivots against base.
func ResolveCapsulePivots(base string, pivots []CapsulePivot) []ResolvedCapsulePivot {
	resolved := make([]ResolvedCapsulePivot, 0, len(pivots))
	for _, p := range pivots {
		rp := CanonicalizePath(base, p.Path)
		if sumHex, _, totalLines, ok := ReadFileDigestAndLines(rp); ok {
			s, e := NormalizeLineRange(p.StartLine, p.EndLine, totalLines)
			resolved = append(resolved, ResolvedCapsulePivot{Resolved: rp, SumHex: sumHex, Start: s, End: e})
		}
	}
	return resolved
}

// RegisterCapsulePivots records Hop-0/Hop-1 capsule ranges in read_ledger using h.OpenSessionStore.
func RegisterCapsulePivots(h Hooks, dir, home, conversationID string, turnNum int, pivots []CapsulePivot) error {
	if len(pivots) == 0 {
		return nil
	}
	st, root, err := h.OpenSessionStore(dir, home)
	if err != nil {
		return err
	}
	defer st.Close()
	base := root
	if base == "" {
		base = dir
	}
	resolved := ResolveCapsulePivots(base, pivots)
	if len(resolved) == 0 {
		return nil
	}
	return UpsertCapsulePivotsTx(st, h.Now, conversationID, turnNum, resolved)
}

// UpsertCapsulePivotsTx atomically inserts pre-resolved capsule pivots into read_ledger.
func UpsertCapsulePivotsTx(st DBStore, nowFn func() time.Time, conversationID string, turnNum int, pivots []ResolvedCapsulePivot) error {
	conversationID = NormalizeConvID(conversationID)
	genID, defTurn := CurrentGeneration(st.DB(), conversationID)
	if turnNum <= 0 {
		turnNum = defTurn
	}
	now := hooksNow(nowFn).Unix()
	return st.WithImmediateTx(func() error {
		for _, p := range pivots {
			if err := UpsertStamp(st.DB(), conversationID, genID, p.Resolved, p.Start, p.End, p.SumHex, turnNum, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// CheckAndRecordReadInSession records or deduplicates a read in the session's ledger store
// while canonicalizing targetPath relative to toolDir (supporting external checkouts).
func CheckAndRecordReadInSession(h Hooks, sessionDir, home, toolDir, conversationID, targetPath string, startLine, endLine, turnNum int) (ReadDecision, error) {
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
	return CheckAndRecordResolvedRead(st, h, root, conversationID, resolved, startLine, endLine, turnNum)
}

// CheckAndRecordResolvedRead deduplicates or records a read for an already-canonicalized path.
func CheckAndRecordResolvedRead(st DBStore, h Hooks, root, conversationID, resolved string, startLine, endLine, turnNum int) (ReadDecision, error) {
	sumHex, data, totalLines, ok := ReadFileDigestAndLines(resolved)
	if !ok {
		return ReadDecision{Allowed: true, ResolvedPath: resolved}, nil
	}
	reqStart, reqEnd := NormalizeLineRange(startLine, endLine, totalLines)
	genID, defTurn := CurrentGeneration(st.DB(), conversationID)
	if turnNum <= 0 {
		turnNum = defTurn
	}
	var hitCount int
	var hit bool
	if err := st.WithImmediateTx(func() error {
		var err error
		hit, hitCount, err = recordResolvedReadTx(st.DB(), h.Now, conversationID, genID, resolved, reqStart, reqEnd, sumHex, turnNum)
		return err
	}); err != nil {
		return ReadDecision{}, err
	}
	if hit {
		reason := formatUnchangedNotice(h, root, resolved, string(data), reqStart, reqEnd, sumHex, hitCount)
		return ReadDecision{Allowed: false, Reason: reason, ResolvedPath: resolved, StartLine: reqStart, EndLine: reqEnd, SHA256: sumHex, GenerationID: genID, HitCount: hitCount}, nil
	}
	return ReadDecision{Allowed: true, ResolvedPath: resolved, StartLine: reqStart, EndLine: reqEnd, SHA256: sumHex, GenerationID: genID, HitCount: 1}, nil
}

func formatUnchangedNotice(h Hooks, root, resolved, text string, reqStart, reqEnd int, sumHex string, hitCount int) string {
	relPath := RelDisplayPath(root, resolved)
	var skel string
	if h.SkeletonSummary != nil {
		skel = h.SkeletonSummary(root, resolved, text)
	}
	if h.FormatUnchanged != nil {
		return h.FormatUnchanged(relPath, reqStart, reqEnd, sumHex, hitCount, skel)
	}
	return fmt.Sprintf("[UNCHANGED]\nPath: %s\nRange: %d-%d\n", relPath, reqStart, reqEnd)
}

func recordResolvedReadTx(db *sql.DB, nowFn func() time.Time, conversationID string, genID int, resolved string, reqStart, reqEnd int, sumHex string, turnNum int) (bool, int, error) {
	now := hooksNow(nowFn).Unix()
	_, _ = db.Exec(`DELETE FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND start_line >= 0 AND sha256 != '' AND sha256 != ?`, conversationID, genID, resolved, sumHex)
	var hitStart, hitEnd, hitCount int
	err := db.QueryRow(`SELECT start_line, end_line, hit_count FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND sha256 = ? AND start_line > 0 AND start_line <= ? AND end_line >= ? ORDER BY (end_line - start_line) DESC LIMIT 1`,
		conversationID, genID, resolved, sumHex, reqStart, reqEnd).Scan(&hitStart, &hitEnd, &hitCount)
	if err == nil {
		_, _ = db.Exec(`UPDATE read_ledger SET hit_count = ?, turn_num = ?, updated_at = ? WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND start_line = ? AND end_line = ?`,
			hitCount+1, turnNum, now, conversationID, genID, resolved, hitStart, hitEnd)
		RecordBlock(db, conversationID, turnNum, now)
		return true, hitCount + 1, nil
	}
	if err := UpsertStamp(db, conversationID, genID, resolved, reqStart, reqEnd, sumHex, turnNum, now); err != nil {
		return false, 0, err
	}
	return false, 1, nil
}

// RecordBlock increments read_ledger_stats for a blocked duplicate read.
func RecordBlock(db *sql.DB, conversationID string, turnNum int, now int64) {
	const ins = `INSERT INTO read_ledger_stats(conversation_id, turn_num, saved_count, updated_at) VALUES (?, ?, 1, ?) ON CONFLICT(conversation_id, turn_num) DO UPDATE SET saved_count = read_ledger_stats.saved_count + 1, updated_at = excluded.updated_at`
	if _, err := db.Exec(ins, conversationID, turnNum, now); err != nil {
		_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS read_ledger_stats(conversation_id TEXT, turn_num INT, saved_count INT, updated_at INT, PRIMARY KEY(conversation_id, turn_num))`)
		_, _ = db.Exec(ins, conversationID, turnNum, now)
	}
}

// UpsertStamp writes a read_ledger row, coalescing adjacent/overlapping ranges with matching sha256.
func UpsertStamp(db *sql.DB, conversationID string, genID int, resolved string, start, end int, sumHex string, turnNum int, now int64) error {
	if start > 0 && end >= start && sumHex != "" {
		var minS, maxE int
		if err := db.QueryRow(
			`SELECT COALESCE(MIN(start_line), 0), COALESCE(MAX(end_line), 0) FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND sha256 = ? AND start_line > 0 AND start_line <= ? AND end_line >= ?`,
			conversationID, genID, resolved, sumHex, end+1, start-1,
		).Scan(&minS, &maxE); err == nil && minS > 0 {
			start, end = min(start, minS), max(end, maxE)
			_, _ = db.Exec(
				`DELETE FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND sha256 = ? AND start_line > 0 AND start_line <= ? AND end_line >= ?`,
				conversationID, genID, resolved, sumHex, end, start,
			)
		}
	}
	const q = `INSERT INTO read_ledger(conversation_id, generation_id, resolved_path, start_line, end_line, sha256, turn_num, hit_count, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?) ON CONFLICT(conversation_id, generation_id, resolved_path, start_line, end_line) DO UPDATE SET sha256 = excluded.sha256, turn_num = excluded.turn_num, hit_count = CASE WHEN excluded.start_line = -5 AND excluded.end_line = -5 THEN read_ledger.hit_count + 1 ELSE 1 END, updated_at = excluded.updated_at`
	if _, err := db.Exec(q, conversationID, genID, resolved, start, end, sumHex, turnNum, now); err != nil {
		return err
	}
	if start > 0 && sumHex != "" {
		_, err := db.Exec(q, conversationID, genID, resolved, 0, 0, sumHex, turnNum, now)
		return err
	}
	return nil
}

// ReadFileDigestAndLines reads a regular file up to MaxFileBytes and returns its sha256 hex, bytes, and line count.
func ReadFileDigestAndLines(resolvedPath string) (string, []byte, int, bool) {
	fi, err := os.Stat(resolvedPath)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > MaxFileBytes {
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

// NormalizeLineRange clamps startLine/endLine (including negative tail counts) to [1, totalLines].
func NormalizeLineRange(startLine, endLine, totalLines int) (int, int) {
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

// NormalizeConvID trims conversationID and substitutes "default" when empty.
func NormalizeConvID(conversationID string) string {
	id := strings.TrimSpace(conversationID)
	if id == "" {
		return "default"
	}
	return id
}

// RelDisplayPath formats resolvedPath relative to root when inside root, else slash-normalized resolvedPath.
func RelDisplayPath(root, resolvedPath string) string {
	if root != "" {
		if rel, err := filepath.Rel(filepath.FromSlash(CanonicalizePath("", root)), filepath.FromSlash(resolvedPath)); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(resolvedPath)
}
