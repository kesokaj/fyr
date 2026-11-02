package search

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

// reconcileGlobal brings the global search index up to date: its projects
// table, dropping any checkout that is gone or no longer carries this home's
// registration marker (ADR-068, ADR-068a). The index holds no file: --global
// answers from the brain store (ADR-086, ADR-087).
func reconcileGlobal(o Options, out io.Writer) (*store, error) {
	path := globalDB(o.Home)
	if o.Rebuild {
		if err := rebuildGlobal(path, out); err != nil {
			return nil, err
		}
	}
	st, err := openStore(path, out)
	if err != nil {
		return nil, err
	}
	roots, err := registrations(st)
	if err != nil {
		st.close()
		return nil, err
	}
	for _, root := range roots {
		if err := checkRegistered(st, o.Home, root, out); err != nil {
			st.close()
			return nil, err
		}
	}
	return st, nil
}

// rebuildGlobal deletes the global index for --rebuild, with a note of how
// many registrations go with it.
func rebuildGlobal(path string, out io.Writer) error {
	dropped := 0
	if st, err := tryOpen(path); err == nil {
		st.db.QueryRow(`SELECT count(*) FROM projects`).Scan(&dropped)
		st.close()
	}
	if err := removeDB(path); err != nil {
		return fmt.Errorf("search: %w", err)
	}
	if dropped > 0 {
		fmt.Fprintf(out, "note: %d project registrations dropped; each registered one returns on its next search\n", dropped) // numbers only
	}
	return nil
}

// registrations lists the checkouts registered in st, first registered
// first.
func registrations(st *store) ([]string, error) {
	rows, err := st.db.Query(`SELECT root FROM projects ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	var roots []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			rows.Close()
			return nil, err
		}
		roots = append(roots, r)
	}
	rows.Close()
	return roots, nil
}

// checkRegistered drops a registration that no longer holds, with a note on
// out saying why; one whose check errors stays, with a note.
func checkRegistered(st *store, home, root string, out io.Writer) error {
	reason, err := dropReason(home, root)
	if err == nil && reason == "" && !trail.Registered(filepath.Join(home, harness.StateDir), root) {
		// A row is a cache entry the checkout's own search writes again once
		// it is registered, so a marker that does not check out drops it: a
		// row from before ADR-068, a clone's, a moved checkout's (D4).
		reason = "not registered: fyr index --register"
	}
	if err != nil {
		// A check that errors keeps the registration (context § Hard
		// rules): skip this checkout for this query and say why.
		fmt.Fprintln(out, printable(fmt.Sprintf("note: skipped %s this time (%v)", root, err)))
		return nil
	}
	if reason == "" {
		return nil
	}
	if err := dropProject(st, root); err != nil {
		return err
	}
	fmt.Fprintln(out, printable(fmt.Sprintf("note: dropped %s (%s)", root, reason)))
	return nil
}

// HasTrail reports whether root's .agents/trail.jsonl is a regular file in a
// real .agents directory: a symlink at either name is no trail, as in brain's
// guardExportPath, and a file at .agents is none either. An error means the
// check could not run.
func HasTrail(root string) (bool, error) {
	fi, err := os.Lstat(filepath.Join(root, ".agents"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !fi.IsDir() {
		return false, nil
	}
	fi, err = os.Lstat(filepath.Join(root, filepath.FromSlash(trailExport)))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return fi.Mode().IsRegular(), nil
}

// HasScopeRecords reports whether root has project trail records: either a
// regular .agents/trail.jsonl in a real .agents directory, or exported-kind
// rows for root's scope in brain.db under home (ADR-091). A symlink or
// non-regular file at .agents or .agents/trail.jsonl returns false, nil. It
// never creates brain.db.
func HasScopeRecords(home, root string) (bool, error) {
	if fi, err := os.Lstat(filepath.Join(root, ".agents")); err == nil {
		if !fi.IsDir() {
			return false, nil
		}
		tfi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(trailExport)))
		if err == nil {
			return tfi.Mode().IsRegular(), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if home == "" {
		return false, nil
	}
	brainPath := filepath.Join(home, harness.StateDir, "brain.db")
	if fi, err := os.Lstat(brainPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	} else if !fi.Mode().IsRegular() {
		return false, nil
	}
	scope := root
	if main, _, err := trail.Roots(root); err == nil {
		scope = main
	}
	db, err := sql.Open("sqlite", DSN(brainPath))
	if err != nil {
		return false, err
	}
	defer db.Close()
	var one int
	err = db.QueryRow(`SELECT 1 FROM records WHERE scope = ? AND kind NOT IN ('memory', 'feedback', 'proposal') LIMIT 1`, scope).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// dropReason says why a registration should go, or "" when it stays; an
// error means the check could not run, and the registration stays. It goes
// only on positive evidence: the root is gone, git says it is not a
// checkout (or a different one), or it has no records (ADR-086, ADR-091).
func dropReason(home, root string) (string, error) {
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return "no longer a git checkout", nil
	} else if err != nil {
		return "", err
	}
	got, err := checkoutRoot(root)
	switch {
	case err != nil && strings.Contains(err.Error(), "not a git repository"):
		return "no longer a git checkout", nil
	case err != nil:
		return "", err
	case got != root:
		return "no longer a git checkout", nil
	}
	ok, err := HasScopeRecords(home, root)
	switch {
	case err != nil:
		return "", err
	case !ok:
		return "no longer has .agents/trail.jsonl", nil
	}
	return "", nil
}

func dropProject(st *store, root string) error {
	_, err := st.db.Exec(`DELETE FROM projects WHERE root = ?`, root)
	if isBusy(err) {
		return errBusy
	}
	return err
}
