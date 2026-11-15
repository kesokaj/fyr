package search

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	verifyStampRelPath  = ".agents/scratch/verify.stamp"
	stopNudgeStampRel   = ".agents/scratch/stop-nudge.stamp"
	maxVerifyStampBytes = 4096
)

type verifyStampDoc struct {
	TreeSHA256   string `json:"treeSha256"`
	TreeSHA256Sn string `json:"tree_sha256,omitempty"`
	UpdatedAt    string `json:"updatedAt"`
}

type stopNudgeDoc struct {
	ConversationID string `json:"conversationId"`
	TreeSHA256     string `json:"treeSha256"`
	UpdatedAt      string `json:"updatedAt"`
}

func verifyStampPath(root string) string {
	return filepath.Join(root, filepath.FromSlash(verifyStampRelPath))
}

func writeVerifyStamp(root, treeSHA256 string) error {
	sum := strings.ToLower(strings.TrimSpace(treeSHA256))
	if strings.TrimSpace(root) == "" || sum == "" {
		return fmt.Errorf("verify stamp requires root and non-empty sha256")
	}
	if err := checkScratchNoSymlinks(root); err != nil {
		return err
	}
	target := verifyStampPath(root)
	payload, err := json.Marshal(verifyStampDoc{
		TreeSHA256: sum,
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	return writeScratchStampAtomic(target, append(payload, '\n'))
}

func readVerifyStamp(root string) (string, bool) {
	if strings.TrimSpace(root) == "" {
		return "", false
	}
	data, ok := readSmallRegularFile(verifyStampPath(root), maxVerifyStampBytes)
	if !ok {
		return "", false
	}
	var doc verifyStampDoc
	if json.Unmarshal(data, &doc) == nil {
		sum := strings.ToLower(strings.TrimSpace(doc.TreeSHA256))
		if sum == "" {
			sum = strings.ToLower(strings.TrimSpace(doc.TreeSHA256Sn))
		}
		if sum != "" {
			return sum, true
		}
	}
	fields := strings.Fields(string(data))
	if len(fields) > 0 && len(fields[0]) == 64 {
		return strings.ToLower(fields[0]), true
	}
	return "", false
}

func evaluateStopStamp(home, dir string, in HookInput) (bool, string) {
	if hooksDisabledForHome(home, dir) || in.ContinuationActive || in.ExecutionNum > 0 {
		return false, ""
	}
	if strings.TrimSpace(in.Error) != "" || !isNormalStopTermination(in.TerminationReason) {
		return false, ""
	}
	root, _, ok := findGitRootFast(dir)
	if !ok || hooksDisabled(root) || checkScratchNoSymlinks(root) != nil {
		return false, ""
	}
	state, err := ComputeGoTreeState(root)
	if err != nil || len(state.DirtyGo) == 0 {
		return false, ""
	}
	if stamp, ok := readVerifyStamp(root); ok && strings.EqualFold(stamp, state.TreeSHA256) {
		return false, ""
	}
	convID := strings.TrimSpace(in.ConversationID)
	if convID == "" {
		convID = "_default"
	}
	if !sessionTouchedDirtyGo(root, convID, state.DirtyGo) {
		return false, ""
	}
	if !markStopNudgeOnce(root, convID, state.TreeSHA256) {
		return false, ""
	}
	sample := cleanPrintableSlice(state.DirtyGo, 80)
	extra := ""
	if len(sample) > 3 {
		extra = fmt.Sprintf(" (+%d more)", len(sample)-3)
		sample = sample[:3]
	}
	shortHash := state.TreeSHA256
	if len(shortHash) > 12 {
		shortHash = shortHash[:12]
	}
	reason := fmt.Sprintf("[VERIFY_STAMP] dirty Go files (%s%s) lack a matching %s for tree %s; ensure go vet ./... && go test -race ./... pass (or commit verified changes) before finishing.",
		strings.Join(sample, ", "), extra, verifyStampRelPath, shortHash)
	return true, reason
}

func sessionTouchedDirtyGo(root, convID string, dirtyGo []string) bool {
	if fi, err := os.Lstat(projectDB(root)); err != nil || !fi.Mode().IsRegular() {
		return true
	}
	st, _, err := openLedgerStore(root)
	if err != nil {
		return true
	}
	defer st.close()
	var total int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM read_ledger`).Scan(&total); err != nil || total == 0 {
		return true
	}
	dirty := make(map[string]bool, len(dirtyGo))
	for _, rel := range dirtyGo {
		dirty[rel] = true
	}
	rows, err := st.db.Query(`SELECT resolved_path, start_line, sha256 FROM read_ledger WHERE conversation_id = ?`, convID)
	if err != nil {
		return true
	}
	defer rows.Close()
	for rows.Next() {
		var resolved, sha string
		var startLine int
		if rows.Scan(&resolved, &startLine, &sha) != nil {
			continue
		}
		rel := relPathInRepo(root, resolved)
		if !dirty[rel] {
			continue
		}
		if startLine == -1 || (startLine == 0 && sha == "") {
			return true
		}
		if sha != "" {
			if cur, _, _, _ := readFileDigestAndLines(filepath.Join(root, filepath.FromSlash(rel))); cur != "" && cur != sha {
				return true
			}
		}
	}
	return false
}

func isNormalStopTermination(reason string) bool {
	trimmed := strings.ToUpper(strings.TrimSpace(reason))
	trimmed = strings.TrimPrefix(trimmed, "EXECUTOR_TERMINATION_REASON_")
	return trimmed == "" || trimmed == "NO_TOOL_CALL"
}

func markStopNudgeOnce(root, convID, treeSHA string) bool {
	if fi, err := os.Lstat(projectDB(root)); err == nil && fi.Mode().IsRegular() {
		if st, _, err := openLedgerStore(root); err == nil {
			defer st.close()
			genID, turnCount := st.currentGeneration(convID)
			short := strings.ToLower(strings.TrimSpace(treeSHA))
			if len(short) > 16 {
				short = short[:16]
			}
			key := "stop_verify:" + short
			return markSessionSentinelOnce(st, convID, genID, turnCount, key)
		}
	}
	nudgePath := filepath.Join(root, filepath.FromSlash(stopNudgeStampRel))
	if data, ok := readSmallRegularFile(nudgePath, maxVerifyStampBytes); ok {
		var prev stopNudgeDoc
		if json.Unmarshal(data, &prev) == nil && prev.ConversationID == convID && strings.EqualFold(prev.TreeSHA256, treeSHA) {
			return false
		}
	}
	payload, err := json.Marshal(stopNudgeDoc{
		ConversationID: convID,
		TreeSHA256:     treeSHA,
		UpdatedAt:      time.Now().UTC().Format(time.RFC3339),
	})
	if err == nil {
		_ = writeScratchStampAtomic(nudgePath, append(payload, '\n'))
	}
	return true
}

func readSmallRegularFile(path string, maxBytes int64) ([]byte, bool) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxBytes {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

func writeScratchStampAtomic(target string, data []byte) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if fi, err := os.Lstat(target); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing symlink stamp %s", target)
	}
	tmp, err := os.CreateTemp(dir, ".stamp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		return err
	}
	cleanup = false
	return nil
}
