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
	case errors.As(err, &ee) && ee.ExitCode() == 1:
		return errNotIgnored
	default:
		return fmt.Errorf("search: git check-ignore: %w", err)
	}
	// A rebuild removes the companions, which need no ignore rule of their
	// own but must not be files the checkout tracks.
	for _, name := range []string{"-wal", "-shm", "-journal"} {
		rel := ".agents/scratch/search.db" + name
		out, err := git(root, nil, "ls-files", "--", rel)
		if err != nil {
			return fmt.Errorf("search: %w", err)
		}
		if len(out) > 0 {
			return fmt.Errorf("search: %s is tracked; `git rm --cached` it, since a rebuild would delete it", rel)
		}
	}
	return nil
}

// prepare opens and reconciles the index a query reads: the checkout's
// own, or with --global the global search index, after registering the
// checkout there when it carries the operator's marker and a trail.
func prepare(o Options, out io.Writer) (*scope, error) {
	start := time.Now()
	root, rootErr := checkoutRoot(o.Dir)
	if rootErr != nil && !o.Global {
		return nil, errNotCheckout
	}
	sc := &scope{byKey: map[string]proj{}}
	if rootErr == nil {
		if err := sc.prepareProject(o, root, out); err != nil {
			return nil, err
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

// prepareProject reconciles the checkout's own index: into sc when the
// query is the checkout's, closed again under --global; then registers the
// checkout in the global search index when it carries the operator's marker
// and .agents/trail.jsonl (ADR-068, ADR-086).
func (sc *scope) prepareProject(o Options, root string, out io.Writer) error {
	if err := guardProject(root); err != nil {
		return err
	}
	path := projectDB(root)
	if o.Rebuild && !o.Global {
		if err := removeDB(path); err != nil {
			return fmt.Errorf("search: %w", err)
		}
	}
	st, err := openStore(path, out)
	if err != nil {
		return err
	}
	view, leftOut, err := gitView(root)
	if err != nil {
		st.close()
		return fmt.Errorf("search: %w", err)
	}
	n, err := st.apply(source{project: root, dir: root, view: view})
	if err != nil {
		st.close()
		return err
	}
	sc.changed += n
	p := proj{key: root, name: filepath.Base(root)}
	if !o.Global {
		sc.st, sc.projects = st, []proj{p}
		sc.leftOut = leftOut
		sc.byKey[root] = p
	} else {
		st.close()
	}
	// Only a checkout the operator registered (`fyr index --register`, a
	// marker no clone carries) joins the global index: a third-party
	// clone's trail would otherwise answer every --global query and feed
	// /retro's cross-project evidence (ADR-068; security review, finding 2).
	// It also needs a trail, which dropReason would drop it for lacking; a
	// check that errors registers nothing this time.
	// The marker is read on the checkout that ran: its common directory, and
	// the seal to its own main path, are what the operator's consent names.
	if !trail.Registered(filepath.Join(o.Home, harness.StateDir), root) {
		return nil
	}
	row := RegistryRoot(root)
	if ok, err := HasTrail(row); err == nil && ok {
		if err := register(o.Home, row, out); err != nil {
			sc.close()
			return err
		}
	}
	return nil
}

// RegistryRoot is the root a projects row names for the checkout holding dir:
// the main worktree's, whichever linked worktree ran, because the dream keys
// its target and watermark on it (ADR-086). A worktree of a bare repository
// has no main work tree, so it is its own.
func RegistryRoot(dir string) string {
	main, top, err := trail.Roots(dir)
	if err != nil {
		return dir
	}
	if got, err := checkoutRoot(main); err == nil && got == main {
		return main
	}
	return top
}

// register records a checkout in the global search index, at most once a
// day per checkout.
func register(home, root string, out io.Writer) error {
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
	_, err = st.db.Exec(`INSERT INTO projects(root, name, docs_root, last_seen) VALUES (?, ?, '', ?)
		ON CONFLICT(root) DO UPDATE SET last_seen = excluded.last_seen`,
		root, filepath.Base(root), now)
	if isBusy(err) {
		return errBusy
	}
	return err
}
