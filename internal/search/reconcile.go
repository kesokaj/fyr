package search

import (
	"os"
	"path/filepath"
)

// maxFileBytes is the largest file indexed; larger files, and files with a
// NUL byte in their first 8 KiB, are skipped.
const (
	maxFileBytes = 1 << 20
	binarySniff  = 8 << 10
	gcAge        = 14 * 24 * 60 * 60 // seconds an unreferenced blob is kept
	touchEvery   = 24 * 60 * 60      // seconds between last_seen refreshes
)

// source is what apply needs to know about one project.
type source struct {
	project  string
	dir      string // where the view's paths are read from
	docsRoot string // "" for ~config
	view     []entry
}

// apply brings one project's rows up to its current view and returns how
// many paths changed. When nothing changed and last_seen is fresh it takes
// no write lock at all.
func (s *store) apply(src source) (int, error) {
	now := nowFunc().Unix()
	changed, err := s.diffCount(src)
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
	n, err := s.applyLocked(src, now)
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
	if n > 0 {
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

// trailExport is the brain store's committed export, relative to a checkout.
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
	files, blobs, err := s.current(src.project)
	if err != nil {
		return 0, err
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
	return n, nil
}

func (s *store) touchDue(project string, now int64) bool {
	var v int64
	err := s.db.QueryRow(`SELECT CAST(value AS INTEGER) FROM meta WHERE key = ?`, "touched:"+project).Scan(&v)
	return err != nil || now-v >= touchEvery
}

func (s *store) applyLocked(src source, now int64) (int, error) {
	files, blobs, err := s.current(src.project)
	if err != nil {
		return 0, err
	}
	if err := s.indexBlobs(src, blobs, now); err != nil {
		return 0, err
	}
	n, err := s.writeFiles(src, files, wanted(src.view, blobs))
	if err != nil {
		return 0, err
	}
	if _, err := s.db.Exec(`UPDATE blobs SET last_seen = ? WHERE oid IN (SELECT oid FROM files WHERE project = ?)`, now, src.project); err != nil {
		return 0, err
	}
	if _, err := s.db.Exec(`INSERT INTO meta(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, "touched:"+src.project, now); err != nil {
		return 0, err
	}
	return n, s.gc(now)
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
	var in *inserter
	for _, e := range src.view {
		if _, known := blobs[e.oid]; known {
			continue
		}
		text, ok := readIndexable(fsRoot, filepath.FromSlash(e.path))
		if _, err := s.db.Exec(`INSERT INTO blobs(oid, last_seen, skipped) VALUES (?, ?, ?)`, e.oid, now, boolInt(!ok)); err != nil {
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
		if err := in.content(e, text, src.docsRoot); err != nil {
			return err
		}
	}
	return nil
}

// writeFiles brings the project's files rows from files to w and returns
// how many paths were added or removed; a path whose blob is unchanged only
// has its state updated.
func (s *store) writeFiles(src source, files map[string]string, w map[string]entry) (int, error) {
	n := 0
	for p, oid := range files {
		if e, ok := w[p]; !ok || e.oid != oid {
			if _, err := s.db.Exec(`DELETE FROM files WHERE project = ? AND path = ?`, src.project, p); err != nil {
				return 0, err
			}
			n++
		}
	}
	for p, e := range w {
		if old, ok := files[p]; ok && old == e.oid {
			if _, err := s.db.Exec(`UPDATE files SET state = ? WHERE project = ? AND path = ? AND state != ?`, e.state, src.project, p, e.state); err != nil {
				return 0, err
			}
			continue
		}
		if _, ok := files[p]; !ok {
			n++
		}
		if _, err := s.db.Exec(`INSERT INTO files(project, path, oid, state) VALUES (?, ?, ?, ?)`, src.project, p, e.oid, e.state); err != nil {
			return 0, err
		}
	}
	return n, nil
}
