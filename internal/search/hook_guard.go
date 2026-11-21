package search

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
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
		"without re-asking",
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
	return checkTurnOrMidTurnInput(dir, HookInput{
		ConversationID: conversationID,
		TranscriptPath: transcriptPath,
		InvocationNum:  invocationNum,
	}, rawPrompt)
}

func checkTurnOrMidTurnInput(dir string, in HookInput, rawPrompt string) (SessionState, bool, error) {
	st, _, err := openLedgerStore(dir)
	if err != nil {
		return SessionState{}, false, err
	}
	defer st.close()
	return checkTurnOrMidTurnWithStore(context.Background(), st, in, rawPrompt)
}

func checkTurnOrMidTurnWithStore(ctx context.Context, st *store, in HookInput, rawPrompt string) (SessionState, bool, error) {
	if err := ctx.Err(); err != nil {
		return SessionState{}, false, err
	}
	conversationID := strings.TrimSpace(in.ConversationID)
	if conversationID == "" {
		conversationID = "default"
	}
	curBytes, hasSize := statTranscriptBytes(in.TranscriptPath)
	promptSum := sha256PromptHex(rawPrompt)
	var genID, lastBytes, turnCount, estTokens int
	err := st.db.QueryRowContext(ctx, `SELECT generation_id, last_transcript_bytes, turn_count, est_tokens FROM session_meta WHERE conversation_id = ?`, conversationID).
		Scan(&genID, &lastBytes, &turnCount, &estTokens)
	if errors.Is(err, sql.ErrNoRows) {
		est := curBytes / 4
		_, err = st.db.ExecContext(ctx,
			`INSERT INTO session_meta(conversation_id, generation_id, last_transcript_bytes, turn_count, est_tokens) VALUES (?, 0, ?, 1, ?)
			 ON CONFLICT(conversation_id) DO UPDATE SET last_transcript_bytes = excluded.last_transcript_bytes, turn_count = MAX(session_meta.turn_count, 1), est_tokens
			 = excluded.est_tokens`,
			conversationID, curBytes, est,
		)
		if err == nil {
			upsertPreInvocationStampCtx(ctx, st, conversationID, 0, 1, promptSum)
		}
		return SessionState{ConversationID: conversationID, GenerationID: 0, LastTranscriptBytes: curBytes, TurnCount: 1, EstTokens: est}, false, err
	}
	if err != nil {
		return SessionState{}, false, err
	}
	return advanceOrSkipTurn(ctx, st, conversationID, genID, lastBytes, turnCount, estTokens, curBytes, hasSize, in, promptSum)
}

func isMidTurnContinuation(in HookInput) bool {
	return in.ContinuationActive || in.ExecutionNum > 0 || (in.StepIdx > 0 && in.StepIdx > in.InitialNumSteps)
}

func advanceOrSkipTurn(ctx context.Context, st *store, conversationID string, genID, lastBytes, turnCount, est, curBytes int, hasSize bool, in HookInput, promptSum string) (SessionState, bool, error) {
	compacted := hasSize && lastBytes > 0 && curBytes < lastBytes*80/100
	var prevSum string
	_ = st.db.QueryRowContext(ctx, `SELECT sha256 FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND start_line = -3 LIMIT 1`,
		conversationID, genID, preInvocationSentinel).Scan(&prevSum)
	if err := ctx.Err(); err != nil {
		return SessionState{}, false, err
	}
	midTurn := isMidTurnContinuation(in)
	if !compacted && promptSum != "" && prevSum == promptSum && midTurn {
		if hasSize {
			lastBytes, est = curBytes, curBytes/4
			_, _ = st.db.ExecContext(ctx, `UPDATE session_meta SET last_transcript_bytes = ?, est_tokens = ? WHERE conversation_id = ?`, lastBytes, est, conversationID)
		}
		return SessionState{ConversationID: conversationID, GenerationID: genID, LastTranscriptBytes: lastBytes, TurnCount: max(1, turnCount), EstTokens: est}, true, nil
	}
	if compacted {
		genID++
	}
	if compacted || hasSize {
		lastBytes, est = curBytes, curBytes/4
	}
	turnCount++
	_, err := st.db.ExecContext(ctx, `UPDATE session_meta SET generation_id = ?, last_transcript_bytes = ?, turn_count = ?, est_tokens = ? WHERE conversation_id = ?`, genID, lastBytes, turnCount, est, conversationID)
	if err == nil {
		upsertPreInvocationStampCtx(ctx, st, conversationID, genID, turnCount, promptSum)
	}
	return SessionState{ConversationID: conversationID, GenerationID: genID, LastTranscriptBytes: lastBytes, TurnCount: turnCount, EstTokens: est, Compacted: compacted}, false, err
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
	upsertPreInvocationStampCtx(context.Background(), st, conversationID, genID, turnCount, promptSum)
}

