package brain

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

// openScopes is openForSearch with the scopes the search reads; a nil
// store is nothing to search.
func openScopes(o Options) (*store, string, []string, error) {
	st, project, err := openForSearch(o)
	if err != nil || st == nil {
		return nil, "", nil, err
	}
	scopes, err := searchScopes(st, o, project)
	if err != nil {
		st.close()
		return nil, "", nil, err
	}
	return st, project, scopes, nil
}

// searchScopes is the scopes a search reads: the project's and the global
// scope, and with Global also every other project scope whose checkout
// carries this home's registration marker, so a clone's or a stranger's
// records answer no --global query (ADR-068, audit-choices D15). The
// project's own scope stays in under Global, registered or not: a search
// without --global reads it already (ledger D13).
func searchScopes(st *store, o Options, project string) ([]string, error) {
	scopes := []string{"", project}
	if !o.Global {
		return scopes, nil
	}
	rows, err := st.db.Query(`SELECT DISTINCT scope FROM records WHERE scope != '' ORDER BY scope`)
	if err != nil {
		return nil, fmt.Errorf("brain: %w", err)
	}
	var all []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	stateDir := filepath.Join(o.Home, harness.StateDir)
	for _, s := range all {
		if s != project && trail.Registered(stateDir, s) {
			scopes = append(scopes, s)
		}
	}
	return scopes, nil
}

// scopeFilter is the SQL that keeps a search to scopes; kindFilter keeps
// one record kind.
func scopeFilter(scopes []string, args []any) (string, []any) {
	for _, s := range scopes {
		args = append(args, s)
	}
	return ` AND r.scope IN (` + strings.TrimSuffix(strings.Repeat("?, ", len(scopes)), ", ") + `)`, args
}

func kindFilter(kind string, args []any) (string, []any) {
	if kind == "" || kind == "trail" {
		return "", args
	}
	return ` AND r.kind = ?`, append(args, kind)
}
