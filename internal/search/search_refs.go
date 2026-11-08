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
	where, args := refsWhere(x, isDir)
	// The first line is a path's state; an ID's own record is the brain's.
	switch {
	case isID:
	case isDir:
		fmt.Fprintf(out, "%s  directory\n", x)
	default:
		if err := sc.pathLine(x, out); err != nil {
			return err
		}
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
		fmt.Fprintf(out, "%s%s\n    %s\n", sc.label(g.project, g.path, g.line), times, markTarget(sc.lineText(g.oid, g.line), g.target))
	}
	return nil
}

// refsWhere is the condition on r.target that --refs x matches, with its
// arguments.
func refsWhere(x string, isDir bool) (string, []any) {
	if isDir {
		base := strings.TrimSuffix(x, "/")
		return `(r.target = ? OR r.target = ? OR r.target LIKE ? ESCAPE '\')`, []any{base, x, likeEscape(x) + "%"}
	}
	return `r.target = ?`, []any{x}
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

// refGroup is the references of one file, counted.
type refGroup struct {
	project, path, oid, target string
	line, count                int
}

// groupRefs groups raw per file, sorted by path.
func (sc *scope) groupRefs(raw []refRow) []*refGroup {
	var groups []*refGroup
	index := map[string]*refGroup{}
	for _, r := range raw {
		key := r.project + "\x00" + r.path
		if prev, ok := index[key]; ok {
			prev.count++
			continue
		}
		g := &refGroup{project: r.project, path: r.path, oid: r.oid, target: r.target, line: r.line, count: 1}
		index[key] = g
		groups = append(groups, g)
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].path < groups[j].path })
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

func (sc *scope) linkedRecordsForPaths(paths map[string]bool, brainRefs func(string) []LinkedRecord) []LinkedRecord {
	if len(paths) == 0 {
		return nil
	}
	byID := map[string]LinkedRecord{}
	var pathList []string
	for p := range paths {
		pathList = append(pathList, p)
	}
	sort.Strings(pathList)
	for _, p := range pathList {
		if brainRefs != nil {
			for _, r := range brainRefs(p) {
				byID[r.ID] = r
			}
		}
		sc.collectFileIDRefs(p, byID)
	}
	if brainRefs != nil {
		for id := range byID {
			for _, r := range brainRefs(id) {
				byID[r.ID] = r
			}
		}
	}
	var out []LinkedRecord
	for _, r := range byID {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (sc *scope) collectFileIDRefs(p string, byID map[string]LinkedRecord) {
	q := `SELECT DISTINCT r.target, r.line FROM refs r JOIN files f ON f.oid = r.oid
		WHERE f.project IN ` + sc.in() + ` AND f.path = ? ORDER BY r.target`
	rows, err := sc.st.db.Query(q, append(sc.keys(), p)...)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var target string
		var line int
		if err := rows.Scan(&target, &line); err == nil && idPattern.MatchString(target) {
			if _, ok := byID[target]; !ok {
				byID[target] = LinkedRecord{ID: target, Status: "ref", Title: fmt.Sprintf("referenced at %s:%d", p, line)}
			}
		}
	}
}

func (sc *scope) coChangeHintsForPaths(paths map[string]bool) []string {
	if len(paths) == 0 || sc.st == nil {
		return nil
	}
	rows, err := sc.st.db.Query(`SELECT file_a, file_b, kind, count FROM co_activity WHERE count >= 3 ORDER BY count DESC, file_a, file_b`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a, b, kind string
		var count int
		if err := rows.Scan(&a, &b, &kind, &count); err == nil && (paths[a] || paths[b]) {
			out = append(out, fmt.Sprintf("%s <-> %s (%s: %d)", a, b, kind, count))
		}
	}
	return out
}
