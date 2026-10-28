package search

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

// reconcileGlobal brings the global search index up to date: the files of
// ~/.config/fyr/, then the trail of every checkout in its projects table,
// dropping any that is gone or no longer carries this home's registration
// marker (ADR-068, ADR-068a).
func reconcileGlobal(o Options, out io.Writer) (*store, int, []proj, error) {
	path := globalDB(o.Home)
	if o.Rebuild {
		if err := rebuildGlobal(path, out); err != nil {
			return nil, 0, nil, err
		}
	}
	st, err := openStore(path, out)
	if err != nil {
		return nil, 0, nil, err
	}
	fail := func(err error) (*store, int, []proj, error) {
		st.close()
		return nil, 0, nil, err
	}
	confDir := filepath.Dir(path)
	view, err := configView(confDir)
	if err != nil {
		return fail(err)
	}
	changed, err := st.apply(source{project: configProject, dir: confDir, view: view})
	if err != nil {
		return fail(err)
	}
	regs, err := registrations(st)
	if err != nil {
		return fail(err)
	}
	projects := []proj{{key: configProject, name: configProject}}
	names := map[string]int{configProject: 1}
	for _, r := range regs {
		n, searched, err := reconcileRegistered(st, o.Home, r.root, out)
		if err != nil {
			return fail(err)
		}
		if !searched {
			continue
		}
		changed += n
		name := filepath.Base(r.root)
		names[name]++
		if names[name] > 1 {
			name = fmt.Sprintf("%s~%d", name, names[name])
		}
		projects = append(projects, proj{key: r.root, name: name, docsRoot: globalDocsRoot})
	}
	return st, changed, projects, nil
}

// globalDocsRoot is the one trail directory the global index reads in a
// registered checkout: never a plain docs/ (dropReason).
const globalDocsRoot = ".agents/docs"

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

// registration is one row of the global index's projects table.
type registration struct{ root, docs string }

// registrations lists the checkouts registered in st, first registered
// first.
func registrations(st *store) ([]registration, error) {
	rows, err := st.db.Query(`SELECT root, docs_root FROM projects ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	var regs []registration
	for rows.Next() {
		var r registration
		if err := rows.Scan(&r.root, &r.docs); err != nil {
			rows.Close()
			return nil, err
		}
		regs = append(regs, r)
	}
	rows.Close()
	return regs, nil
}

// reconcileRegistered brings one registered checkout's trail up to date
// and reports whether the query searches it: a checkout dropped, or
// skipped this time, is not, and a note on out says why.
func reconcileRegistered(st *store, home, root string, out io.Writer) (int, bool, error) {
	reason, err := dropReason(root)
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
		return 0, false, nil
	}
	if reason != "" {
		if err := dropProject(st, root); err != nil {
			return 0, false, err
		}
		fmt.Fprintln(out, printable(fmt.Sprintf("note: dropped %s (%s)", root, reason)))
		return 0, false, nil
	}
	v, _, err := gitView(root, globalDocsRoot+"/") // the global header covers many projects: no left-out list
	if errors.Is(err, errUnsafePartialClone) {
		// One checkout this git cannot read safely never stops the rest.
		fmt.Fprintln(out, printable(fmt.Sprintf("note: skipped %s this time (a partial clone, and this git is older than 2.44)", root)))
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("search: %w", err)
	}
	n, err := st.apply(source{project: root, dir: root, docsRoot: globalDocsRoot, view: v})
	if err != nil {
		return 0, false, err
	}
	return n, true, nil
}

// dropReason says why a registration should go, or "" when it stays; an
// error means the check could not run, and the registration stays. It goes
// only on positive evidence: the root is gone, git says it is not a
// checkout (or a different one), or .agents/docs is gone or not a directory
// (a plain docs/ is never served in its place; security review, rounds 2-3).
func dropReason(root string) (string, error) {
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
	fi, err := os.Stat(filepath.Join(root, ".agents", "docs"))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "no longer has .agents/docs", nil
	case err != nil:
		return "", err
	case !fi.IsDir():
		return "no longer has .agents/docs", nil
	}
	return "", nil
}

func dropProject(st *store, root string) error {
	if _, err := st.db.Exec(`BEGIN IMMEDIATE`); err != nil {
		if isBusy(err) {
			return errBusy
		}
		return err
	}
	for _, q := range []string{`DELETE FROM files WHERE project = ?`, `DELETE FROM projects WHERE root = ?`} {
		if _, err := st.db.Exec(q, root); err != nil {
			st.db.Exec(`ROLLBACK`)
			return err
		}
	}
	_, err := st.db.Exec(`COMMIT`)
	return err
}
