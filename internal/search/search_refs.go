package search

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

func (sc *scope) refs(o Options, out io.Writer) error {
	x := strings.TrimPrefix(strings.TrimSpace(o.Refs), "./")
	isID := idPattern.MatchString(x)
	isDir := !isID && strings.HasSuffix(x, "/")
	where, args := sc.refsWhere(x, isID, isDir)
	// The first line: the ID's own record, or the path's state.
	var err error
	switch {
	case isID:
		err = sc.recordLine(x, out)
	case isDir:
		fmt.Fprintf(out, "%s  directory\n", x)
	default:
		err = sc.pathLine(x, out)
	}
	if err != nil {
		return err
	}
	raw, err := sc.refRows(o, where, args)
	if err != nil {
		return err
	}
	groups := sc.groupRefs(raw)
	fmt.Fprintf(out, "referenced by %d:\n", len(groups))
	for _, g := range groups {
		times := ""
		if g.count > 1 {
			times = fmt.Sprintf("  ×%d", g.count)
		}
		fmt.Fprintf(out, "%s%s\n    %s\n", sc.label(g.project, g.path, g.oid, g.line), times, markTarget(sc.lineText(g.oid, g.line), g.target))
	}
	return nil
}

// refsWhere is the condition on r.target that --refs x matches, with its
// arguments: x itself and, for a path under a project's docs root, the
// path relative to that root as well.
func (sc *scope) refsWhere(x string, isID, isDir bool) (string, []any) {
	switch {
	case isID:
		return `r.target = ?`, []any{x}
	case isDir:
		base := strings.TrimSuffix(x, "/")
		where := `(r.target = ? OR r.target = ? OR r.target LIKE ? ESCAPE '\')`
		args := []any{base, x, likeEscape(x) + "%"}
		for _, p := range sc.projects {
			if rel, ok := docsRelative(base, p.docsRoot); ok {
				where = strings.TrimSuffix(where, ")") + ` OR r.target = ? OR r.target LIKE ? ESCAPE '\')`
				args = append(args, rel, likeEscape(rel+"/")+"%")
			}
		}
		return where, args
	}
	where, args := `r.target = ?`, []any{x}
	for _, p := range sc.projects {
		if rel, ok := docsRelative(x, p.docsRoot); ok {
			where = `(` + where + ` OR r.target = ?)`
			args = append(args, rel)
		}
	}
	return where, args
}

// recordLine prints the record x names, once per file that holds it, or
// that there is none.
func (sc *scope) recordLine(x string, out io.Writer) error {
	rows, err := sc.st.db.Query(`SELECT f.project, f.path, r.line, r.status, r.date, r.title FROM records r JOIN files f ON f.oid = r.oid
		WHERE r.id = ? AND f.project IN `+sc.in()+` ORDER BY f.project, f.path`, append([]any{x}, sc.keys()...)...)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var project, path, status, date, title string
		var line int
		if err := rows.Scan(&project, &path, &line, &status, &date, &title); err != nil {
			rows.Close()
			return err
		}
		found = true
		fmt.Fprintln(out, printable(fmt.Sprintf("%s  %s:%d  %s  %s  %s", x, sc.display(project, path), line, status, date, title)))
	}
	rows.Close()
	if !found {
		fmt.Fprintf(out, "%s  no record\n", x)
	}
	return nil
}

// pathLine prints the state of path x and, when a bare file name matches
// nothing, the tracked paths it could mean.
func (sc *scope) pathLine(x string, out io.Writer) error {
	state := "absent"
	err := sc.st.db.QueryRow(`SELECT state FROM files WHERE path = ? AND project IN `+sc.in()+` LIMIT 1`, append([]any{x}, sc.keys()...)...).Scan(&state)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	fmt.Fprintf(out, "%s  %s\n", x, state)
	if state == "absent" && !strings.Contains(x, "/") {
		// A bare file name matches nothing (no basename matching, D-row in
		// the plan): name the tracked paths it could mean instead.
		hints, err := sc.sameName(x)
		if err != nil {
			return err
		}
		for _, h := range hints {
			fmt.Fprintln(out, printable("hint: did you mean --refs "+h))
		}
	}
	return nil
}

