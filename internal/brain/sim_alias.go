package brain

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

func resolveSIMAlias(ctx context.Context, q sqlQuerier, id string) (string, bool, error) {
	cur := id
	visited := map[string]bool{id: true}
	for depth := 0; depth < maxSIMAliasDepth; depth++ {
		var next string
		err := q.QueryRowContext(ctx, `SELECT target_id FROM sim_aliases WHERE old_id = ?`, cur).Scan(&next)
		if errors.Is(err, sql.ErrNoRows) {
			return cur, cur != id, nil
		}
		if err != nil {
			return "", false, err
		}
		if visited[next] {
			return "", false, refuse("sim: merge cycle refused")
		}
		visited[next] = true
		cur = next
	}
	var extra string
	if err := q.QueryRowContext(ctx, `SELECT target_id FROM sim_aliases WHERE old_id = ?`, cur).Scan(&extra); err == nil {
		return "", false, refuse("sim: merge cycle refused")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	return cur, cur != id, nil
}

func loadSIMAliasMap(ctx context.Context, q sqlQuerier) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT old_id, target_id FROM sim_aliases`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var oldID, targetID string
		if err := rows.Scan(&oldID, &targetID); err != nil {
			return nil, err
		}
		out[oldID] = targetID
	}
	return out, rows.Err()
}

func resolveAliasInMap(m map[string]string, id string) (string, bool) {
	cur := id
	visited := map[string]bool{id: true}
	for depth := 0; depth < maxSIMAliasDepth; depth++ {
		next, ok := m[cur]
		if !ok {
			return cur, true
		}
		if visited[next] {
			return "", false
		}
		visited[next] = true
		cur = next
	}
	if _, more := m[cur]; more {
		return "", false
	}
	return cur, true
}

func aliasesForCanonical(m map[string]string, canonID string) []string {
	var out []string
	for oldID := range m {
		if target, ok := resolveAliasInMap(m, oldID); ok && target == canonID {
			out = append(out, oldID)
		}
	}
	sort.Strings(out)
	return out
}

// MergeSIMProblem marks oldID as merged and inserts an alias to targetID's
// canonical problem, refusing cycles and derived merges of operator problems.
func MergeSIMProblem(home, oldID, targetID, provenance string, now time.Time) (SIMMergeResult, error) {
	oldID, targetID = strings.TrimSpace(oldID), strings.TrimSpace(targetID)
	if !validSIMProblemID(oldID) || !validSIMProblemID(targetID) {
		return SIMMergeResult{}, usage("sim: invalid problem id (want SIM-PNNN)")
	}
	prov, err := validateSIMEnum("provenance", provenance, "derived", "operator", "derived")
	if err != nil {
		return SIMMergeResult{}, err
	}
	if oldID == targetID {
		return SIMMergeResult{}, refuse("sim: merge cycle refused")
	}
	if now.IsZero() {
		now = time.Now()
	}
	st, err := openStore(home)
	if err != nil {
		return SIMMergeResult{}, err
	}
	defer st.close()
	var res SIMMergeResult
	err = st.tx(func(c *sql.Conn) error {
		var txErr error
		res, txErr = mergeSIMProblemTx(context.Background(), c, oldID, targetID, prov, now.UTC().Format(time.RFC3339))
		return txErr
	})
	return res, err
}

func mergeSIMProblemTx(ctx context.Context, c *sql.Conn, oldID, targetID, prov, stamp string) (SIMMergeResult, error) {
	canonTarget, err := checkSIMMergeAllowed(ctx, c, oldID, targetID, prov)
	if err != nil {
		return SIMMergeResult{}, err
	}
	var curTarget, curMerged string
	err = c.QueryRowContext(ctx, `SELECT target_id, merged_at FROM sim_aliases WHERE old_id = ?`, oldID).Scan(&curTarget, &curMerged)
	if err == nil {
		if curTarget == canonTarget {
			return SIMMergeResult{OldID: oldID, TargetID: canonTarget, MergedAt: curMerged}, nil
		}
		return SIMMergeResult{}, refuse("sim: problem %s is already merged into %s", oldID, curTarget)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return SIMMergeResult{}, err
	}
	if _, err := c.ExecContext(ctx, `UPDATE sim_problems SET status = 'merged', updated_at = ? WHERE id = ?`, stamp, oldID); err != nil {
		return SIMMergeResult{}, err
	}
	if _, err := c.ExecContext(ctx, `INSERT INTO sim_aliases(old_id, target_id, merged_at) VALUES (?, ?, ?)`, oldID, canonTarget, stamp); err != nil {
		return SIMMergeResult{}, err
	}
	return SIMMergeResult{OldID: oldID, TargetID: canonTarget, MergedAt: stamp}, nil
}

func checkSIMMergeAllowed(ctx context.Context, c *sql.Conn, oldID, targetID, prov string) (string, error) {
	oldProb, ok, err := lookupSIMProblem(ctx, c, oldID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", refuse("sim: problem %s not found", oldID)
	}
	if oldProb.Provenance == "operator" && prov != "operator" {
		return "", refuse("sim: operator-provenance problem %s is immutable to derived writes", oldID)
	}
	if _, ok, err := lookupSIMProblem(ctx, c, targetID); err != nil {
		return "", err
	} else if !ok {
		return "", refuse("sim: problem %s not found", targetID)
	}
	canonTarget, _, err := resolveSIMAlias(ctx, c, targetID)
	if err != nil {
		return "", err
	}
	if canonTarget == oldID {
		return "", refuse("sim: merge cycle refused")
	}
	return canonTarget, nil
}
