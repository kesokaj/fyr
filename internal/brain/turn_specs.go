package brain

import (
	"context"
	"fmt"
	"sort"
)

// ListCurrentSpecsCtx returns current SPEC-* records for o.Dir (or the global
// scope when o.Global is true) using a read-only, context-bounded SQLite
// connection without running migrations or following symlinks (ADR-535).
// The boolean reports whether brain.db was present on disk so callers avoid
// falling back to a stale on-demand trail.jsonl when all specs were retired.
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
		return nil, st != nil, err
	}
	defer st.close()
	scope, ok := resolveTurnScope(o)
	if !ok {
		return nil, true, nil
	}
	q := `SELECT ` + selectRecordCols(ver) + ` FROM records WHERE scope = ? AND kind = 'spec' AND status = 'current'`
	rows, err := st.db.QueryContext(ctx, q, scope)
	if err != nil {
		return nil, true, fmt.Errorf("brain: %w", err)
	}
	defer rows.Close()
	out, err := scanRecordsRows(rows)
	if err != nil {
		return nil, true, err
	}
	sort.Slice(out, func(i, j int) bool { return recordDescLess(out[i], out[j]) })
	return out, true, nil
}
