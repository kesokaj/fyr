package metrics

import (
	"database/sql"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/search"
)

func (s *Store) copyDevStoreTables(srcPath, tmp, label string, log io.Writer) {
	srcDB, err := search.OpenCopy(filepath.Dir(srcPath), filepath.Base(srcPath), tmp)
	if err != nil {
		noteSecondBrain(log, "%s: %v", srcPath, err)
		return
	}
	defer srcDB.Close()
	var verStr string
	if err := srcDB.QueryRow(`SELECT value FROM meta WHERE key = 'schema'`).Scan(&verStr); err != nil {
		noteSecondBrain(log, "%s: %v", srcPath, err)
		return
	}
	ver, err := strconv.Atoi(verStr)
	if err != nil || ver < 4 || ver > len(migrations) {
		noteSecondBrain(log, "%s: unsupported schema %q", srcPath, verStr)
		return
	}
	if err := s.copyDevTrials(srcDB, label, ver >= 5); err != nil {
		noteSecondBrain(log, "%s trials: %v", srcPath, err)
	}
	if err := s.copyDevSessionsAndSteps(srcDB, label, ver >= 5); err != nil {
		noteSecondBrain(log, "%s sessions: %v", srcPath, err)
	}
	if ver >= 6 {
		if err := s.copyDevWorkWaves(srcDB, label); err != nil {
			noteSecondBrain(log, "%s work_waves: %v", srcPath, err)
		}
	}
}

