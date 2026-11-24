package brain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
)

// ListCurrentSpecsCtx returns current SPEC-* records for o.Dir (or the global
// scope when o.Global is true) using a read-only, context-bounded SQLite
// connection without running migrations or following symlinks (ADR-535, ADR-536).
// The boolean reports whether brain.db holds at least one row for the target
// scope so an unsynced clone with tracked .agents/trail.jsonl still reads trail
// specs while a synced repo whose specs were retired avoids stale trail fallback.
func ListCurrentSpecsCtx(ctx context.Context, o Options) ([]Record, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	st, ver, err := openTurnStoreCtx(ctx, o.Home)
	if err != nil || st == nil || ver < 1 {
		if st != nil {
			_ = st.close()
		}
		return nil, false, err
	}
	defer st.close()
	scope, ok := resolveTurnScope(o)
	if !ok {
		return nil, false, nil
	}
	q := `SELECT ` + selectRecordCols(ver) + ` FROM records WHERE scope = ? AND kind = 'spec' AND status = 'current'`
	rows, err := st.db.QueryContext(ctx, q, scope)
	if err != nil {
		return nil, false, fmt.Errorf("brain: %w", err)
	}
	defer rows.Close()
	out, err := scanRecordsRows(rows)
	if err != nil {
		return nil, false, err
	}
	if len(out) > 0 {
		sort.Slice(out, func(i, j int) bool { return recordDescLess(out[i], out[j]) })
		return out, true, nil
	}
	var one int
	if err := st.db.QueryRowContext(ctx, `SELECT 1 FROM records WHERE scope = ? LIMIT 1`, scope).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("brain: %w", err)
	}
	return nil, true, nil
}
