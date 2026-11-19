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
		_, err = st.db.ExecContext(ctx, `INSERT INTO session_meta(conversation_id, generation_id, last_transcript_bytes, turn_count, est_tokens) VALUES (?, 0, ?, 1, ?)`, conversationID, curBytes, est)
		if err == nil {
			if err = ctx.Err(); err == nil {
				upsertPreInvocationStamp(st, conversationID, 0, 1, promptSum)
			}
		}
		return SessionState{ConversationID: conversationID, GenerationID: 0, LastTranscriptBytes: curBytes, TurnCount: 1, EstTokens: est}, false, err
	}
	if err != nil {
		return SessionState{}, false, err
	}
	return advanceOrSkipTurn(ctx, st, conversationID, genID, lastBytes, turnCount, estTokens, curBytes, hasSize, in, promptSum)
}

func advanceOrSkipTurn(ctx context.Context, st *store, conversationID string, genID, lastBytes, turnCount, estTokens, curBytes int, hasSize bool, in HookInput, promptSum string) (SessionState, bool, error) {
	compacted := hasSize && lastBytes > 0 && curBytes < lastBytes*80/100
	var prevSum string
	_ = st.db.QueryRowContext(ctx, `SELECT sha256 FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND start_line = -3 LIMIT 1`,
		conversationID, genID, preInvocationSentinel).Scan(&prevSum)
	if err := ctx.Err(); err != nil {
		return SessionState{}, false, err
	}
	midTurn := in.ContinuationActive || (in.InvocationNum >= 1 && (in.ExecutionNum > 0 || in.StepIdx > in.InitialNumSteps))
	if !compacted && promptSum != "" && prevSum == promptSum && midTurn {
		if hasSize {
			lastBytes, estTokens = curBytes, curBytes/4
			_, _ = st.db.ExecContext(ctx, `UPDATE session_meta SET last_transcript_bytes = ?, est_tokens = ? WHERE conversation_id = ?`, lastBytes, estTokens, conversationID)
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
	_, err := st.db.ExecContext(ctx, `UPDATE session_meta SET generation_id = ?, last_transcript_bytes = ?, turn_count = ?, est_tokens = ? WHERE conversation_id = ?`, genID, lastBytes, turnCount, estTokens, conversationID)
	if err == nil {
		if err = ctx.Err(); err == nil {
			upsertPreInvocationStamp(st, conversationID, genID, turnCount, promptSum)
		}
	}
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
	return trail.CheckScratchNoSymlinks(root)
}
