package search

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// reconcileGlobal brings the global search index up to date: the files of
// ~/.config/fyr/, then the trail of every registered checkout, dropping
// any that is gone.
func reconcileGlobal(o Options, out io.Writer) (*store, int, []proj, error) {
	path := globalDB(o.Home)
	if o.Rebuild {
		dropped := 0
		if st, err := tryOpen(path); err == nil {
			st.db.QueryRow(`SELECT count(*) FROM projects`).Scan(&dropped)
			st.close()
		}
		if err := removeDB(path); err != nil {
			return nil, 0, nil, fmt.Errorf("search: %w", err)
		}
		if dropped > 0 {
			fmt.Fprintf(out, "note: %d project registrations dropped; each returns on its next search\n", dropped) // numbers only
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
	rows, err := st.db.Query(`SELECT root, docs_root FROM projects ORDER BY rowid`)
	if err != nil {
		return fail(err)
	}
	type reg struct{ root, docs string }
	var regs []reg
	for rows.Next() {
		var r reg
		if err := rows.Scan(&r.root, &r.docs); err != nil {
			rows.Close()
			return fail(err)
		}
		regs = append(regs, r)
	}
	rows.Close()
	projects := []proj{{key: configProject, name: configProject}}
	names := map[string]int{configProject: 1}
	for _, r := range regs {
		reason, err := dropReason(r.root)
		if err != nil {
			// A check that errors keeps the registration (context § Hard
			// rules): skip this checkout for this query and say why.
			fmt.Fprintln(out, printable(fmt.Sprintf("note: skipped %s this time (%v)", r.root, err)))
			continue
		}
		if reason != "" {
			if err := dropProject(st, r.root); err != nil {
				return fail(err)
			}
			fmt.Fprintln(out, printable(fmt.Sprintf("note: dropped %s (%s)", r.root, reason)))
			continue
		}
		docs := ".agents/docs"
		v, _, err := gitView(r.root, docs+"/") // the global header covers many projects: no left-out list
		if errors.Is(err, errUnsafePartialClone) {
			// One checkout this git cannot read safely never stops the rest.
			fmt.Fprintln(out, printable(fmt.Sprintf("note: skipped %s this time (a partial clone, and this git is older than 2.44)", r.root)))
			continue
		}
		if err != nil {
			return fail(fmt.Errorf("search: %w", err))
		}
		n, err := st.apply(source{project: r.root, dir: r.root, docsRoot: docs, view: v})
		if err != nil {
			return fail(err)
		}
		changed += n
		name := filepath.Base(r.root)
		names[name]++
		if names[name] > 1 {
			name = fmt.Sprintf("%s~%d", name, names[name])
		}
		projects = append(projects, proj{key: r.root, name: name, docsRoot: docs})
	}
	return st, changed, projects, nil
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