func upsertPreInvocationStampCtx(ctx context.Context, st *store, conversationID string, genID, turnCount int, promptSum string) {
	if st == nil || st.db == nil || promptSum == "" {
		return
	}
	if ctx == nil || ctx.Err() != nil {
		return
	}
	_, _ = st.db.ExecContext(ctx,
		`INSERT INTO read_ledger(conversation_id, generation_id, resolved_path, start_line, end_line, sha256, turn_num, hit_count, updated_at)
		 VALUES (?, ?, ?, -3, -3, ?, ?, 1, ?)
		 ON CONFLICT(conversation_id, generation_id, resolved_path, start_line, end_line)
		 DO UPDATE SET sha256 = excluded.sha256, turn_num = excluded.turn_num, updated_at = excluded.updated_at`,
		normalizeLedgerConvID(conversationID), genID, preInvocationSentinel, promptSum, max(1, turnCount), nowFunc().Unix(),
	)
}

// stampPreInvocationFallbackCtx ensures session_meta and the __pre_invocation__
// prompt-hash stamp exist when PreInvocation times out before stamping, breaking
// mid-turn repeat timeout spirals (ADR-532).
func stampPreInvocationFallbackCtx(ctx context.Context, st *store, convID string, in HookInput, rawPrompt string) int {
	if st == nil || st.db == nil || ctx.Err() != nil {
		return 1
	}
	convID = normalizeLedgerConvID(convID)
	curBytes, hasSize := statTranscriptBytes(in.TranscriptPath)
	est := curBytes / 4
	var genID, turnCount int
	err := st.db.QueryRowContext(ctx, `SELECT generation_id, turn_count FROM session_meta WHERE conversation_id = ?`, convID).Scan(&genID, &turnCount)
	if errors.Is(err, sql.ErrNoRows) {
		turnCount = 1
		_, _ = st.db.ExecContext(ctx,
			`INSERT INTO session_meta(conversation_id, generation_id, last_transcript_bytes, turn_count, est_tokens) VALUES (?, 0, ?, 1, ?)
			 ON CONFLICT(conversation_id) DO UPDATE SET turn_count = MAX(session_meta.turn_count, 1)`,
			convID, curBytes, est,
		)
	} else if err == nil {
		if turnCount <= 0 {
			turnCount = 1
		}
		if hasSize {
			_, _ = st.db.ExecContext(ctx, `UPDATE session_meta SET last_transcript_bytes = ?, est_tokens = ?, turn_count = ? WHERE conversation_id = ?`, curBytes, est, turnCount, convID)
		} else {
			_, _ = st.db.ExecContext(ctx, `UPDATE session_meta SET turn_count = ? WHERE conversation_id = ?`, turnCount, convID)
		}
	}
	if promptSum := sha256PromptHex(rawPrompt); promptSum != "" {
		upsertPreInvocationStampCtx(ctx, st, convID, genID, turnCount, promptSum)
	}
	return max(1, turnCount)
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
	return trail.CheckScratchNoSymlinks(root)
}
