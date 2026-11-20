package check

import (
	"errors"
	"io/fs"
	"regexp"
	"strings"
)

var (
	// componentID is an SDD component, M1.. (PLAN-2026-10-29-root-docs-and-fyr-serve D8).
	componentID = regexp.MustCompile(`^M[0-9]+$`)
	namesM      = regexp.MustCompile(`\bM[0-9]+\b`)
	specRefRe   = regexp.MustCompile(`SPEC-[a-z0-9-]+`)
	sepCellRe   = regexp.MustCompile(`^:?-{1,}:?$`)
	traceTestRe = regexp.MustCompile(`\bTest[A-Za-z0-9_]+\b`)
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
	compSet, comps, compFinds := sddComponents(sdd)
	sddTrace, sddFinds := sddTraceRows(sdd, compSet)
	out = append(out, sddFinds...)
	if prd, note := k.rootDoc("PRD.md", "requirements not traced"); note != "" {
		notes = append(notes, note)
	} else if items := requirementItems(prd); len(items) == 0 {
		notes = append(notes, "PRD.md has no Must or Should items: requirements not traced")
	} else {
		out = append(out, requirementTraces(items, sddTrace)...)
	}
	if len(comps) == 0 {
		return out, append(notes, "SDD.md has no M# component: components not traced")
	}
	out = append(out, compFinds...)
	tdd, note := k.rootDoc("TDD.md", "components not traced")
	if note != "" {
		return out, append(notes, note)
	}
	tddTrace, tddFinds := k.tddTraceRows(tdd)
	out = append(out, tddFinds...)
	for _, c := range comps {
		if _, ok := tddTrace[c.cells[0]]; !ok {
			out = append(out, Finding{"SDD.md", c.line, "trace", c.cells[0] + ": no row in TDD.md's trace"})
		}
	}
	return out, notes
}

func sddComponents(sdd string) (map[string]bool, []row, []Finding) {
	compSet := map[string]bool{}
	var comps []row
	var out []Finding
	for _, r := range tableRowList(sdd, "Components") {
		id := r.cells[0]
		if !componentID.MatchString(id) {
			continue
		}
		if compSet[id] {
			out = append(out, Finding{"SDD.md", r.line, "trace", id + ": duplicate row in SDD.md's components"})
			continue
		}
		compSet[id] = true
		comps = append(comps, r)
	}
	return compSet, comps, out
}

func sddTraceRows(sdd string, compSet map[string]bool) (map[string]row, []Finding) {
	rows := map[string]row{}
	var out []Finding
	for _, r := range tableRowList(sdd, "Trace") {
		key := r.cells[0]
		if key != "" {
			if _, seen := rows[key]; seen {
				out = append(out, Finding{"SDD.md", r.line, "trace", key + ": duplicate row in SDD.md's trace"})
			} else {
				rows[key] = r
			}
		}
		if len(compSet) > 0 && len(r.cells) > 1 {
			out = append(out, unknownComponentFindings(r, compSet)...)
		}
	}
	return rows, out
}

func unknownComponentFindings(r row, compSet map[string]bool) []Finding {
	var out []Finding
	seen := map[string]bool{}
	for _, id := range namesM.FindAllString(strings.Join(r.cells[1:], "|"), -1) {
		if seen[id] || compSet[id] {
			continue
		}
		seen[id] = true
		out = append(out, Finding{"SDD.md", r.line, "trace", id + ": unknown component in SDD.md's trace"})
	}
	return out
}

func (k *checker) tddTraceRows(tdd string) (map[string]row, []Finding) {
	rows := map[string]row{}
	var out []Finding
	for _, r := range tableRowList(tdd, "Trace") {
		key := r.cells[0]
		if key != "" {
			if _, seen := rows[key]; seen {
				out = append(out, Finding{"TDD.md", r.line, "trace", key + ": duplicate row in TDD.md's trace"})
			} else {
				rows[key] = r
			}
		}
		out = append(out, k.tddVerifiedFindings(r)...)
	}
	return rows, out
}

func (k *checker) tddVerifiedFindings(r row) []Finding {
	ver := ""
	if len(r.cells) >= 3 {
		ver = strings.TrimSpace(r.cells[2])
	}
	if ver == "" || ver == "-" || ver == "—" || strings.EqualFold(ver, "none") {
		return []Finding{{"TDD.md", r.line, "trace", r.cells[0] + ": empty Verified by in TDD.md's trace"}}
	}
	if !k.hasTrackedGoTests() {
		return nil
	}
	clean := placeholderRe.ReplaceAllString(ver, "")
	var out []Finding
	seen := map[string]bool{}
	for _, m := range traceTestRe.FindAllStringIndex(clean, -1) {
		if m[1] < len(clean) && clean[m[1]] == '*' {
			continue
		}
		tok := clean[m[0]:m[1]]
		if seen[tok] {
			continue
		}
		seen[tok] = true
		if !k.testDeclared(tok) {
			out = append(out, Finding{"TDD.md", r.line, "trace", tok + ": missing test in TDD.md's trace"})
		}
	}
	return out
}

func (k *checker) hasTrackedGoTests() bool {
	for _, p := range k.tracked {
		if strings.HasSuffix(p, "_test.go") {
			return true
		}
	}
	return false
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

// tableRowList is every data table row of text's `## <heading>` sections,
// the heading's text exactly, up to the next level-one or level-two heading;
// separator rows, header rows above a separator, and fenced lines are skipped.
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
		cells := splitTableRow(t)
		if isSeparatorRow(cells) {
			if len(out) > 0 && out[len(out)-1].line == n-1 {
				out = out[:len(out)-1]
			}
			return true
		}
		out = append(out, row{n, cells})
		return true
	})
	return out
}

func splitTableRow(line string) []string {
	t := strings.TrimPrefix(strings.TrimSpace(line), "|")
	if hasUnescapedTrailingPipe(t) {
		t = t[:len(t)-1]
	}
	var cells []string
	var buf strings.Builder
	for i := 0; i < len(t); i++ {
		if t[i] == '\\' && i+1 < len(t) && t[i+1] == '|' {
			buf.WriteByte('|')
			i++
			continue
		}
		if t[i] == '|' {
			cells = append(cells, strings.Trim(strings.TrimSpace(buf.String()), "`"))
			buf.Reset()
			continue
		}
		buf.WriteByte(t[i])
	}
	return append(cells, strings.Trim(strings.TrimSpace(buf.String()), "`"))
}

func hasUnescapedTrailingPipe(t string) bool {
	if !strings.HasSuffix(t, "|") {
		return false
	}
	slashes := 0
	for i := len(t) - 2; i >= 0 && t[i] == '\\'; i-- {
		slashes++
	}
	return slashes%2 == 0
}

func isSeparatorRow(cells []string) bool {
	hasDash := false
	for _, c := range cells {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if !sepCellRe.MatchString(c) {
			return false
		}
		hasDash = true
	}
	return hasDash
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
