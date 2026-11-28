package search

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/trail"
	"github.com/kesokaj/fyr/internal/vet"
)

const (
	preInvocationSentinel  = "__pre_invocation__"
	maxCheckpointScanBytes = 512 << 10
)

// StripSandboxPreamble removes the fyr eval / fyr work print-mode preamble
// when present at the start of prompt so anchor checks and prefix gates see
// the operator's actual prompt.
func StripSandboxPreamble(prompt string) string {
	return vet.StripSandboxPreamble(prompt)
}

// hasAutonomousWaiver reports whether lowPrompt explicitly forbids interactive
// questions in unattended / eval sessions.
func hasAutonomousWaiver(lowPrompt string) bool {
	return vet.HasAutonomousWaiver(lowPrompt)
}

func checkTurnOrMidTurnWithStore(ctx context.Context, st *store, in HookInput, rawPrompt string) (SessionState, bool, error) {
	if err := ctx.Err(); err != nil {
		return SessionState{}, false, err
	}
	conversationID := strings.TrimSpace(in.ConversationID)
	if conversationID == "" {
		conversationID = "default"
	}
	promptSum := sha256PromptHex(rawPrompt)
	var genID, lastBytes, turnCount, estTokens, lastInvNum, lastCPOff int
	err := st.db.QueryRowContext(ctx,
		`SELECT generation_id, last_transcript_bytes, turn_count, est_tokens, COALESCE(last_invocation_num, 0), COALESCE(last_checkpoint_offset, 0) FROM session_meta WHERE conversation_id = ?`,
		conversationID,
	).Scan(&genID, &lastBytes, &turnCount, &estTokens, &lastInvNum, &lastCPOff)
	if errors.Is(err, sql.ErrNoRows) {
		return initSessionMetaTurn(ctx, st, conversationID, in, promptSum)
	}
	if err != nil {
		return SessionState{}, false, err
	}
	return advanceOrSkipTurn(ctx, st, conversationID, genID, lastBytes, turnCount, estTokens, lastInvNum, lastCPOff, in, promptSum)
}

func initSessionMetaTurn(ctx context.Context, st *store, conversationID string, in HookInput, promptSum string) (SessionState, bool, error) {
	curBytes64, cpOff, _, _ := scanTranscriptCheckpoint(in.TranscriptPath, 0)
	curBytes := int(curBytes64)
	cpOffset := int(cpOff)
	est := calcEstTokens(curBytes, cpOffset)
	_, err := st.db.ExecContext(ctx,
		`INSERT INTO session_meta(conversation_id, generation_id, last_transcript_bytes, turn_count, est_tokens, last_invocation_num, last_checkpoint_offset) VALUES (?, 0, ?, 1, ?, ?, ?)
		 ON CONFLICT(conversation_id) DO UPDATE SET last_transcript_bytes = excluded.last_transcript_bytes, turn_count = MAX(session_meta.turn_count, 1), est_tokens = ?, last_invocation_num = excluded.last_invocation_num, last_checkpoint_offset = excluded.last_checkpoint_offset`,
		conversationID, curBytes, est, in.InvocationNum, cpOffset, est,
	)
	if err == nil {
		upsertPreInvocationStampCtx(ctx, st, conversationID, 0, 1, promptSum)
	}
	return SessionState{ConversationID: conversationID, GenerationID: 0, LastTranscriptBytes: curBytes, TurnCount: 1, EstTokens: est}, false, err
}

func isMidTurnContinuation(in HookInput) bool {
	return in.ContinuationActive || in.ExecutionNum > 0 || (in.StepIdx > 0 && in.StepIdx > in.InitialNumSteps)
}

func hasNewTurnCue(in HookInput) bool {
	return !in.ContinuationActive && in.ExecutionNum == 0 && in.InitialNumSteps > 0 && in.StepIdx == in.InitialNumSteps
}

func calcEstTokens(curBytes, checkpointOffset int) int {
	delta := curBytes - checkpointOffset
	if delta < 0 {
		delta = curBytes
	}
	if delta < 0 {
		return 0
	}
	return delta / 4
}

func advanceOrSkipTurn(ctx context.Context, st *store, conversationID string, genID, lastBytes, turnCount, est, lastInvNum, lastCPOff int, in HookInput, promptSum string) (SessionState, bool, error) {
	curBytes64, newCPOff, cps, hasSize := scanTranscriptCheckpoint(in.TranscriptPath, int64(lastBytes))
	curBytes := int(curBytes64)
	truncated := hasSize && lastBytes > 0 && curBytes < lastBytes*80/100
	compacted := truncated || (hasSize && cps > 0)
	cpOffset := resolveCheckpointOffset(lastCPOff, int(newCPOff), curBytes, truncated, cps > 0)

	var prevSum string
	_ = st.db.QueryRowContext(ctx, `SELECT sha256 FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND start_line = -3 LIMIT 1`,
		conversationID, genID, preInvocationSentinel).Scan(&prevSum)
	if err := ctx.Err(); err != nil {
		return SessionState{}, false, err
	}
	midTurn := isMidTurnContinuation(in) || (!hasNewTurnCue(in) && in.InvocationNum > 0 && in.InvocationNum > lastInvNum)
	if !compacted && promptSum != "" && prevSum == promptSum && midTurn {
		if hasSize {
			lastBytes, est = curBytes, calcEstTokens(curBytes, cpOffset)
			_, _ = st.db.ExecContext(ctx,
				`UPDATE session_meta SET last_transcript_bytes = ?, est_tokens = ?, last_invocation_num = ?, last_checkpoint_offset = ? WHERE conversation_id = ?`,
				lastBytes, est, in.InvocationNum, cpOffset, conversationID)
		} else {
			_, _ = st.db.ExecContext(ctx, `UPDATE session_meta SET last_invocation_num = ? WHERE conversation_id = ?`, in.InvocationNum, conversationID)
		}
		return SessionState{ConversationID: conversationID, GenerationID: genID, LastTranscriptBytes: lastBytes, TurnCount: max(1, turnCount), EstTokens: est}, true, nil
	}
	if compacted {
		genID++
	}
	if compacted || hasSize {
		lastBytes, est = curBytes, calcEstTokens(curBytes, cpOffset)
	}
	turnCount++
	_, err := st.db.ExecContext(ctx,
		`UPDATE session_meta SET generation_id = ?, last_transcript_bytes = ?, turn_count = ?, est_tokens = ?, last_invocation_num = ?, last_checkpoint_offset = ? WHERE conversation_id = ?`,
		genID, lastBytes, turnCount, est, in.InvocationNum, cpOffset, conversationID)
	if err == nil {
		upsertPreInvocationStampCtx(ctx, st, conversationID, genID, turnCount, promptSum)
	}
	return SessionState{ConversationID: conversationID, GenerationID: genID, LastTranscriptBytes: lastBytes, TurnCount: turnCount, EstTokens: est, Compacted: compacted}, false, err
}

