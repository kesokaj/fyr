package search

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const coActivityLedgerTrigger = `CREATE TRIGGER IF NOT EXISTS trg_co_activity_mod
AFTER UPDATE ON read_ledger
WHEN OLD.start_line = 0 AND OLD.end_line = 0 AND (OLD.sha256 = '' OR (NEW.sha256 != '' AND OLD.sha256 != NEW.sha256))
BEGIN
	INSERT OR IGNORE INTO read_ledger(conversation_id, generation_id, resolved_path, start_line, end_line, sha256, turn_num, hit_count, updated_at)
	VALUES (NEW.conversation_id, NEW.generation_id, NEW.resolved_path, -1, -1, '', NEW.turn_num, 1, NEW.updated_at);
END;`

func ensureCoActivityTrigger(st *store) error {
	_, err := st.db.Exec(coActivityLedgerTrigger)
	return err
}

func markPreWriteInLedger(dir, conversationID, resolvedPath string) error {
	if resolvedPath == "" {
		return nil
	}
	st, _, err := openLedgerStore(dir)
	if err != nil {
		return err
	}
	defer st.close()
	if err := ensureCoActivityTrigger(st); err != nil {
		return err
	}
	if conversationID == "" {
		conversationID = "default"
	}
	genID, turnNum := st.currentGeneration(conversationID)
	_, err = st.db.Exec(`INSERT OR IGNORE INTO read_ledger(conversation_id, generation_id, resolved_path, start_line, end_line, sha256, turn_num, hit_count, updated_at)
		VALUES (?, ?, ?, -1, -1, '', ?, 1, ?)`, conversationID, genID, resolvedPath, turnNum, time.Now().Unix())
	return err
}

// RecordStopCoActivity inspects the session's read_ledger on the Stop hook and
// increments co_read and co_modified pairs in co_activity at most once per conversation.
func RecordStopCoActivity(dir, conversationID string) error {
	st, root, err := openLedgerStore(dir)
	if err != nil {
		return err
	}
	defer st.close()
	if err := ensureCoActivityTrigger(st); err != nil {
		return err
	}
	if conversationID == "" {
		conversationID = "default"
	}
	reads, mods, err := collectSessionCoActivity(st, root, conversationID)
	if err != nil {
		return err
	}
	if len(reads) >= 2 {
		if err := upsertCoActivityPairs(st, conversationID, "co_read", reads); err != nil {
			return err
		}
	}
	if len(mods) >= 2 {
		if err := upsertCoActivityPairs(st, conversationID, "co_modified", mods); err != nil {
			return err
		}
	}
	return nil
}

type ledgerRow struct {
	rel   string
	start int
	end   int
	sha   string
}

func collectSessionCoActivity(st *store, root, conversationID string) ([]string, []string, error) {
	rows, err := st.db.Query(`SELECT resolved_path, start_line, end_line, sha256 FROM read_ledger WHERE conversation_id = ? AND start_line >= -1`, conversationID)
	if err != nil {
		return nil, nil, err
	}
	var list []ledgerRow
	for rows.Next() {
		var p, sha string
		var start, end int
		if err := rows.Scan(&p, &start, &end, &sha); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if rel := relPathInRepo(root, p); rel != "" {
			list = append(list, ledgerRow{rel: rel, start: start, end: end, sha: sha})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	readSet, modSet := map[string]bool{}, map[string]bool{}
	diskDigest, firstDigest := map[string]string{}, map[string]string{}
	for _, r := range list {
		readSet[r.rel] = true
		if r.sha != "" {
			if prev, ok := firstDigest[r.rel]; ok && prev != r.sha {
				modSet[r.rel] = true
			} else if !ok {
				firstDigest[r.rel] = r.sha
			}
		}
		if isLedgerRowModified(root, r.rel, r.start, r.end, r.sha, diskDigest) {
			modSet[r.rel] = true
		}
	}
	return sortedMapKeys(readSet), sortedMapKeys(modSet), nil
}

func isLedgerRowModified(root, rel string, start, end int, sha string, diskDigest map[string]string) bool {
	if (start == 0 && end == 0 && sha == "") || (start == -1 && end == -1) {
		return true
	}
	if sha == "" {
		return false
	}
	cur, ok := diskDigest[rel]
	if !ok {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		cur, _, _, _ = readFileDigestAndLines(abs)
		diskDigest[rel] = cur
	}
	return cur != "" && cur != sha
}

const maxCoActivityFiles = 30

func upsertCoActivityPairs(st *store, conversationID, kind string, files []string) error {
	if len(files) > maxCoActivityFiles {
		return nil
	}
	now := time.Now().Unix()
	tx, err := st.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	seenStmt, err := tx.Prepare(`INSERT OR IGNORE INTO read_ledger(conversation_id, generation_id, resolved_path, start_line, end_line, sha256, turn_num, hit_count, updated_at)
		VALUES (?, -1, ?, -2, -2, '', 0, 1, ?)`)
	if err != nil {
		return err
	}
	defer seenStmt.Close()
	upStmt, err := tx.Prepare(`INSERT INTO co_activity(file_a, file_b, kind, count, updated_at)
		VALUES(?, ?, ?, 1, ?)
		ON CONFLICT(file_a, file_b, kind) DO UPDATE SET count = co_activity.count + 1, updated_at = excluded.updated_at`)
	if err != nil {
		return err
	}
	defer upStmt.Close()
	for i := 0; i < len(files); i++ {
		for j := i + 1; j < len(files); j++ {
			a, b := files[i], files[j]
			if a > b {
				a, b = b, a
			}
			if a == b {
				continue
			}
			res, err := seenStmt.Exec(conversationID, kind+":"+a+"\x00"+b, now)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err != nil || n == 0 {
				continue
			}
			if _, err := upStmt.Exec(a, b, kind, now); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func relPathInRepo(root, p string) string {
	p = strings.TrimSpace(p)
	if p == "" || strings.Contains(p, "\x00") {
		return ""
	}
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(root, p)
		if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
			return ""
		}
		return filepath.ToSlash(rel)
	}
	clean := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(p)), "./")
	if clean == "." || clean == "" || strings.HasPrefix(clean, "..") {
		return ""
	}
	return clean
}

func sortedMapKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