func (s *Store) copyDevTrials(src *sql.DB, label string, hasStageCols bool) error {
	q := `SELECT run, row_version, kind, cluster, expect, harness, cli, status, cause, started_at, seconds, session_seconds, NULL, NULL FROM trials`
	if hasStageCols {
		q = `SELECT run, row_version, kind, cluster, expect, harness, cli, status, cause, started_at, seconds, session_seconds, setup_seconds, check_seconds FROM trials`
	}
	rows, err := src.Query(q)
	if err != nil {
		return err
	}
	defer rows.Close()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for rows.Next() {
		if err := insertCopiedTrial(tx, rows, label); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

func insertCopiedTrial(tx *sql.Tx, rows *sql.Rows, label string) error {
	var run, harnessVal, status, cause string
	var kind, cluster, expect, cli sql.NullString
	var rowVer, sesSec, setSec, chkSec sql.NullInt64
	var startedAt, seconds int64
	if err := rows.Scan(&run, &rowVer, &kind, &cluster, &expect, &harnessVal, &cli, &status, &cause, &startedAt, &seconds, &sesSec, &setSec, &chkSec); err != nil {
		return err
	}
	destRun := scopedKey(label, run)
	if destRun == "" {
		return nil
	}
	_, err := tx.Exec(
		`INSERT OR IGNORE INTO trials(run, label, row_version, kind, cluster, expect, harness, cli, status, cause, started_at, seconds, session_seconds, setup_seconds, check_seconds) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		destRun, printable(label), rowVer, nullPrintable(kind), nullPrintable(cluster), nullPrintable(expect),
		printable(harnessVal), nullPrintable(cli), printable(status), printable(cause),
		startedAt, seconds, sesSec, setSec, chkSec,
	)
	return err
}

func (s *Store) copyDevSessionsAndSteps(src *sql.DB, label string, hasStepTimes bool) error {
	rows, err := src.Query(`SELECT id, vendor, project_root, parent_id, agent_type, model, outcome, run, started_at, ended_at, cost_usd, duration_ms FROM sessions`)
	if err != nil {
		return err
	}
	defer rows.Close()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for rows.Next() {
		origID, destID, err := insertCopiedSession(tx, rows, label)
		if err != nil {
			return err
		}
		if destID == "" {
			continue
		}
		if err := copyDevSessionSteps(tx, src, origID, destID, hasStepTimes); err != nil {
			return err
		}
		if err := copyDevSessionVerbs(tx, src, origID, destID); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

func insertCopiedSession(tx *sql.Tx, rows *sql.Rows, label string) (string, string, error) {
	var origID string
	var vendor, root, parent, agent, model, outcome, run sql.NullString
	var started, ended, dur sql.NullInt64
	var cost sql.NullFloat64
	if err := rows.Scan(&origID, &vendor, &root, &parent, &agent, &model, &outcome, &run, &started, &ended, &cost, &dur); err != nil {
		return "", "", err
	}
	destID := scopedKey(label, origID)
	if destID == "" {
		return "", "", nil
	}
	_, err := tx.Exec(
		`INSERT OR IGNORE INTO sessions(id, vendor, project_root, parent_id, agent_type, model, label, outcome, run, started_at, ended_at, cost_usd, duration_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		destID, nullPrintable(vendor), nullPrintable(root), scopedNullString(label, parent),
		nullPrintable(agent), nullPrintable(model), printable(label), nullPrintable(outcome),
		scopedNullString(label, run), started, ended, cost, dur,
	)
	return origID, destID, err
}

func copyDevSessionSteps(tx *sql.Tx, src *sql.DB, origSID, destSID string, hasStepTimes bool) error {
	q := `SELECT ext_id, ts, kind, tool, skill, target_kind, is_error, denied, tok_in, tok_out, cache_read, cache_write, cmd_hash, context, NULL, NULL, NULL FROM steps WHERE session_id = ?`
	if hasStepTimes {
		q = `SELECT ext_id, ts, kind, tool, skill, target_kind, is_error, denied, tok_in, tok_out, cache_read, cache_write, cmd_hash, context, started_ms, ended_ms, wait_ms FROM steps WHERE session_id = ?`
	}
	rows, err := src.Query(q, origSID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var extID string
		var kind, tool, skill, targetKind, cmdHash sql.NullString
		var ts, isErr, denied, tokIn, tokOut, cRead, cWrite, ctxTok, sMS, eMS, wMS sql.NullInt64
		if err := rows.Scan(&extID, &ts, &kind, &tool, &skill, &targetKind, &isErr, &denied, &tokIn, &tokOut, &cRead, &cWrite, &cmdHash, &ctxTok, &sMS, &eMS, &wMS); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO steps(session_id, ext_id, ts, kind, tool, skill, target_kind, is_error, denied, tok_in, tok_out, cache_read, cache_write, cmd_hash, context, started_ms, ended_ms, wait_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			destSID, printable(extID), ts, nullPrintable(kind), nullPrintable(tool), nullPrintable(skill),
			nullPrintable(targetKind), isErr, denied, tokIn, tokOut, cRead, cWrite, nullPrintable(cmdHash),
			ctxTok, sMS, eMS, wMS,
		); err != nil {
			return err
		}
	}
	return rows.Err()
}

func copyDevSessionVerbs(tx *sql.Tx, src *sql.DB, origSID, destSID string) error {
	rows, err := src.Query(`SELECT ext_id, n, verb FROM step_verbs WHERE session_id = ?`, origSID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var extID, verb string
		var n int
		if err := rows.Scan(&extID, &n, &verb); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO step_verbs(session_id, ext_id, n, verb) VALUES (?, ?, ?, ?)`,
			destSID, printable(extID), n, printable(verb)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) copyDevWorkWaves(src *sql.DB, label string) error {
	rows, err := src.Query(`SELECT stamp, worker, project_root, state, model, step_count, commits, base_check, judge_model, judge_verdict, updated_at FROM work_waves`)
	if err != nil {
		return err
	}
	defer rows.Close()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for rows.Next() {
		var stamp, worker, root, state, model, bc, jm, jv string
		var steps, commits int
		var updatedAt int64
		if err := rows.Scan(&stamp, &worker, &root, &state, &model, &steps, &commits, &bc, &jm, &jv, &updatedAt); err != nil {
			return err
		}
		enc := encodeWaveStamp(printable(label), printable(stamp))
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO work_waves(stamp, worker, project_root, state, model, step_count, commits, base_check, judge_model, judge_verdict, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			enc, printable(worker), printable(root), printable(state), printable(model),
			max(0, steps), max(0, commits), printable(bc), printable(jm), printable(jv), updatedAt,
		); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

func nullPrintable(ns sql.NullString) any {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	return printable(ns.String)
}

func scopedNullString(label string, ns sql.NullString) any {
	if !ns.Valid || strings.TrimSpace(ns.String) == "" {
		return nil
	}
	return scopedKey(label, ns.String)
}