func resolveCheckpointOffset(prevOff, newOff, curBytes int, truncated, sawCheckpoint bool) int {
	if truncated {
		if sawCheckpoint {
			return newOff
		}
		return 0
	}
	if sawCheckpoint {
		return newOff
	}
	if curBytes < prevOff {
		return 0
	}
	return prevOff
}

// scanTranscriptCheckpoint safely inspects regular transcript JSONL files from
// fromOffset (capped at 512 KiB/call, tail-scanning on larger jumps) and returns
// the current byte length, offset immediately after the last CHECKPOINT record,
// checkpoint count, and whether path was a readable regular file.
func scanTranscriptCheckpoint(path string, fromOffset int64) (curBytes, lastCheckpointOffset int64, checkpoints int, ok bool) {
	p := strings.TrimSpace(path)
	if p == "" {
		return 0, 0, 0, false
	}
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return 0, 0, 0, false
	}
	curBytes = fi.Size()
	f, err := trail.OpenRegular(p)
	if err != nil {
		return 0, 0, 0, false
	}
	defer f.Close()
	if fromOffset < 0 || fromOffset > curBytes {
		fromOffset = 0
	}
	if fromOffset == curBytes {
		return curBytes, 0, 0, true
	}
	scanStart := fromOffset
	if curBytes-fromOffset > maxCheckpointScanBytes {
		scanStart = curBytes - maxCheckpointScanBytes
	}
	skipFirst := false
	if scanStart > 0 {
		if _, err := f.Seek(scanStart-1, io.SeekStart); err != nil {
			return curBytes, 0, 0, true
		}
		var prev [1]byte
		if _, err := io.ReadFull(f, prev[:]); err != nil {
			return curBytes, 0, 0, true
		}
		skipFirst = prev[0] != '\n'
	}
	buf, err := io.ReadAll(io.LimitReader(f, maxCheckpointScanBytes))
	if err != nil {
		return curBytes, 0, 0, true
	}
	lastCheckpointOffset, checkpoints = scanCheckpointLines(buf, scanStart, skipFirst)
	return curBytes, lastCheckpointOffset, checkpoints, true
}

func scanCheckpointLines(buf []byte, baseOffset int64, skipFirst bool) (int64, int) {
	var lastCP int64
	checkpoints := 0
	pos := 0
	if skipFirst {
		nl := bytes.IndexByte(buf, '\n')
		if nl < 0 {
			return 0, 0
		}
		pos = nl + 1
	}
	for pos < len(buf) {
		nl := bytes.IndexByte(buf[pos:], '\n')
		var line []byte
		var nextPos int
		if nl >= 0 {
			line = buf[pos : pos+nl]
			nextPos = pos + nl + 1
		} else {
			line = buf[pos:]
			nextPos = len(buf)
		}
		if isCheckpointJSONLLine(line) {
			checkpoints++
			lastCP = baseOffset + int64(nextPos)
		}
		pos = nextPos
	}
	return lastCP, checkpoints
}

func isCheckpointJSONLLine(line []byte) bool {
	if !bytes.Contains(line, []byte("CHECKPOINT")) {
		return false
	}
	var rec struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(bytes.TrimSpace(line), &rec) == nil && rec.Type == "CHECKPOINT"
}

func statTranscriptBytes(transcriptPath string) (int, bool) {
	p := strings.TrimSpace(transcriptPath)
	if p == "" {
		return 0, false
	}
	if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() {
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
			`INSERT INTO session_meta(conversation_id, generation_id, last_transcript_bytes, turn_count, est_tokens, last_invocation_num) VALUES (?, 0, ?, 1, ?, ?)
			 ON CONFLICT(conversation_id) DO UPDATE SET turn_count = MAX(session_meta.turn_count, 1), last_invocation_num = excluded.last_invocation_num`,
			convID, curBytes, est, in.InvocationNum,
		)
	} else if err == nil {
		if turnCount <= 0 {
			turnCount = 1
		}
		if hasSize {
			_, _ = st.db.ExecContext(ctx, `UPDATE session_meta SET last_transcript_bytes = ?, est_tokens = ?, turn_count = ?, last_invocation_num = ? WHERE conversation_id = ?`, curBytes, est, turnCount, in.InvocationNum, convID)
		} else {
			_, _ = st.db.ExecContext(ctx, `UPDATE session_meta SET turn_count = ?, last_invocation_num = ? WHERE conversation_id = ?`, turnCount, in.InvocationNum, convID)
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
