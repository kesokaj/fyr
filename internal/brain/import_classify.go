package brain

import (
	"path/filepath"
	"strings"
	"time"
)

// The kinds and ID prefixes of an old docs tree's directories; testing's
// guide and matrices are guides, everything else under testing/ a test log.
var dirKinds = map[string]string{
	"plans": "plan", "reviews": "review", "testing": "testing", "changes": "change",
	"handoffs": "handoff", "retros": "retro", "deploy": "deploy", "specs": "spec",
}

// guides are testing/ files that describe how to test, not one run.
var guides = map[string]string{"testing/guide.md": "GUIDE-testing"}

// ImportedID is the ID the import gives the trail file with this stem (its
// base name without .md) in this kind's directory: the kind's prefix, a
// dash, slugOf(stem). The metrics join keys its events to it, so a project's
// migration adds no duplicate (plan 2026-10-25-1754 D10). A -N suffix a
// later collision adds is not reproduced.
func ImportedID(kind, stem string) string { return prefixes[kind] + "-" + slugOf(stem) }

func slugOf(stem string) string {
	s := strings.Trim(slugBad.ReplaceAllString(strings.ToLower(stem), "-"), "-._")
	// 120, not the 128 an ID's slug may hold: room for a -N a collision adds.
	if len(s) > 120 {
		s = strings.TrimRight(s[:120], "-._")
	}
	return s
}

// docDate is a file's day: its frontmatter date, else its name's stamp,
// else the fallback; a date that is not a real day (2026-02-30) is skipped,
// never exported (security review of the import, finding 1).
func docDate(meta map[string]string, base, fallback string) string {
	if d := meta["date"]; realDay(d) {
		return d
	}
	if m := stampPrefix.FindStringSubmatch(base); m != nil && realDay(m[1]) {
		return m[1]
	}
	return fallback
}

func realDay(d string) bool {
	_, err := time.Parse("2006-01-02", d)
	return err == nil && dateRe.MatchString(d)
}

// bugIndex is INDEX.md read for what a bug file may not say: its row's
// status and prevention, and where a graduated bug's rule now lives.
type bugIndex struct {
	status, prevention, graduated map[string]string
}

func readBugIndex(text string) bugIndex {
	ix := bugIndex{status: map[string]string{}, prevention: map[string]string{}, graduated: map[string]string{}}
	graduated := false
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "## ") {
			graduated = strings.Contains(strings.ToLower(l), "graduated")
			continue
		}
		m := indexRow.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		cells := strings.Split(m[2], "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		switch {
		case graduated && len(cells) >= 2:
			ix.graduated[m[1]] = cells[len(cells)-1]
		case !graduated && len(cells) >= 5:
			ix.status[m[1]] = cells[2]
			ix.prevention[m[1]] = cells[len(cells)-1]
		}
	}
	return ix
}

// bugStatus maps a bug file's free-text status to the kind's values (D6).
func bugStatus(raw string) string {
	w := strings.ToLower(strings.Fields(raw + " x")[0])
	switch w {
	case "open", "fixed", "accepted", "obsolete", "graduated":
		return w
	}
	return "fixed"
}

// candidate is a record an old tree would import, before D8 decides.
type candidate struct {
	rec  Record
	path string // relative to the checkout
	rel  string // relative to the docs root, as trail text cites it
	root bool   // kept whatever references it (D8's first list)
}

// classify turns the tree into candidates, and returns the paths it leaves
// as files (outside every trail directory). bugs/INDEX.md is folded into the
// bugs; DECISIONS.md gives one ADR per section.
func classify(files []docFile, today string) ([]candidate, []string, error) {
	var ix bugIndex
	for _, f := range files {
		if f.rel == "bugs/INDEX.md" {
			ix = readBugIndex(f.text)
		}
	}
	var out []candidate
	var left []string
	newestHandoff := -1
	for _, f := range files {
		cs, isLeft, err := classifyFile(f, ix, today)
		if err != nil {
			return nil, nil, err
		}
		if isLeft {
			left = append(left, f.path)
			continue
		}
		for _, c := range cs {
			if c.rec.Kind == "handoff" && (newestHandoff < 0 || fileStem(c.path) > fileStem(out[newestHandoff].path)) {
				newestHandoff = len(out)
			}
			out = append(out, c)
		}
	}
	if newestHandoff >= 0 {
		out[newestHandoff].root = true
	}
	if err := uniqueIDs(out); err != nil {
		return nil, nil, err
	}
	return out, left, nil
}

// fileStem is a path's base name without .md.
func fileStem(p string) string { return strings.TrimSuffix(filepath.Base(p), ".md") }

