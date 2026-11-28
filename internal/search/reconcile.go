package search

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
)

// maxFileBytes is the largest file indexed; larger files, and files with a
// NUL byte in their first 8 KiB, are skipped.
const (
	maxFileBytes           = 1 << 20
	binarySniff            = 8 << 10
	gcAge                  = 14 * 24 * 60 * 60 // seconds an unreferenced blob is kept
	touchEvery             = 24 * 60 * 60      // seconds between last_seen refreshes
	walCheckpointThreshold = 50
)

// source is what apply needs to know about one project.
type source struct {
	project string
	dir     string // where the view's paths are read from
	view    []entry
}

// apply brings one project's rows up to its current view and returns how
// many paths changed. When nothing changed and last_seen is fresh it takes
// no write lock at all.
func (s *store) apply(src source) (int, error) {
	now := nowFunc().Unix()
	var verBefore int
	_ = s.db.QueryRow(`PRAGMA data_version`).Scan(&verBefore)
	changed, files, blobs, w, err := s.diffCountWithState(src)
	if err != nil {
		return 0, err
	}
	if changed == 0 && !s.touchDue(src.project, now) {
		return 0, nil
	}
	if _, err := s.db.Exec(`BEGIN IMMEDIATE`); err != nil {
		if isBusy(err) {
			return 0, errBusy
		}
		return 0, err
	}
	n, err := s.applyLocked(src, now, files, blobs, w, verBefore)
	if err != nil {
		s.db.Exec(`ROLLBACK`)
		if isBusy(err) {
			return 0, errBusy
		}
		return 0, err
	}
	if _, err := s.db.Exec(`COMMIT`); err != nil {
		s.db.Exec(`ROLLBACK`)
		if isBusy(err) {
			return 0, errBusy
		}
		return 0, err
	}
	if n >= walCheckpointThreshold {
		s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	}
	return n, nil
}

// current reads the project's files rows and the blob table's skip marks.
func (s *store) current(project string) (map[string]string, map[string]bool, error) {
	files := map[string]string{}
	rows, err := s.db.Query(`SELECT path, oid FROM files WHERE project = ?`, project)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var p, o string
		if err := rows.Scan(&p, &o); err != nil {
			rows.Close()
			return nil, nil, err
		}
		files[p] = o
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	blobs := map[string]bool{} // oid -> skipped
	rows, err = s.db.Query(`SELECT oid, skipped FROM blobs`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var o string
		var sk int
		if err := rows.Scan(&o, &sk); err != nil {
			return nil, nil, err
		}
		blobs[o] = sk != 0
	}
	return files, blobs, rows.Err()
}

// trailExport is the brain store's on-demand export, relative to a checkout.
const trailExport = ".agents/trail.jsonl"

// wanted is the view minus paths whose blob is known to be skipped.
func wanted(view []entry, blobs map[string]bool) map[string]entry {
	w := map[string]entry{}
	for _, e := range view {
		if skipped, known := blobs[e.oid]; known && skipped {
			continue
		}
		w[e.path] = e
	}
	return w
}

func (s *store) diffCount(src source) (int, error) {
	n, _, _, _, err := s.diffCountWithState(src)
	return n, err
}

func (s *store) diffCountWithState(src source) (int, map[string]string, map[string]bool, map[string]entry, error) {
	files, blobs, err := s.current(src.project)
	if err != nil {
		return 0, nil, nil, nil, err
	}
	w := wanted(src.view, blobs)
	n := 0
	for p, e := range w {
		if files[p] != e.oid {
			n++
		}
	}
	for p := range files {
		if _, ok := w[p]; !ok {
			n++
		}
	}
	return n, files, blobs, w, nil
}

func (s *store) touchDue(project string, now int64) bool {
	var v int64
	err := s.db.QueryRow(`SELECT CAST(value AS INTEGER) FROM meta WHERE key = ?`, "touched:"+project).Scan(&v)
	return err != nil || now-v >= touchEvery
}

