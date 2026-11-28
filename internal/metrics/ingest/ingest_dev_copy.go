package ingest

import (
	"database/sql"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/search"
)

// CopyDevStoreTables copies trials, sessions, steps, verbs, and work_waves from a dev metrics.db under label.
func (d Deps) CopyDevStoreTables(srcPath, tmp, label string, log io.Writer) {
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
	if err != nil || ver < 4 || (d.MaxSchemaVersion > 0 && ver > d.MaxSchemaVersion) {
		noteSecondBrain(log, "%s: unsupported schema %q", srcPath, verStr)
		return
	}
	if err := d.copyDevTrials(srcDB, label, ver >= 5, ver >= 13); err != nil {
		noteSecondBrain(log, "%s trials: %v", srcPath, err)
	}
	if err := d.copyDevSessionsAndSteps(srcDB, label, ver >= 5, ver >= 11); err != nil {
		noteSecondBrain(log, "%s sessions: %v", srcPath, err)
	}
	if ver >= 6 {
		if err := d.copyDevWorkWaves(srcDB, label, ver >= 7); err != nil {
			noteSecondBrain(log, "%s work_waves: %v", srcPath, err)
		}
	}
}

func (d Deps) copyDevTrials(src *sql.DB, label string, hasStageCols, hasPolicyModel bool) error {
	stageCols := `NULL, NULL`
	if hasStageCols {
		stageCols = `setup_seconds, check_seconds`
	}
	pmCol := `''`
	if hasPolicyModel {
		pmCol = `COALESCE(policy_model, '')`
	}
	q := `SELECT run, row_version, kind, cluster, expect, harness, cli, status, cause, started_at, seconds, session_seconds, ` + stageCols + `, ` + pmCol + ` FROM trials`
	rows, err := src.Query(q)
	if err != nil {
		return err
	}
	defer rows.Close()
	tx, err := d.DB.Begin()
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
	var run, harnessVal, status, cause, policyModel string
	var kind, cluster, expect, cli sql.NullString
	var rowVer, sesSec, setSec, chkSec sql.NullInt64
	var startedAt, seconds int64
	if err := rows.Scan(&run, &rowVer, &kind, &cluster, &expect, &harnessVal, &cli, &status, &cause, &startedAt, &seconds, &sesSec, &setSec, &chkSec, &policyModel); err != nil {
		return err
	}
	destRun := ScopedKey(label, run)
	if destRun == "" {
		return nil
	}
	_, err := tx.Exec(
		`INSERT OR IGNORE INTO trials(run, label, row_version, kind, cluster, expect, harness, cli, status, cause, started_at, seconds, session_seconds, setup_seconds, check_seconds, policy_model) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		destRun, search.Printable(label), rowVer, nullPrintable(kind), nullPrintable(cluster), nullPrintable(expect),
		search.Printable(harnessVal), nullPrintable(cli), search.Printable(status), search.Printable(cause),
		startedAt, seconds, sesSec, setSec, chkSec, clipIngestText(policyModel, trialTextLimit),
	)
	return err
}

func (d Deps) copyDevSessionsAndSteps(src *sql.DB, label string, hasStepTimes, hasRepoID bool) error {
	q := `SELECT id, vendor, project_root, '', parent_id, agent_type, model, outcome, run, started_at, ended_at, cost_usd, duration_ms FROM sessions`
	if hasRepoID {
		q = `SELECT id, vendor, project_root, COALESCE(repo_id, ''), parent_id, agent_type, model, outcome, run, started_at, ended_at, cost_usd, duration_ms FROM sessions`
	}
	rows, err := src.Query(q)
	if err != nil {
		return err
	}
	defer rows.Close()
	tx, err := d.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for rows.Next() {
		origID, destID, inserted, err := d.insertCopiedSession(tx, rows, label)
		if err != nil {
			return err
		}
		if !inserted {
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

func (d Deps) insertCopiedSession(tx *sql.Tx, rows *sql.Rows, label string) (string, string, bool, error) {
	var origID, repoID string
	var vendor, root, parent, agent, model, outcome, run sql.NullString
	var started, ended, dur sql.NullInt64
	var cost sql.NullFloat64
	if err := rows.Scan(&origID, &vendor, &root, &repoID, &parent, &agent, &model, &outcome, &run, &started, &ended, &cost, &dur); err != nil {
		return "", "", false, err
	}
	destID := ScopedKey(label, origID)
	if destID == "" {
		return "", "", false, nil
	}
	repoID = clipIngestText(repoID, 256)
	if repoID == "" && root.Valid && strings.TrimSpace(root.String) != "" {
		repoID = clipIngestText(d.resolveRepoID(root.String), 256)
	}
	res, err := tx.Exec(
		`INSERT OR IGNORE INTO sessions(id, vendor, project_root, repo_id, parent_id, agent_type, model, label, outcome, run, started_at, ended_at, cost_usd, duration_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		destID, nullPrintable(vendor), nullPrintable(root), repoID, scopedNullString(label, parent),
		nullPrintable(agent), nullPrintable(model), search.Printable(label), nullPrintable(outcome),
		scopedNullString(label, run), started, ended, cost, dur,
	)
	if err != nil {
		return "", "", false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return "", "", false, err
	}
	return origID, destID, true, nil
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
			destSID, search.Printable(extID), ts, nullPrintable(kind), nullPrintable(tool), nullPrintable(skill),
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
			destSID, search.Printable(extID), n, search.Printable(verb)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (d Deps) copyDevWorkWaves(src *sql.DB, label string, hasStageCols bool) error {
	q := `SELECT stamp, worker, project_root, state, model, step_count, commits, base_check, judge_model, judge_verdict, updated_at, 0, 0, 0 FROM work_waves`
	if hasStageCols {
		q = `SELECT stamp, worker, project_root, state, model, step_count, commits, base_check, judge_model, judge_verdict, updated_at, COALESCE(setup_s, 0), COALESCE(agy_s, 0), COALESCE(check_s, 0) FROM work_waves`
	}
	rows, err := src.Query(q)
	if err != nil {
		return err
	}
	defer rows.Close()
	tx, err := d.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for rows.Next() {
		var stamp, worker, root, state, model, bc, jm, jv string
		var steps, commits, setupS, agyS, checkS int
		var updatedAt int64
		if err := rows.Scan(&stamp, &worker, &root, &state, &model, &steps, &commits, &bc, &jm, &jv, &updatedAt, &setupS, &agyS, &checkS); err != nil {
			return err
		}
		enc := EncodeWaveStamp(search.Printable(label), search.Printable(stamp))
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO work_waves(stamp, worker, project_root, state, model, step_count, commits, base_check, judge_model, judge_verdict, updated_at, setup_s, agy_s, check_s) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			enc, search.Printable(worker), search.Printable(root), search.Printable(state), search.Printable(model),
			max(0, steps), max(0, commits), search.Printable(bc), search.Printable(jm), search.Printable(jv), updatedAt,
			max(0, setupS), max(0, agyS), max(0, checkS),
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
	return search.Printable(ns.String)
}

func scopedNullString(label string, ns sql.NullString) any {
	if !ns.Valid || strings.TrimSpace(ns.String) == "" {
		return nil
	}
	return ScopedKey(label, ns.String)
}
