package search

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

// projectDB is where a checkout's index lives.
func projectDB(root string) string {
	return filepath.Join(root, ".agents", "scratch", "search.db")
}

func globalDB(home string) string {
	return filepath.Join(home, harness.StateDir, "search.db")
}

// guardProject refuses what the index must not write through or leave
// unignored.
func guardProject(root string) error {
	for _, d := range []string{".agents", filepath.Join(".agents", "scratch")} {
		if fi, err := os.Lstat(filepath.Join(root, d)); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("search: %s is a symlink; refusing to write through it", filepath.ToSlash(d))
		}
	}
	cmd := trail.Command(root, "check-ignore", "-q", ".agents/scratch/search.db")
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ee) && ee.ExitCode() == 1:
		return errNotIgnored
	default:
		return fmt.Errorf("search: git check-ignore: %w", err)
	}
}

// prepare opens and reconciles the index a query reads: the checkout's
// own, or with --global the global search index, after registering the
// checkout (when there is one) there.
func prepare(o Options, out io.Writer) (*scope, error) {
	start := time.Now()
	root, rootErr := checkoutRoot(o.Dir)
	if rootErr != nil && !o.Global {
		return nil, errNotCheckout
	}
	sc := &scope{byKey: map[string]proj{}}
	if rootErr == nil {
		if err := guardProject(root); err != nil {
			return nil, err
		}
		path := projectDB(root)
		if o.Rebuild && !o.Global {
			if err := removeDB(path); err != nil {
				return nil, fmt.Errorf("search: %w", err)
			}
		}
		st, err := openStore(path, out)
		if err != nil {
			return nil, err
		}
		docs := docsRootOf(root)
		view, leftOut, err := gitView(root, "")
		if err != nil {
			st.close()
			return nil, fmt.Errorf("search: %w", err)
		}
		n, err := st.apply(source{project: root, dir: root, docsRoot: docs, view: view})
		if err != nil {
			st.close()
			return nil, err
		}
		sc.changed += n
		p := proj{key: root, name: filepath.Base(root), docsRoot: docs}
		if !o.Global {
			sc.st, sc.projects = st, []proj{p}
			sc.leftOut = leftOut
			sc.byKey[root] = p
		} else {
			st.close()
		}
		// Only a checkout the harness scaffolded registers: a third-party
		// clone's docs/ would otherwise answer every --global query and
		// feed /retro's cross-project evidence (security review, finding 2).
		if fi, err := os.Stat(filepath.Join(root, ".agents", "docs")); err == nil && fi.IsDir() {
			if err := register(o.Home, root, docs, out); err != nil {
				sc.close()
				return nil, err
			}
		}
	}
	if o.Global {
		st, n, projects, err := reconcileGlobal(o, out)
		if err != nil {
			return nil, err
		}
		sc.st, sc.projects, sc.global = st, projects, true
		sc.changed += n
		for _, p := range projects {
			sc.byKey[p.key] = p
		}
	}
	if err := sc.st.db.QueryRow(`SELECT count(*) FROM files WHERE project IN `+sc.in(), sc.keys()...).Scan(&sc.files); err != nil {
		sc.close()
		return nil, err
	}
	sc.elapsed = time.Since(start)
	return sc, nil
}

// register records a checkout in the global search index, at most once a
// day per checkout.
func register(home, root, docs string, out io.Writer) error {
	st, err := openStore(globalDB(home), out)
	if err != nil {
		return err
	}
	defer st.close()
	now := nowFunc().Unix()
	var last int64
	err = st.db.QueryRow(`SELECT last_seen FROM projects WHERE root = ?`, root).Scan(&last)
	if err == nil && now-last < touchEvery {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = st.db.Exec(`INSERT INTO projects(root, name, docs_root, last_seen) VALUES (?, ?, ?, ?)
		ON CONFLICT(root) DO UPDATE SET docs_root = excluded.docs_root, last_seen = excluded.last_seen`,
		root, filepath.Base(root), docs, now)
	if isBusy(err) {
		return errBusy
	}
	return err
}