// classifyFile is one file's candidates: none for INDEX.md, one per ADR for
// DECISIONS.md, else one; left reports a file outside every trail directory.
func classifyFile(f docFile, ix bugIndex, today string) (cs []candidate, left bool, err error) {
	meta, body := frontmatter(f.text)
	body = strings.TrimSpace(body)
	base := fileStem(f.rel)
	dir, _, _ := strings.Cut(f.rel, "/")
	date := docDate(meta, base, today)
	title := heading(body)
	if title == "" {
		title = base
	}
	var c candidate
	switch {
	case f.rel == "bugs/INDEX.md":
		return nil, false, nil
	case f.rel == "architecture/DECISIONS.md":
		adrs, err := splitADRs(body, f.path, today)
		return adrs, false, err
	case bugFile.MatchString(f.rel):
		c, title, body = bugCandidate(f.rel, meta, title, body, ix)
	case guides[f.rel] != "":
		c = candidate{rec: Record{ID: guides[f.rel], Kind: "guide", Status: "current"}, root: true}
	case dir == "testing" && strings.HasSuffix(base, "-matrix") && !stampPrefix.MatchString(base):
		c = candidate{rec: Record{ID: "GUIDE-" + slugOf(base), Kind: "guide", Status: "current"}, root: true}
	case dirKinds[dir] != "":
		c = dirCandidate(dirKinds[dir], f.rel, base, body)
	default:
		return nil, true, nil
	}
	if !validID(c.rec.Kind, c.rec.ID) {
		return nil, false, refuse("%s: %q is not a valid %s id", f.path, c.rec.ID, c.rec.Kind)
	}
	c.rec.Date, c.rec.Title, c.rec.Body, c.rec.Updated = date, oneLine(title), body, date+"T00:00:00Z"
	c.path, c.rel = f.path, f.rel
	return []candidate{c}, false, nil
}

// bugCandidate is a bug file's candidate, with the title and body it keeps:
// INDEX.md gives the status the file does not, and graduation; a closed bug
// keeps only its title and prevention rule.
func bugCandidate(rel string, meta map[string]string, title, body string, ix bugIndex) (candidate, string, string) {
	id := bugFile.FindStringSubmatch(rel)[1]
	status := bugStatus(meta["status"])
	if s, ok := ix.status[id]; ok && meta["status"] == "" {
		status = bugStatus(s)
	}
	if _, ok := ix.graduated[id]; ok {
		status = "graduated"
	}
	title = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(title, id), ":"))
	if title == "" {
		title = id
	}
	if status != "open" {
		// A closed bug keeps its title and prevention rule (D8): it is
		// the learning.
		prev := section(body, "Prevention")
		if prev == "" && ix.prevention[id] != "" {
			prev = "## Prevention\n" + ix.prevention[id]
		}
		body = "# " + id + ": " + title
		if prev != "" {
			body += "\n\n" + prev
		}
	}
	if g, ok := ix.graduated[id]; ok {
		body += "\n\nGraduated to: " + g
	}
	return candidate{rec: Record{ID: id, Kind: "bug", Status: status}, root: true}, title, body
}

// dirCandidate is the candidate of a file in kind's directory; a spec,
// a deploy guide and an open plan are kept as roots.
func dirCandidate(kind, rel, base, body string) candidate {
	stem := base
	if kind == "spec" {
		// specs/<app>/<topic>.md is SPEC-<app>-<topic>.
		stem = strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(rel, "specs/"), ".md"), "/", "-")
	}
	r := Record{ID: ImportedID(kind, stem), Kind: kind}
	switch kind {
	case "plan":
		r.Status = planStatus(body)
	case "handoff":
		r.Status = ""
	case "spec", "deploy":
		r.Status = "current"
	default:
		r.Status = "closed"
	}
	return candidate{rec: r, root: kind == "spec" || kind == "deploy" || (kind == "plan" && r.Status == "open")}
}

// uniqueIDs refuses two candidates of one ID.
func uniqueIDs(out []candidate) error {
	seen := map[string]string{}
	for _, c := range out {
		if p, dup := seen[c.rec.ID]; dup {
			return refuse("%s and %s both import as %s; rename one", p, c.path, c.rec.ID)
		}
		seen[c.rec.ID] = c.path
	}
	return nil
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "untitled"
	}
	return s
}

// splitADRs makes one record per "## ADR-NNN: Title (date) — Status"
// section of DECISIONS.md; the preamble before the first is dropped.
func splitADRs(body, path, today string) ([]candidate, error) {
	locs := adrHead.FindAllStringSubmatchIndex(body, -1)
	var out []candidate
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		id := body[loc[2]:loc[3]]
		head := body[loc[4]:loc[5]]
		date := today
		title := head
		if m := adrDate.FindStringSubmatchIndex(head); m != nil {
			if d := head[m[2]:m[3]]; realDay(d) {
				date = d
			}
			title = head[:m[0]]
		}
		status := "accepted"
		if m := adrSuper.FindStringSubmatch(head); m != nil {
			status = "superseded:" + m[1]
		}
		out = append(out, candidate{
			rec: Record{ID: id, Kind: "adr", Status: status, Date: date, Title: oneLine(title),
				Body: strings.TrimSpace(body[loc[0]:end]), Updated: date + "T00:00:00Z"},
			path: path, rel: "architecture/DECISIONS.md", root: true,
		})
	}
	return out, nil
}
