package brain

import (
	"context"
	"fmt"
	"sort"
)

// ListCurrentSpecsCtx returns current SPEC-* records for o.Dir (or the global
// scope when o.Global is true) using a read-only, context-bounded SQLite
// connection without running migrations or following symlinks (ADR-535).
func ListCurrentSpecsCtx(ctx context.Context, o Options) ([]Record, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	st, ver, err := openTurnStoreCtx(ctx, o.Home)
	if err != nil || st == nil || ver < 1 {
		if st != nil {
			_ = st.close()
		}
		return nil, err
	}
	defer st.close()
	scope, ok := resolveTurnScope(o)
	if !ok {
		return nil, nil
	}
	q := `SELECT ` + selectRecordCols(ver) + ` FROM records WHERE scope = ? AND kind = 'spec' AND status = 'current'`
	rows, err := st.db.QueryContext(ctx, q, scope)
	if err != nil {
		return nil, fmt.Errorf("brain: %w", err)
	}
	defer rows.Close()
	out, err := scanRecordsRows(rows)
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return recordDescLess(out[i], out[j]) })
	return out, nil
}
