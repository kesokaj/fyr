package check

import (
	"errors"
	"io/fs"
	"regexp"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
)

var (
	// componentID is an SDD component, M1.. (PLAN-2026-10-29-root-docs-and-fyr-serve D8).
	componentID = regexp.MustCompile(`^M[0-9]+$`)
	namesM      = regexp.MustCompile(`\bM[0-9]+\b`)
	specRefRe   = regexp.MustCompile(`SPEC-[a-z0-9-]+`)
)

// tracesRule is rule (d) (PLAN-2026-10-29-root-docs-and-fyr-serve D40):
// every Must and Should item of PRD.md has a row in SDD.md's `## Trace`
// that names a component, and every component of SDD.md's `## Components`
// has a row in TDD.md's `## Trace`. A project without SDD.md is not checked;
// a half that cannot be read is a note, and the other half is still checked.
func (k *checker) tracesRule() ([]Finding, []string) {
	sdd, note := k.rootDoc("SDD.md", "traces not checked")
	if note != "" {
		return nil, []string{note}
	}
	var out []Finding
	var notes []string
	if prd, note := k.rootDoc("PRD.md", "requirements not traced"); note != "" {
		notes = append(notes, note)
	} else if items := requirementItems(prd); len(items) == 0 {
		notes = append(notes, "PRD.md has no Must or Should items: requirements not traced")
	} else {
		out = requirementTraces(items, tableRows(sdd, "Trace"))
	}
	var comps []row
	for _, r := range tableRowList(sdd, "Components") {
		if componentID.MatchString(r.cells[0]) {
			comps = append(comps, r)
		}
	}
	if len(comps) == 0 {
		return out, append(notes, "SDD.md has no M# component: components not traced")
	}
	tdd, note := k.rootDoc("TDD.md", "components not traced")
	if note != "" {
		return out, append(notes, note)
	}
	tddTrace := tableRows(tdd, "Trace")
	for _, c := range comps {
		if _, ok := tddTrace[c.cells[0]]; !ok {
			out = append(out, Finding{"SDD.md", c.line, "trace", c.cells[0] + ": no row in TDD.md's trace"})
		}
	}
	return out, notes
}

// requirementTraces is rule (d)'s first half: each item's row in SDD.md's
// trace, which names a component.
func requirementTraces(items []item, trace map[string]row) []Finding {
	var out []Finding
	for _, it := range items {
		r, ok := trace[it.id]
		switch {
		case !ok:
			out = append(out, Finding{"PRD.md", it.line, "trace", it.id + ": no row in SDD.md's trace"})
		case !namesM.MatchString(strings.Join(r.cells[1:], "|")):
			out = append(out, Finding{"PRD.md", it.line, "trace", it.id + ": SDD.md's trace row names no component"})
		}
	}
	return out
}

// rootDoc reads a root file for rule (d), or says in a note, ending with
// what, why it was not read.
func (k *checker) rootDoc(name, what string) (string, string) {
	data, err := k.readFile(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", "no " + name + ": " + what
	case err != nil:
		return "", name + " is not a regular file of at most 16 MiB: " + what
	}
	return string(data), ""
}

// row is a Markdown table row: its 1-based line and its cells, trimmed,
// with the backticks around each dropped.
type row struct {
	line  int
	cells []string
}

// tableRowList is every table row of text's `## <heading>` sections, the
// heading's text exactly, up to the next level-one or level-two heading;
// lines in a fence are not read.
func tableRowList(text, heading string) []row {
	var out []row
	in := false
	unfenced(text, func(n int, l string) bool {
		if strings.HasPrefix(l, "# ") || strings.HasPrefix(l, "## ") {
			in = strings.TrimSpace(strings.TrimPrefix(l, "## ")) == heading
			return true
		}
		t := strings.TrimSpace(l)
		if !in || !strings.HasPrefix(t, "|") {
			return true
		}
		cells := strings.Split(strings.Trim(t, "|"), "|")
		for i, c := range cells {
			cells[i] = strings.Trim(strings.TrimSpace(c), "`")
		}
		out = append(out, row{n, cells})
		return true
	})
	return out
}

// tableRows is tableRowList keyed by each row's first cell; the first row
// with a key wins.
func tableRows(text, heading string) map[string]row {
	rows := map[string]row{}
	for _, r := range tableRowList(text, heading) {
		if _, seen := rows[r.cells[0]]; !seen {
			rows[r.cells[0]] = r
		}
	}
	return rows
}

// unfenced calls f with each 1-based line of text that is neither in a fence
// nor a fence marker, until f returns false. The fence rule is scan's (same
// character, a run at least as long, nothing after it).
func unfenced(text string, f func(n int, l string) bool) {
	var fenceChar byte
	fenceLen := 0
	for i, l := range strings.Split(text, "\n") {
		l = strings.TrimSuffix(l, "\r")
		if c, k, rest, ok := fenceMarker(l); ok {
			if fenceLen == 0 && (c != '`' || !strings.Contains(rest, "`")) {
				fenceChar, fenceLen = c, k
				continue
			}
			if fenceLen > 0 && c == fenceChar && k >= fenceLen && strings.TrimSpace(rest) == "" {
				fenceLen = 0
				continue
			}
		}
		if fenceLen > 0 {
			continue
		}
		if !f(i+1, l) {
			return
		}
	}
}

// specRefsFindings checks unfenced SPEC-* citations in SDD.md and TDD.md
// against the trail records when a trail is present (ok == true).
func (k *checker) specRefsFindings(recs []brain.Record, ok bool) []Finding {
	if !ok {
		return nil
	}
	status := make(map[string]string, len(recs))
	for _, r := range recs {
		if r.Kind == "spec" {
			status[r.ID] = r.Status
		}
	}
	var out []Finding
	for _, docName := range []string{"SDD.md", "TDD.md"} {
		data, err := k.readFile(docName)
		if err != nil {
			continue
		}
		out = append(out, docSpecRefFindings(docName, string(data), status)...)
	}
	return out
}

func docSpecRefFindings(docName, text string, status map[string]string) []Finding {
	var out []Finding
	unfenced(text, func(n int, l string) bool {
		seen := map[string]bool{}
		for _, id := range lineSpecRefs(l) {
			if seen[id] {
				continue
			}
			seen[id] = true
			if st, found := status[id]; !found {
				out = append(out, Finding{docName, n, "trace", id + ": missing spec record"})
			} else if st != "current" {
				out = append(out, Finding{docName, n, "trace", id + ": spec status is " + st + ", want current"})
			}
		}
		return true
	})
	return out
}

func lineSpecRefs(l string) []string {
	var ids []string
	for _, m := range specRefRe.FindAllStringIndex(l, -1) {
		start, end := m[0], m[1]
		if start > 0 && isSpecIDChar(l[start-1]) {
			continue
		}
		id := l[start:end]
		if strings.HasSuffix(id, "-") || (end < len(l) && (l[end] == '*' || isSpecIDChar(l[end]))) {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

func isSpecIDChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '-'
}
