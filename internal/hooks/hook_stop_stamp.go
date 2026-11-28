package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/hooks/guard"
	"github.com/kesokaj/fyr/internal/hooks/telemetry"
	"github.com/kesokaj/fyr/internal/readledger"
	"github.com/kesokaj/fyr/internal/trail"
)

const (
	// VerifyStampRelPath is the workspace-relative path of the verification stamp.
	VerifyStampRelPath  = trail.VerifyStampRelPath
	stopNudgeStampRel   = ".agents/scratch/stop-nudge.stamp"
	maxVerifyStampBytes = 4096
)

type stopNudgeDoc struct {
	ConversationID string `json:"conversationId"`
	TreeSHA256     string `json:"treeSha256"`
	UpdatedAt      string `json:"updatedAt"`
}

func evaluateStopStamp(d Deps, home, dir string, in HookInput) (bool, string) {
	if HooksDisabledForHome(home, dir) || in.ContinuationActive || in.ExecutionNum > 0 {
		return false, ""
	}
	if strings.TrimSpace(in.Error) != "" || !isNormalStopTermination(in.TerminationReason) {
		return false, ""
	}
	root, _, ok := guard.FindGitRootFast(dir)
	if !ok || HooksDisabled(root) || trail.CheckScratchNoSymlinks(root) != nil {
		return false, ""
	}
	state, err := guard.ComputeGoTreeState(root, ReadGitBranchReftable)
	if err != nil || len(state.DirtyGo) == 0 || verifyStampMatchesTree(root, state.TreeSHA256) {
		return false, ""
	}
	convID := strings.TrimSpace(in.ConversationID)
	if convID == "" {
		convID = "_default"
	}
	if !sessionTouchedDirtyGo(d, root, convID, state.DirtyGo) || !markStopNudgeOnce(d, root, convID, state.TreeSHA256) {
		return false, ""
	}
	return true, formatVerifyStampReason(state.DirtyGo, state.TreeSHA256)
}

func verifyStampMatchesTree(root, treeSHA string) bool {
	st, err := trail.ReadVerifyStamp(root)
	if err != nil {
		return false
	}
	sum := strings.TrimSpace(st.TreeSHA256)
	if sum == "" {
		sum = strings.TrimSpace(st.TreeSHA256Sn)
	}
	return sum != "" && strings.EqualFold(sum, treeSHA)
}

func formatVerifyStampReason(dirtyGo []string, treeSHA string) string {
	sample := telemetry.CleanPrintableSlice(dirtyGo, 80)
	extra := ""
	if len(sample) > 3 {
		extra = fmt.Sprintf(" (+%d more)", len(sample)-3)
		sample = sample[:3]
	}
	shortHash := treeSHA
	if len(shortHash) > 12 {
		shortHash = shortHash[:12]
	}
	return fmt.Sprintf("[VERIFY_STAMP] dirty Go files (%s%s) lack a matching %s for tree %s; ensure go vet ./... && go test -race ./... pass (or commit verified changes) before finishing.",
		strings.Join(sample, ", "), extra, VerifyStampRelPath, shortHash)
}

func sessionTouchedDirtyGo(d Deps, root, convID string, dirtyGo []string) bool {
	if d.ProjectDB == nil || d.OpenLedgerStore == nil {
		return true
	}
	if fi, err := os.Lstat(d.ProjectDB(root)); err != nil || !fi.Mode().IsRegular() {
		return true
	}
	st, _, err := d.OpenLedgerStore(root)
	if err != nil || st == nil {
		return true
	}
	defer st.Close()
	var total int
	if err := st.SQLDB.QueryRow(`SELECT COUNT(*) FROM read_ledger`).Scan(&total); err != nil || total == 0 {
		return true
	}
	dirty := make(map[string]bool, len(dirtyGo))
	for _, rel := range dirtyGo {
		dirty[rel] = true
	}
	rows, err := st.SQLDB.Query(`SELECT resolved_path, start_line, sha256 FROM read_ledger WHERE conversation_id = ?`, convID)
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
		if ledgerRowTouchedDirty(d, root, resolved, sha, startLine, dirty) {
			return true
		}
	}
	return false
}

func ledgerRowTouchedDirty(d Deps, root, resolved, sha string, startLine int, dirty map[string]bool) bool {
	rel := d.RelPathInRepo(root, resolved)
	if !dirty[rel] {
		return false
	}
	if startLine == -1 || (startLine == 0 && sha == "") {
		return true
	}
	if sha == "" {
		return false
	}
	cur, _, _, _ := readledger.ReadFileDigestAndLines(filepath.Join(root, filepath.FromSlash(rel)))
	return cur != "" && cur != sha
}

func isNormalStopTermination(reason string) bool {
	trimmed := strings.ToUpper(strings.TrimSpace(reason))
	trimmed = strings.TrimPrefix(trimmed, "EXECUTOR_TERMINATION_REASON_")
	return trimmed == "" || trimmed == "NO_TOOL_CALL"
}

func markStopNudgeInStore(d Deps, root, convID, treeSHA string) (marked, handled bool) {
	if d.ProjectDB == nil || d.OpenLedgerStore == nil {
		return false, false
	}
	fi, err := os.Lstat(d.ProjectDB(root))
	if err != nil || !fi.Mode().IsRegular() {
		return false, false
	}
	st, _, err := d.OpenLedgerStore(root)
	if err != nil || st == nil {
		return false, false
	}
	defer st.Close()
	genID, turnCount := readledger.CurrentGeneration(st.SQLDB, convID)
	short := strings.ToLower(strings.TrimSpace(treeSHA))
	if len(short) > 16 {
		short = short[:16]
	}
	return MarkSessionSentinelOnce(st.SQLDB, convID, genID, turnCount, "stop_verify:"+short), true
}

func markStopNudgeOnce(d Deps, root, convID, treeSHA string) bool {
	if marked, handled := markStopNudgeInStore(d, root, convID, treeSHA); handled {
		return marked
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
		_ = WriteScratchStampAtomic(nudgePath, append(payload, '\n'))
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

// WriteScratchStampAtomic writes data to target via temp-file + chmod 0600 + rename, refusing symlinks.
func WriteScratchStampAtomic(target string, data []byte) error {
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
