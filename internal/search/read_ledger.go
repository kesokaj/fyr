package search

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/readledger"
)

// SessionState holds the compaction generation and transcript metrics for a conversation.
type SessionState = readledger.SessionState

// CapsulePivot represents a file range pre-seeded into the session read ledger by a capsule.
type CapsulePivot = readledger.CapsulePivot

// ReadDecision reports whether a file read or write is allowed by the session read ledger.
type ReadDecision = readledger.ReadDecision

type resolvedCapsulePivot struct {
	resolved, sumHex string
	start, end       int
}

type storeLedgerAdapter struct{ *store }

func (a storeLedgerAdapter) DB() *sql.DB                           { return a.db }
func (a storeLedgerAdapter) WithImmediateTx(fn func() error) error { return a.withImmediateTx(fn) }
func (a storeLedgerAdapter) Close()                                { _ = a.close() }

func ledgerHooks() readledger.Hooks {
	return readledger.Hooks{
		OpenSessionStore: func(sessionDir, home string) (readledger.DBStore, string, error) {
			st, root, err := openSessionLedgerStore(sessionDir, home)
			if err != nil {
				return nil, "", err
			}
			return storeLedgerAdapter{st}, root, nil
		},
		ResolveLedgerDir: resolveSessionLedgerDir,
		SkeletonSummary:  fileSkeletonSummary,
		FormatUnchanged:  formatUnchangedLedgerNotice,
		FormatModified:   formatModifiedLedgerNotice,
		Now:              func() time.Time { return nowFunc() },
	}
}

// CanonicalizePath resolves targetPath against cwd and evaluates symlinks so git worktrees never collide.
func CanonicalizePath(cwd, targetPath string) string {
	return readledger.CanonicalizePath(cwd, targetPath)
}

// ParseCapsulePivots extracts Hop-0 and full-body Hop-1 file ranges from a rendered slice capsule.
func ParseCapsulePivots(capsule string) []CapsulePivot {
	return readledger.ParseCapsulePivots(capsule)
}

// RegisterCapsulePivots records Hop-0/Hop-1 capsule ranges in read_ledger for conversationID.
func RegisterCapsulePivots(dir, conversationID string, turnNum int, pivots []CapsulePivot) error {
	return RegisterCapsulePivotsWithHome(dir, "", conversationID, turnNum, pivots)
}

// RegisterCapsulePivotsWithHome records Hop-0/Hop-1 capsule ranges in read_ledger,
// routing through openSessionLedgerStore so auto-ignore and globalDB fallback apply.
func RegisterCapsulePivotsWithHome(dir, home, conversationID string, turnNum int, pivots []CapsulePivot) error {
	return readledger.RegisterCapsulePivots(ledgerHooks(), dir, home, conversationID, turnNum, pivots)
}

// CheckAndRecordRead checks whether a file range was already read unchanged in this generation,
// returning [UNCHANGED] + symbol skeleton while keeping the persistent hash-keyed stamp active.
func CheckAndRecordRead(dir, conversationID, targetPath string, startLine, endLine, turnNum int) (ReadDecision, error) {
	return CheckAndRecordReadInSession(dir, "", dir, conversationID, targetPath, startLine, endLine, turnNum)
}

// CheckAndRecordReadInSession records or deduplicates a read in the session's ledger store
// while canonicalizing targetPath relative to toolDir (supporting external checkouts).
func CheckAndRecordReadInSession(sessionDir, home, toolDir, conversationID, targetPath string, startLine, endLine, turnNum int) (ReadDecision, error) {
	return readledger.CheckAndRecordReadInSession(ledgerHooks(), sessionDir, home, toolDir, conversationID, targetPath, startLine, endLine, turnNum)
}

// CheckPreWriteHashWithEdit verifies disk content before a write/edit and invalidates
// only stamps overlapping the edited line range (recording any predicted post-edit hash as pending).
func CheckPreWriteHashWithEdit(sessionDir, home, toolDir, conversationID, targetPath string, args map[string]any) (ReadDecision, error) {
	return readledger.CheckPreWriteHashWithEdit(ledgerHooks(), sessionDir, home, toolDir, conversationID, targetPath, args)
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

func (s *store) currentGeneration(conversationID string) (int, int) {
	return readledger.CurrentGeneration(s.db, conversationID)
}

func (s *store) upsertReadStamp(conversationID string, genID int, resolved string, start, end int, sumHex string, turnNum int, now int64) error {
	return readledger.UpsertStamp(s.db, conversationID, genID, resolved, start, end, sumHex, turnNum, now)
}

func (s *store) upsertCapsulePivotsTx(conversationID string, turnNum int, pivots []resolvedCapsulePivot) error {
	conv := make([]readledger.ResolvedCapsulePivot, len(pivots))
	for i, p := range pivots {
		conv[i] = readledger.ResolvedCapsulePivot{Resolved: p.resolved, SumHex: p.sumHex, Start: p.start, End: p.end}
	}
	return readledger.UpsertCapsulePivotsTx(storeLedgerAdapter{s}, nowFunc, conversationID, turnNum, conv)
}

func (s *store) checkAndRecordResolvedRead(root, conversationID, resolved string, startLine, endLine, turnNum int) (ReadDecision, error) {
	return readledger.CheckAndRecordResolvedRead(storeLedgerAdapter{s}, ledgerHooks(), root, conversationID, resolved, startLine, endLine, turnNum)
}

func fileSkeletonSummary(_, resolvedPath, text string) string {
	syms, _ := extractSymbolsAndRefsForIndex(resolvedPath, text)
	lines := make([]string, 0, len(syms))
	for _, sy := range syms {
		lines = append(lines, fmt.Sprintf("%d-%d  %s", sy.StartLine, sy.EndLine, sy.Sig))
	}
	return strings.Join(lines, "\n")
}

func readFileDigestAndLines(resolvedPath string) (string, []byte, int, bool) {
	return readledger.ReadFileDigestAndLines(resolvedPath)
}

func normalizeLineRange(startLine, endLine, totalLines int) (int, int) {
	return readledger.NormalizeLineRange(startLine, endLine, totalLines)
}

func normalizeLedgerConvID(conversationID string) string {
	return readledger.NormalizeConvID(conversationID)
}

func relDisplayPath(root, resolvedPath string) string {
	return readledger.RelDisplayPath(root, resolvedPath)
}
