package search

import (
	"database/sql"

	"github.com/kesokaj/fyr/internal/sqliteutil"
)

// inserter holds the four insert statements, prepared once per reconcile:
// a full build runs some fifty thousand inserts.
type inserter struct{ chunk, ref, sym, symRef *sql.Stmt }

func (s *store) newInserter() (*inserter, error) {
	var in inserter
	var err error
	if in.chunk, err = s.db.Prepare(`INSERT INTO chunks(oid, line, heading, body, tokens) VALUES (?, ?, ?, ?, ?)`); err != nil {
		return nil, err
	}
	if in.ref, err = s.db.Prepare(`INSERT INTO refs(oid, line, target) VALUES (?, ?, ?)`); err != nil {
		in.close()
		return nil, err
	}
	if in.sym, err = s.db.Prepare(`INSERT INTO symbols(oid, name, recv, kind, pkg, start_line, end_line, sig, exported, body_hash) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`); err != nil {
		in.close()
		return nil, err
	}
	if in.symRef, err = s.db.Prepare(`INSERT INTO sym_refs(oid, from_sym, to_pkg, to_name, kind, confidence, weight, line) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`); err != nil {
		in.close()
		return nil, err
	}
	return &in, nil
}

func (in *inserter) close() {
	for _, st := range []*sql.Stmt{in.chunk, in.ref, in.sym, in.symRef} {
		if st != nil {
			st.Close()
		}
	}
}

func (in *inserter) content(e entry, text string) error {
	for _, c := range chunkText(e.path, text) {
		if _, err := in.chunk.Exec(e.oid, c.Line, c.Heading, c.Body, splitIdentTokens(e.path, c.Heading, c.Body)); err != nil {
			return err
		}
	}
	for _, r := range parseRefs(text) {
		if _, err := in.ref.Exec(e.oid, r.Line, r.Target); err != nil {
			return err
		}
	}
	syms, srefs := extractSymbolsAndRefsForIndex(e.path, text)
	for _, sy := range syms {
		if _, err := in.sym.Exec(e.oid, sy.Name, sy.Recv, sy.Kind, sy.Pkg, sy.StartLine, sy.EndLine, sy.Sig, sy.Exported, sy.BodyHash); err != nil {
			return err
		}
	}
	for _, sr := range srefs {
		if _, err := in.symRef.Exec(e.oid, sr.FromSym, sr.ToPkg, sr.ToName, sr.Kind, sr.Confidence, sr.Weight, sr.Line); err != nil {
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
	for _, t := range []string{"chunks", "refs", "symbols", "sym_refs"} {
		if _, err := s.db.Exec(`DELETE FROM `+t+` WHERE oid IN (`+old+`)`, cut); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(`DELETE FROM blobs WHERE oid IN (`+old+`)`, cut)
	return err
}

func boolInt(b bool) int { return sqliteutil.BoolInt(b) }