// refRow is one reference: the file and line that hold it, and its target.
type refRow struct {
	project, path, oid, target string
	line                       int
}

// refRows reads the references matching where that --kind keeps. Its rows
// are closed before it returns: groupRefs queries again.
func (sc *scope) refRows(o Options, where string, args []any) ([]refRow, error) {
	rows, err := sc.st.db.Query(`SELECT f.project, f.path, r.oid, r.line, r.target FROM refs r JOIN files f ON f.oid = r.oid
		WHERE `+where+` AND f.project IN `+sc.in()+` ORDER BY f.project, f.path, r.line`, append(args, sc.keys()...)...)
	if err != nil {
		return nil, err
	}
	var raw []refRow
	for rows.Next() {
		var r refRow
		if err := rows.Scan(&r.project, &r.path, &r.oid, &r.line, &r.target); err != nil {
			rows.Close()
			return nil, err
		}
		if sc.kindMatches(o, r.project, r.path) {
			raw = append(raw, r)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	return raw, nil
}

// refGroup is the references of one file and record, counted.
type refGroup struct {
	project, path, oid, target, date string
	line, count                      int
}

// groupRefs groups raw per file and record, the newest record first. It
// runs after the rows are closed (the store has one connection, and a query
// inside an open row loop waits forever): in DECISIONS.md, ADR-041's
// "supersedes ADR-040" is its own line, not folded into ADR-040's own
// heading.
func (sc *scope) groupRefs(raw []refRow) []*refGroup {
	var groups []*refGroup
	index := map[string]*refGroup{}
	for _, r := range raw {
		recID, date := "", ""
		if rec := sc.recordAt(r.oid, r.line); rec != nil {
			recID, date = rec.id, rec.date
		}
		key := r.project + "\x00" + r.path + "\x00" + recID
		if prev, ok := index[key]; ok {
			prev.count++
			continue
		}
		g := &refGroup{project: r.project, path: r.path, oid: r.oid, target: r.target, date: date, line: r.line, count: 1}
		index[key] = g
		groups = append(groups, g)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].date != groups[j].date {
			return groups[i].date > groups[j].date
		}
		return groups[i].path < groups[j].path
	})
	return groups
}

// sameName lists up to five current paths whose last element is name.
func (sc *scope) sameName(name string) ([]string, error) {
	rows, err := sc.st.db.Query(`SELECT DISTINCT path FROM files WHERE path LIKE ? ESCAPE '\' AND project IN `+sc.in()+` ORDER BY path LIMIT 5`,
		append([]any{"%/" + likeEscape(name)}, sc.keys()...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (sc *scope) open(o Options, out io.Writer) error {
	rows, err := sc.st.db.Query(`SELECT f.project, f.path, r.id, r.line, r.status, r.date, r.title FROM records r JOIN files f ON f.oid = r.oid
		WHERE (r.status = 'open' OR r.status LIKE 'open:%') AND f.project IN `+sc.in()+`
		ORDER BY r.date DESC, f.project, f.path`, sc.keys()...)
	if err != nil {
		return err
	}
	defer rows.Close()
	n := 0
	var lines []string
	for rows.Next() {
		var project, path, id, status, date, title string
		var line int
		if err := rows.Scan(&project, &path, &id, &line, &status, &date, &title); err != nil {
			return err
		}
		if !sc.kindMatches(o, project, path) {
			continue
		}
		n++
		lines = append(lines, printable(fmt.Sprintf("%s:%d  %s %s %s %s %s", sc.display(project, path), line, sc.kind(project, path), id, status, date, title)))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	fmt.Fprintf(out, "search: %d open records (%s)\n", n, sc.status())
	for _, l := range lines {
		fmt.Fprintln(out, l)
	}
	return nil
}
