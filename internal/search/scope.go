package search

import (
	"fmt"
	"strings"
	"time"
)

// proj is one project a query covers.
type proj struct {
	key, name, docsRoot string
}

// scope is an open, reconciled index and the projects a query reads.
type scope struct {
	st       *store
	projects []proj
	byKey    map[string]proj
	global   bool
	files    int
	changed  int
	elapsed  time.Duration
	leftOut  []string // directories a noindex marker takes out (project scope only)
}

func (sc *scope) close() {
	if sc.st != nil {
		sc.st.close()
	}
}

func (sc *scope) keys() []any {
	out := make([]any, len(sc.projects))
	for i, p := range sc.projects {
		out[i] = p.key
	}
	return out
}

func (sc *scope) in() string {
	return "(" + strings.TrimSuffix(strings.Repeat("?,", len(sc.projects)), ",") + ")"
}

func (sc *scope) status() string {
	s := fmt.Sprintf("index current: %d files, reconciled %d changed in %d ms", sc.files, sc.changed, sc.elapsed.Milliseconds())
	if len(sc.leftOut) > 0 {
		// The names are the project's own text: printable, like every hit.
		s += "; left out by a noindex marker: " + printable(strings.Join(sc.leftOut, ", "))
	}
	return s
}

func (sc *scope) display(project, path string) string {
	if !sc.global {
		return path
	}
	return sc.byKey[project].name + ":" + path
}

func (sc *scope) kind(project, path string) string {
	return kindOf(project, path, sc.byKey[project].docsRoot)
}

func (sc *scope) kindMatches(o Options, project, path string) bool {
	if o.Kind == "" {
		return true
	}
	if o.Kind == "trail" {
		_, ok := docsRelative(path, sc.byKey[project].docsRoot)
		return ok && project != configProject
	}
	return sc.kind(project, path) == o.Kind
}

// recordAt is the record a hit at line belongs to: a file's only record, or
// in a file of several (DECISIONS.md) the nearest one at or above the line.
func (sc *scope) recordAt(oid string, line int) *record {
	rows, err := sc.st.db.Query(`SELECT id, line, title, status, date FROM records WHERE oid = ? ORDER BY line`, oid)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var all []record
	for rows.Next() {
		var r record
		if rows.Scan(&r.id, &r.line, &r.title, &r.status, &r.date) == nil {
			all = append(all, r)
		}
	}
	if len(all) == 1 {
		return &all[0]
	}
	var best *record
	for i := range all {
		if all[i].line <= line {
			best = &all[i]
		}
	}
	return best
}

func (sc *scope) label(project, path, oid string, line int) string {
	return printable(sc.rawLabel(project, path, oid, line))
}

func (sc *scope) rawLabel(project, path, oid string, line int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s:%d  %s", sc.display(project, path), line, sc.kind(project, path))
	if r := sc.recordAt(oid, line); r != nil {
		if rel, _ := docsRelative(path, sc.byKey[project].docsRoot); r.id != rel {
			b.WriteString(" " + r.id)
		}
		if r.status != "" {
			b.WriteString(" " + r.status)
		}
		if r.date != "" {
			b.WriteString("  " + r.date)
		}
	}
	return b.String()
}
