package search

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"go/ast"
	"go/token"
)

// inserter holds the four insert statements, prepared once per reconcile:
// a full build runs some fifty thousand inserts.
type inserter struct{ chunk, ref, sym, symRef *sql.Stmt }

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
		if _, err := in.chunk.Exec(e.oid, c.line, c.heading, c.body); err != nil {
			return err
		}
	}
	for _, r := range parseRefs(text) {
		if _, err := in.ref.Exec(e.oid, r.line, r.target); err != nil {
			return err
		}
	}
	syms, srefs := extractSymbolsAndRefs(e.path, text)
	for _, sy := range syms {
		if _, err := in.sym.Exec(e.oid, sy.name, sy.recv, sy.kind, sy.pkg, sy.startLine, sy.endLine, sy.sig, sy.exported, sy.bodyHash); err != nil {
			return err
		}
	}
	for _, sr := range srefs {
		if _, err := in.symRef.Exec(e.oid, sr.fromSym, sr.toPkg, sr.toName, sr.kind, sr.confidence, sr.weight, sr.line); err != nil {
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

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func dedupSymRefs(refs []symRefRow) []symRefRow {
	type key struct {
		from, pkg, name, kind string
		line                  int
	}
	seen := map[key]bool{}
	var out []symRefRow
	for _, r := range refs {
		if r.toName == "" {
			continue
		}
		k := key{r.fromSym, r.toPkg, r.toName, r.kind, r.line}
		if !seen[k] {
			seen[k] = true
			out = append(out, r)
		}
	}
	return out
}

func mergeSymbols(base, extra []symbolRow) []symbolRow {
	seen := map[string]bool{}
	for _, s := range base {
		seen[s.recv+"."+s.name] = true
	}
	for _, s := range extra {
		if !seen[s.recv+"."+s.name] {
			seen[s.recv+"."+s.name] = true
			base = append(base, s)
		}
	}
	return base
}

func specLineSpan(fset *token.FileSet, gd *ast.GenDecl, sp ast.Spec) (int, int) {
	if len(gd.Specs) == 1 {
		return fset.Position(gd.Pos()).Line, fset.Position(gd.End()).Line
	}
	return fset.Position(sp.Pos()).Line, fset.Position(sp.End()).Line
}

func sliceNodeText(fset *token.FileSet, start, end token.Pos, text string) string {
	s, e := fset.Position(start).Offset, fset.Position(end).Offset
	if s < 0 || e < s || e > len(text) {
		return ""
	}
	return text[s:e]
}

func hashNodeText(fset *token.FileSet, start, end token.Pos, text string) string {
	return hashString(sliceNodeText(fset, start, end, text))
}

func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}
