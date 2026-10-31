package search

import (
	"database/sql"
)

// inserter holds the two insert statements, prepared once per reconcile:
// a full build runs some fifty thousand inserts.
type inserter struct{ chunk, ref *sql.Stmt }

func (s *store) newInserter() (*inserter, error) {
	var in inserter
	var err error
	if in.chunk, err = s.db.Prepare(`INSERT INTO chunks(oid, line, heading, body) VALUES (?, ?, ?, ?)`); err != nil {
		return nil, err
	}
	if in.ref, err = s.db.Prepare(`INSERT INTO refs(oid, line, target) VALUES (?, ?, ?)`); err != nil {
		in.close()
		return nil, err
	}
	return &in, nil
}

func (in *inserter) close() {
	for _, st := range []*sql.Stmt{in.chunk, in.ref} {
		if st != nil {
			st.Close()
		}
	}
}

func (in *inserter) content(e entry, text string) error {
	for _, c := range chunkText(e.path, text) {
		if _, err := in.chunk.Exec(e.oid, c.line, c.heading, c.body); err != nil {
			return err
		}
	}
	for _, r := range parseRefs(text) {
		if _, err := in.ref.Exec(e.oid, r.line, r.target); err != nil {
			return err
		}
	}
	return nil
}

// gc drops blobs no project's view references that were last seen more
// than gcAge ago, with everything derived from them; the chunks trigger
// keeps chunks_fts in step.
func (s *store) gc(now int64) error {
	cut := now - gcAge
	old := `SELECT oid FROM blobs WHERE last_seen < ? AND oid NOT IN (SELECT oid FROM files)`
	for _, t := range []string{"chunks", "refs"} {
		if _, err := s.db.Exec(`DELETE FROM `+t+` WHERE oid IN (`+old+`)`, cut); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(`DELETE FROM blobs WHERE oid IN (`+old+`)`, cut)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