func (s *store) applyLocked(src source, now int64, files map[string]string, blobs map[string]bool, w map[string]entry, verBefore int) (int, error) {
	var verAfter int
	if err := s.db.QueryRow(`PRAGMA data_version`).Scan(&verAfter); err != nil || verBefore == 0 || verAfter != verBefore || files == nil {
		var err2 error
		files, blobs, err2 = s.current(src.project)
		if err2 != nil {
			return 0, err2
		}
		w = wanted(src.view, blobs)
	}
	if err := s.indexBlobs(src, blobs, now); err != nil {
		return 0, err
	}
	n, err := s.writeFiles(src, files, w)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		if err := s.reconcileGoInterfaces(src.project); err != nil {
			return 0, err
		}
	}
	if _, err := s.db.Exec(`UPDATE blobs SET last_seen = ? WHERE oid IN (SELECT oid FROM files WHERE project = ?)`, now, src.project); err != nil {
		return 0, err
	}
	if _, err := s.db.Exec(`INSERT INTO meta(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, "touched:"+src.project, now); err != nil {
		return 0, err
	}
	s.saveGitIdxHeadSig(src.project)
	return n, s.gc(now)
}

// applyDirtyPaths incrementally updates only the supplied modified, untracked,
// restored, or deleted paths in search.db without scanning the full files/blobs tables.
func (s *store) applyDirtyPaths(ctx context.Context, root string, dirtyPaths []entry, existingFiles int) error {
	if len(dirtyPaths) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	fsRoot, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer fsRoot.Close()
	if _, err := s.db.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		if isBusy(err) {
			return errBusy
		}
		return err
	}
	n, goChanged, err := s.applyDirtyPathsLocked(ctx, root, fsRoot, dirtyPaths, nowFunc().Unix())
	if err != nil {
		_, _ = s.db.Exec(`ROLLBACK`)
		if isBusy(err) {
			return errBusy
		}
		return err
	}
	if n > 0 && goChanged && existingFiles < 2000 && ctx.Err() == nil {
		if err := s.reconcileGoInterfaces(root); err != nil {
			_, _ = s.db.Exec(`ROLLBACK`)
			return err
		}
	}
	if _, err := s.db.Exec(`COMMIT`); err != nil {
		_, _ = s.db.Exec(`ROLLBACK`)
		if isBusy(err) {
			return errBusy
		}
		return err
	}
	return nil
}

func (s *store) applyDirtyPathsLocked(ctx context.Context, root string, fsRoot *os.Root, dirtyPaths []entry, now int64) (int, bool, error) {
	stmts, err := s.prepareDirtyPathStmts()
	if err != nil {
		return 0, false, err
	}
	defer stmts.close()

	var in *inserter
	defer func() {
		if in != nil {
			in.close()
		}
	}()
	n, goChanged := 0, false
	for _, e := range dirtyPaths {
		if err := ctx.Err(); err != nil {
			return n, goChanged, err
		}
		if strings.HasSuffix(e.path, ".go") {
			goChanged = true
		}
		if err := s.applyOneDirtyPathLocked(ctx, root, fsRoot, e, now, stmts, &in); err != nil {
			return n, goChanged, err
		}
		n++
	}
	return n, goChanged, nil
}

type dirtyPathStmts struct {
	del, upsert, blobSel, blobIns *sql.Stmt
}

func (d dirtyPathStmts) close() {
	for _, st := range []*sql.Stmt{d.del, d.upsert, d.blobSel, d.blobIns} {
		if st != nil {
			_ = st.Close()
		}
	}
}

func (s *store) prepareDirtyPathStmts() (dirtyPathStmts, error) {
	var d dirtyPathStmts
	var err error
	if d.del, err = s.db.Prepare(`DELETE FROM files WHERE project = ? AND path = ?`); err != nil {
		return d, err
	}
	if d.upsert, err = s.db.Prepare(`INSERT INTO files(project, path, oid, state) VALUES (?, ?, ?, ?) ON CONFLICT(project, path) DO UPDATE SET oid = excluded.oid, state = excluded.state`); err != nil {
		d.close()
		return dirtyPathStmts{}, err
	}
	if d.blobSel, err = s.db.Prepare(`SELECT skipped FROM blobs WHERE oid = ?`); err != nil {
		d.close()
		return dirtyPathStmts{}, err
	}
	if d.blobIns, err = s.db.Prepare(`INSERT INTO blobs(oid, last_seen, skipped, line_count) VALUES (?, ?, ?, ?)`); err != nil {
		d.close()
		return dirtyPathStmts{}, err
	}
	return d, nil
}

func (s *store) applyOneDirtyPathLocked(ctx context.Context, root string, fsRoot *os.Root, e entry, now int64, d dirtyPathStmts, in **inserter) error {
	if e.state == "deleted" {
		_, err := d.del.ExecContext(ctx, root, e.path)
		return err
	}
	skipped, err := s.ensureDirtyBlobLocked(ctx, fsRoot, e, now, d.blobSel, d.blobIns, in)
	if err != nil {
		return err
	}
	if skipped {
		_, err = d.del.ExecContext(ctx, root, e.path)
		return err
	}
	_, err = d.upsert.ExecContext(ctx, root, e.path, e.oid, e.state)
	return err
}

func (s *store) ensureDirtyBlobLocked(ctx context.Context, fsRoot *os.Root, e entry, now int64, blobSel, blobIns *sql.Stmt, in **inserter) (bool, error) {
	var skippedInt int
	err := blobSel.QueryRowContext(ctx, e.oid).Scan(&skippedInt)
	if err == nil {
		return skippedInt != 0, nil
	}
	if err != sql.ErrNoRows {
		return false, err
	}
	text, ok := readIndexable(fsRoot, filepath.FromSlash(e.path))
	lineCount := 0
	if ok {
		lineCount = len(splitLines(text))
	}
	if _, err := blobIns.ExecContext(ctx, e.oid, now, boolInt(!ok), lineCount); err != nil {
		return false, err
	}
	if !ok {
		return true, nil
	}
	if *in == nil {
		created, err := s.newInserter()
		if err != nil {
			return false, err
		}
		*in = created
	}
	return false, (*in).content(e, text)
}

// indexBlobs indexes every blob the database has not seen, parsed by the
// first path it appears at in the view (D14), and marks it in blobs. Reads
// go through an os.Root.
func (s *store) indexBlobs(src source, blobs map[string]bool, now int64) error {
	fsRoot, err := os.OpenRoot(src.dir)
	if err != nil {
		return err
	}
	defer fsRoot.Close()
	var (
		in       *inserter
		blobStmt *sql.Stmt
	)
	for _, e := range src.view {
		if _, known := blobs[e.oid]; known {
			continue
		}
		if blobStmt == nil {
			if blobStmt, err = s.db.Prepare(`INSERT INTO blobs(oid, last_seen, skipped, line_count) VALUES (?, ?, ?, ?)`); err != nil {
				return err
			}
			defer blobStmt.Close()
		}
		text, ok := readIndexable(fsRoot, filepath.FromSlash(e.path))
		lineCount := 0
		if ok {
			lineCount = len(splitLines(text))
		}
		if _, err := blobStmt.Exec(e.oid, now, boolInt(!ok), lineCount); err != nil {
			return err
		}
		blobs[e.oid] = !ok
		if !ok {
			continue
		}
		if in == nil {
			if in, err = s.newInserter(); err != nil {
				return err
			}
			defer in.close()
		}
		if err := in.content(e, text); err != nil {
			return err
		}
	}
	return nil
}

type fileWriteStmts struct {
	del, upd, ins *sql.Stmt
}

func (s *store) prepareFileWriteStmts() (fileWriteStmts, error) {
	var st fileWriteStmts
	var err error
	if st.del, err = s.db.Prepare(`DELETE FROM files WHERE project = ? AND path = ?`); err != nil {
		return st, err
	}
	if st.upd, err = s.db.Prepare(`UPDATE files SET state = ? WHERE project = ? AND path = ? AND state != ?`); err != nil {
		st.close()
		return st, err
	}
	if st.ins, err = s.db.Prepare(`INSERT INTO files(project, path, oid, state) VALUES (?, ?, ?, ?)`); err != nil {
		st.close()
		return st, err
	}
	return st, nil
}

func (st fileWriteStmts) close() {
	for _, stmt := range []*sql.Stmt{st.del, st.upd, st.ins} {
		if stmt != nil {
			stmt.Close()
		}
	}
}

// writeFiles brings the project's files rows from files to w and returns
// how many paths were added or removed; a path whose blob is unchanged only
// has its state updated.
func (s *store) writeFiles(src source, files map[string]string, w map[string]entry) (int, error) {
	stmts, err := s.prepareFileWriteStmts()
	if err != nil {
		return 0, err
	}
	defer stmts.close()
	n := 0
	for p, oid := range files {
		if e, ok := w[p]; !ok || e.oid != oid {
			if _, err := stmts.del.Exec(src.project, p); err != nil {
				return 0, err
			}
			n++
		}
	}
	for p, e := range w {
		if old, ok := files[p]; ok && old == e.oid {
			if _, err := stmts.upd.Exec(e.state, src.project, p, e.state); err != nil {
				return 0, err
			}
			continue
		}
		if _, ok := files[p]; !ok {
			n++
		}
		if _, err := stmts.ins.Exec(src.project, p, e.oid, e.state); err != nil {
			return 0, err
		}
	}
	return n, nil
}

func (s *store) reconcileGoInterfaces(project string) error {
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM symbols sy JOIN files f ON f.oid = sy.oid WHERE f.project = ? AND sy.kind = 'interface'`, project).Scan(&n); err != nil || n == 0 {
		return err
	}
	rows, err := s.db.Query(`SELECT f.path, sy.oid, sy.name, sy.recv, sy.kind, sy.start_line FROM symbols sy JOIN files f ON f.oid = sy.oid WHERE f.project = ? AND sy.kind IN ('interface', 'interface_method', 'method')`, project)
	if err != nil {
		return err
	}
	var syms []symbolRow
	for rows.Next() {
		var fPath string
		var sy symbolRow
		if err := rows.Scan(&fPath, &sy.OID, &sy.Name, &sy.Recv, &sy.Kind, &sy.StartLine); err != nil {
			rows.Close()
			return err
		}
		if !isExcludedASTPath(fPath) {
			sy.Pkg = canonicalPkgDir(fPath)
			syms = append(syms, sy)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM sym_refs WHERE kind IN ('implements', 'iface_call') AND oid IN (SELECT oid FROM files WHERE project = ?)`, project); err != nil {
		return err
	}
	insStmt, err := s.db.Prepare(`INSERT INTO sym_refs(oid, from_sym, to_pkg, to_name, kind, confidence, weight, line) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insStmt.Close()
	for _, r := range matchImplicitInterfaces(syms) {
		if _, err := insStmt.Exec(r.OID, r.FromSym, r.ToPkg, r.ToName, r.Kind, r.Confidence, r.Weight, r.Line); err != nil {
			return err
		}
	}
	return nil
}
