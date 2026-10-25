package brain

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// Imported is one record an import stores (or, in a dry run, would store).
type Imported struct {
	ID, Kind, Status, Path string // Path: the source file, relative to the checkout (or ~/.config/fyr)
	Project                string // a global record's project
	rec                    Record
}

// ImportResult is what an import did: the records, the source files to
// remove by name (D13: the import removes nothing itself), and what it read
// but could not take.
type ImportResult struct {
	Records  []Imported
	Remove   []string // every tracked file under the docs root, for `git rm`
	Memory   []string // the old memory stores' files, for `rm` by name (gitignored, never tracked)
	Sources  []string // ~/.config/fyr files a global import read, relative to it, for `rm` by name
	Skipped  int      // records the scope already holds
	Problems []string // lines or files left out, each with its reason
	Dropped  int      // trail files D8 drops (git history keeps them)
}

// docFile is one tracked file of an old docs tree.
type docFile struct {
	rel  string // relative to the docs root: "plans/x.md"
	path string // relative to the checkout: ".agents/docs/plans/x.md"
	text string
}

// The kinds and ID prefixes of an old docs tree's directories; testing's
// guide and matrices are guides, everything else under testing/ a test log.
var dirKinds = map[string]string{
	"plans": "plan", "reviews": "review", "testing": "testing", "changes": "change",
	"handoffs": "handoff", "retros": "retro", "deploy": "deploy", "specs": "spec",
}

var (
	bugFile     = regexp.MustCompile(`^bugs/(BUG-\d{3,9})-[^/]*\.md$`)
	adrHead     = regexp.MustCompile(`(?m)^## (ADR-\d{3,9}[a-z]?):[ \t]*(.*)$`)
	adrDate     = regexp.MustCompile(`\((\d{4}-\d{2}-\d{2})\)`)
	adrSuper    = regexp.MustCompile(`(?i)—\s*superseded by (ADR-\d{3,9}[a-z]?)\s*$`)
	stampPrefix = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})`)
	indexRow    = regexp.MustCompile(`^\|\s*(BUG-\d{3,9})\s*\|(.*)\|\s*$`)
	// A reference to a trail file, by path or by the ID it imports as.
	pathRef = regexp.MustCompile(`(?:plans|reviews|testing|changes|handoffs|retros|deploy|specs/[A-Za-z0-9_.-]+)/[A-Za-z0-9_.-]+\.md`)
	idRef   = regexp.MustCompile(`\b(?:PLAN|REVIEW|TEST|CHANGE|HANDOFF|RETRO|SPEC|DEPLOY|GUIDE)-[a-z0-9][a-z0-9._-]*[a-z0-9]`)
	slugBad = regexp.MustCompile(`[^a-z0-9._-]+`)
)

// guides are testing/ files that describe how to test, not one run.
var guides = map[string]string{"testing/guide.md": "GUIDE-testing"}

// docsRootOf resolves import's <docs-root>: the argument, relative to the
// working directory, or the checkout's .agents/docs, else its docs/.
func docsRootOf(dir, top, arg string) (string, error) {
	cands := []string{filepath.Join(top, ".agents", "docs"), filepath.Join(top, "docs")}
	if arg != "" {
		if !filepath.IsAbs(arg) {
			arg = filepath.Join(dir, arg)
		}
		cands = []string{filepath.Clean(arg)}
	}
	for _, c := range cands {
		rel, err := filepath.Rel(top, c)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			return "", refuse("%s is not a directory inside the checkout %s", c, top)
		}
		fi, err := os.Lstat(c)
		if errors.Is(err, os.ErrNotExist) && arg == "" {
			continue
		}
		if err != nil {
			return "", refuse("%s: %v", c, err)
		}
		if !fi.IsDir() {
			return "", refuse("%s is not a directory (a symlinked docs root is refused)", c)
		}
		return c, nil
	}
	return "", refuse("no .agents/docs/ or docs/ in %s: nothing to import", top)
}

// readDocs reads every file git tracks under root. A file that is not
// markdown is refused (D17: a script is not a record; move it first), as is
// a symlink.
func readDocs(top, root string) ([]docFile, error) {
	relRoot, _ := filepath.Rel(top, root)
	out, err := trail.Command(top, "ls-files", "-z", "--", filepath.ToSlash(relRoot)).Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files %s: %w", relRoot, err)
	}
	var files []docFile
	var notMD []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		abs := filepath.Join(top, filepath.FromSlash(p))
		fi, err := os.Lstat(abs)
		if errors.Is(err, os.ErrNotExist) {
			continue // deleted in the work tree; the dirty check refuses a real run
		}
		if err != nil {
			return nil, err
		}
		if !fi.Mode().IsRegular() {
			return nil, refuse("%s is not a regular file", p)
		}
		if !strings.HasSuffix(p, ".md") {
			notMD = append(notMD, p)
			continue
		}
		if fi.Size() > maxBody {
			return nil, refuse("%s is over 1 MiB; a record body is at most that", p)
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return nil, err
		}
		rel, _ := filepath.Rel(root, abs)
		files = append(files, docFile{rel: filepath.ToSlash(rel), path: p, text: strings.ReplaceAll(string(data), "\r\n", "\n")})
	}
	if len(notMD) > 0 {
		return nil, refuse("not a record (D17: move scripts out of the trail first): %s", strings.Join(notMD, ", "))
	}
	return files, nil
}

// frontmatter splits a leading --- block into its key: value lines and the
// text after it.
func frontmatter(text string) (map[string]string, string) {
	meta := map[string]string{}
	if !strings.HasPrefix(text, "---\n") {
		return meta, text
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return meta, text
	}
	for _, l := range strings.Split(text[4:4+end], "\n") {
		if k, v, ok := strings.Cut(l, ":"); ok && !strings.HasPrefix(l, " ") {
			meta[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return meta, text[4+end+5:]
}

// heading is the text of the first "# " line, or "".
func heading(body string) string {
	for _, l := range strings.Split(body, "\n") {
		if t, ok := strings.CutPrefix(l, "# "); ok {
			return strings.TrimSpace(t)
		}
	}
	return ""
}

// section returns the "## name" section of body, heading included, or "".
func section(body, name string) string {
	lines := strings.Split(body, "\n")
	start := -1
	for i, l := range lines {
		if start < 0 && strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(l, "## ")), name) && strings.HasPrefix(l, "## ") {
			start = i
			continue
		}
		if start >= 0 && strings.HasPrefix(l, "## ") {
			return strings.TrimSpace(strings.Join(lines[start:i], "\n"))
		}
	}
	if start < 0 {
		return ""
	}
	return strings.TrimSpace(strings.Join(lines[start:], "\n"))
}

func slugOf(stem string) string {
	s := strings.Trim(slugBad.ReplaceAllString(strings.ToLower(stem), "-"), "-._")
	if len(s) > 128 {
		s = strings.TrimRight(s[:128], "-._")
	}
	return s
}

// docDate is a file's day: its frontmatter date, else its name's stamp,
// else the fallback.
func docDate(meta map[string]string, base, fallback string) string {
	if d := meta["date"]; dateRe.MatchString(d) {
		return d
	}
	if m := stampPrefix.FindStringSubmatch(base); m != nil {
		return m[1]
	}
	return fallback
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
	path string
	root bool // kept whatever references it (D8's first list)
}

// classify turns the tree into candidates. bugs/INDEX.md is folded into the
// bugs; DECISIONS.md gives one ADR per section.
func classify(files []docFile, today string) ([]candidate, []string, error) {
	var ix bugIndex
	for _, f := range files {
		if f.rel == "bugs/INDEX.md" {
			ix = readBugIndex(f.text)
		}
	}
	var out []candidate
	var problems []string
	newestHandoff := -1
	for _, f := range files {
		meta, body := frontmatter(f.text)
		body = strings.TrimSpace(body)
		base := strings.TrimSuffix(filepath.Base(f.rel), ".md")
		dir, _, _ := strings.Cut(f.rel, "/")
		date := docDate(meta, base, today)
		title := heading(body)
		if title == "" {
			title = base
		}
		updated := date + "T00:00:00Z"
		var c candidate
		switch {
		case f.rel == "bugs/INDEX.md":
			continue
		case f.rel == "architecture/DECISIONS.md":
			adrs, err := splitADRs(body, f.path)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, adrs...)
			continue
		case bugFile.MatchString(f.rel):
			id := bugFile.FindStringSubmatch(f.rel)[1]
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
			c = candidate{rec: Record{ID: id, Kind: "bug", Status: status}, root: true}
		case guides[f.rel] != "":
			c = candidate{rec: Record{ID: guides[f.rel], Kind: "guide", Status: "current"}, root: true}
		case dir == "testing" && strings.HasSuffix(base, "-matrix") && !stampPrefix.MatchString(base):
			c = candidate{rec: Record{ID: "GUIDE-" + slugOf(base), Kind: "guide", Status: "current"}, root: true}
		case dirKinds[dir] != "":
			kind := dirKinds[dir]
			stem := base
			if kind == "spec" {
				// specs/<app>/<topic>.md is SPEC-<app>-<topic>.
				stem = strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(f.rel, "specs/"), ".md"), "/", "-")
			}
			r := Record{ID: prefixes[kind] + "-" + slugOf(stem), Kind: kind}
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
			c = candidate{rec: r, root: kind == "spec" || kind == "deploy" || (kind == "plan" && r.Status == "open")}
			if kind == "handoff" && (newestHandoff < 0 || base > strings.TrimSuffix(filepath.Base(out[newestHandoff].path), ".md")) {
				newestHandoff = len(out)
			}
		default:
			problems = append(problems, f.path+": not in a trail directory this import knows; left as a file")
			continue
		}
		if !validID(c.rec.Kind, c.rec.ID) {
			return nil, nil, refuse("%s: %q is not a valid %s id", f.path, c.rec.ID, c.rec.Kind)
		}
		c.rec.Date, c.rec.Title, c.rec.Body, c.rec.Updated = date, oneLine(title), body, updated
		c.path = f.path
		out = append(out, c)
	}
	if newestHandoff >= 0 {
		out[newestHandoff].root = true
	}
	seen := map[string]string{}
	for _, c := range out {
		if p, dup := seen[c.rec.ID]; dup {
			return nil, nil, refuse("%s and %s both import as %s; rename one", p, c.path, c.rec.ID)
		}
		seen[c.rec.ID] = c.path
	}
	return out, problems, nil
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
func splitADRs(body, path string) ([]candidate, error) {
	locs := adrHead.FindAllStringSubmatchIndex(body, -1)
	var out []candidate
	for i, loc := range locs {
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		id := body[loc[2]:loc[3]]
		head := body[loc[4]:loc[5]]
		date := "1970-01-01"
		title := head
		if m := adrDate.FindStringSubmatchIndex(head); m != nil {
			date = head[m[2]:m[3]]
			title = head[:m[0]]
		}
		status := "accepted"
		if m := adrSuper.FindStringSubmatch(head); m != nil {
			status = "superseded:" + m[1]
		}
		out = append(out, candidate{
			rec: Record{ID: id, Kind: "adr", Status: status, Date: date, Title: oneLine(title),
				Body: strings.TrimSpace(body[loc[0]:end]), Updated: date + "T00:00:00Z"},
			path: path, root: true,
		})
	}
	return out, nil
}

// outsideText is what D8's closure starts from besides the kept records:
// the project's own files that stay files.
func outsideText(top string) []string {
	var out []string
	read := func(p string) {
		fi, err := os.Lstat(p)
		if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxBody {
			return
		}
		if b, err := os.ReadFile(p); err == nil {
			out = append(out, string(b))
		}
	}
	for _, f := range []string{"REQUIREMENTS.md", "AGENTS.md", "README.md"} {
		read(filepath.Join(top, f))
	}
	for _, d := range []string{"context", "skills", "agents", "rules"} {
		root := filepath.Join(top, d)
		if fi, err := os.Lstat(root); err != nil || !fi.IsDir() {
			continue
		}
		filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
			if err == nil && e.Type().IsRegular() {
				read(p)
			}
			return nil
		})
	}
	return out
}

// keep applies D8: the roots, then everything they or the outside files
// reference, by path or by ID, transitively.
func keep(cands []candidate, outside []string) []candidate {
	byKey := map[string]int{}
	for i, c := range cands {
		byKey[c.rec.ID] = i
		// The path relative to the docs root, as trail text cites it.
		if j := strings.Index(c.path, "/docs/"); j >= 0 {
			byKey[c.path[j+len("/docs/"):]] = i
		} else if r, ok := strings.CutPrefix(c.path, "docs/"); ok {
			byKey[r] = i
		}
	}
	kept := make([]bool, len(cands))
	var queue []string
	for i, c := range cands {
		if c.root {
			kept[i] = true
			queue = append(queue, c.rec.Body)
		}
	}
	queue = append(queue, outside...)
	for len(queue) > 0 {
		text := queue[0]
		queue = queue[1:]
		refs := append(pathRef.FindAllString(text, -1), idRef.FindAllString(text, -1)...)
		for _, r := range refs {
			if i, ok := byKey[r]; ok && !kept[i] {
				kept[i] = true
				queue = append(queue, cands[i].rec.Body)
			}
		}
	}
	var out []candidate
	for i, c := range cands {
		if kept[i] {
			out = append(out, c)
		}
	}
	return out
}

// Import migrates a project's old docs tree into its scope (D8, D13): it
// stores what D8 keeps, exports, and returns every tracked file under the
// docs root for the caller to `git rm` by name, plus the old memory store's
// files. A dry run reads and decides the same and writes nothing.
func Import(o Options, docsArg string, dryRun bool) (*ImportResult, error) {
	if o.Global {
		return nil, usage("a project import takes no --global; the global logs are `fyr record import --global`")
	}
	scope, top, err := resolveProject(o.Dir)
	if err != nil {
		return nil, err
	}
	if err := guardExportPath(top); err != nil {
		return nil, err
	}
	root, err := docsRootOf(o.Dir, top, docsArg)
	if err != nil {
		return nil, err
	}
	if !dryRun {
		out, err := trail.Command(top, "status", "--porcelain").Output()
		if err != nil {
			return nil, fmt.Errorf("git status: %w", err)
		}
		if len(bytes.TrimSpace(out)) > 0 {
			return nil, refuse("the tree has uncommitted changes: commit or stash them first, so the import's removals are the only change")
		}
	}
	files, err := readDocs(top, root)
	if err != nil {
		return nil, err
	}
	now := o.now()
	cands, problems, err := classify(files, now.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	kept := keep(cands, outsideText(top))
	res := &ImportResult{Problems: problems}
	for _, f := range files {
		res.Remove = append(res.Remove, f.path)
	}
	res.Dropped = len(cands) - len(kept)
	mems, memFiles, memProblems, err := readMemoryStore(scope)
	if err != nil {
		return nil, err
	}
	res.Problems = append(res.Problems, memProblems...)
	res.Memory = memFiles

	var recs []Record
	for _, c := range kept {
		r := c.rec
		r.Scope = scope
		recs = append(recs, r)
		res.Records = append(res.Records, Imported{ID: r.ID, Kind: r.Kind, Status: r.Status, Path: c.path, rec: r})
	}
	for _, m := range mems {
		m.rec.Scope = scope
		recs = append(recs, m.rec)
		res.Records = append(res.Records, m)
	}
	if dryRun {
		return res, nil
	}
	st, err := openStore(o.Home)
	if err != nil {
		return nil, err
	}
	s := &session{st: st, scope: scope, top: top}
	defer s.close()
	stored, err := s.insertNew(recs, true)
	if err != nil {
		return nil, err
	}
	res.Skipped = len(res.Records) - len(stored)
	res.Records = filterStored(res.Records, stored)
	return res, nil
}

func filterStored(all []Imported, stored map[string]bool) []Imported {
	var out []Imported
	for _, r := range all {
		if stored[r.ID] {
			out = append(out, r)
		}
	}
	return out
}

// insertNew stores every record whose ID the scope does not hold yet, in
// one transaction, and exports when export is set; it returns the IDs it
// stored.
func (s *session) insertNew(recs []Record, export bool) (map[string]bool, error) {
	stored := map[string]bool{}
	write := s.st.tx
	if export {
		write = s.write
	}
	err := write(func(c *sql.Conn) error {
		ctx := context.Background()
		for _, r := range recs {
			r = normalize(r)
			if !validID(r.Kind, r.ID) || !allowed(r.Kind, r.Status) || !dateRe.MatchString(r.Date) || len(r.Body) > maxBody {
				return fmt.Errorf("brain: import built a record the load would refuse: %s %s %q %s", r.Kind, r.ID, r.Status, r.Date)
			}
			taken, err := exists(c, r.Scope, r.ID)
			if err != nil {
				return err
			}
			if taken {
				continue
			}
			if _, err := c.ExecContext(ctx, `INSERT INTO records(scope, id, kind, status, date, title, body, project, updated)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.Scope, r.ID, r.Kind, r.Status, r.Date, r.Title, r.Body, r.Project, r.Updated); err != nil {
				return err
			}
			stored[r.ID] = true
		}
		return nil
	})
	return stored, err
}

// readMemoryStore reads the old memory store at <root>/.agents/memory (the
// main worktree's, which every worktree shared) as memory records. No store
// is no records; a symlinked store or file is refused.
func readMemoryStore(root string) ([]Imported, []string, []string, error) {
	store := filepath.Join(root, ".agents", "memory")
	fi, err := os.Lstat(store)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	if !fi.IsDir() {
		return nil, nil, nil, refuse("%s is not a directory (a symlinked store is refused)", store)
	}
	entries, err := os.ReadDir(store)
	if err != nil {
		return nil, nil, nil, err
	}
	var recs []Imported
	var files, problems []string
	for _, e := range entries {
		rel := filepath.ToSlash(filepath.Join(".agents", "memory", e.Name()))
		if !e.Type().IsRegular() {
			if e.Name() != ".lock" {
				problems = append(problems, rel+": not a regular file; left alone")
			}
			continue
		}
		files = append(files, rel)
		if e.Name() == "MEMORY.md" || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.Size() > maxBody {
			problems = append(problems, rel+": unreadable or over 1 MiB; not imported")
			continue
		}
		data, err := os.ReadFile(filepath.Join(store, e.Name()))
		if err != nil {
			return nil, nil, nil, err
		}
		meta, body := frontmatter(strings.ReplaceAll(string(data), "\r\n", "\n"))
		name := strings.TrimSuffix(e.Name(), ".md")
		typ := "project"
		for _, l := range strings.Split(string(data), "\n") {
			if t, ok := strings.CutPrefix(strings.TrimSpace(l), "type: "); ok && memoryTypes[t] {
				typ = t
				break
			}
		}
		body = strings.TrimSpace(body)
		desc := oneLine(meta["description"])
		if !memorySlug.MatchString(name) || body == "" || !utf8Text(body) {
			problems = append(problems, rel+": not a memory the store takes (name, empty or binary body); not imported")
			continue
		}
		modified := meta["modified"]
		day := info.ModTime().UTC().Format("2006-01-02")
		updated := info.ModTime().UTC().Format(time.RFC3339)
		for _, l := range strings.Split(string(data), "\n") {
			if t, ok := strings.CutPrefix(strings.TrimSpace(l), "modified: "); ok {
				modified = t
			}
		}
		if t, err := time.Parse(time.RFC3339, modified); err == nil {
			day, updated = t.UTC().Format("2006-01-02"), t.UTC().Format(time.RFC3339)
		}
		r := Record{ID: "MEMORY-" + name, Kind: "memory", Status: "closed", Date: day, Title: desc,
			Body: "type: " + typ + "\n\n" + body, Updated: updated}
		recs = append(recs, Imported{ID: r.ID, Kind: "memory", Status: "closed", Path: rel, rec: r})
	}
	return recs, files, problems, nil
}

// normalize makes an imported record one the load takes back: control
// characters but tab and newline as "?", a title of at most maxTitle bytes
// on one line, an empty title or body named.
func normalize(r Record) Record {
	clean := func(s string) string {
		return strings.Map(func(c rune) rune {
			if (c < 0x20 && c != '\t' && c != '\n') || c == 0x7f {
				return '?'
			}
			return c
		}, strings.ToValidUTF8(s, "?"))
	}
	r.Body = clean(r.Body)
	if strings.TrimSpace(r.Body) == "" {
		r.Body = "(empty)"
	}
	t := oneLine(clean(r.Title))
	for len(t) > maxTitle {
		_, size := utf8.DecodeLastRuneInString(t)
		t = t[:len(t)-size]
	}
	r.Title = t
	return r
}

func utf8Text(s string) bool {
	return !strings.ContainsRune(s, 0) && strings.ToValidUTF8(s, "�") == s
}

// ImportGlobal migrates ~/.config/fyr's logs into the global scope:
// feedback.jsonl as feedback records, retros.jsonl as retro records,
// proposals/**/*.md as proposal records, each with the project it is about;
// and every indexed project's old memory store into that project's scope. A
// line or file it cannot take is reported, never fatal. Records already
// stored are skipped, so it can run again after the logs grew.
func ImportGlobal(o Options, dryRun bool) (*ImportResult, error) {
	if !o.Global {
		return nil, usage("import --global migrates ~/.config/fyr's logs")
	}
	conf := filepath.Join(o.Home, harness.StateDir)
	res := &ImportResult{}
	var recs []Record
	used := map[string]int{}
	uniq := func(id string) string {
		used[id]++
		if n := used[id]; n > 1 {
			return fmt.Sprintf("%s-%d", id, n)
		}
		return id
	}
	addRec := func(r Record, path string) {
		r.ID = uniq(r.ID)
		recs = append(recs, r)
		res.Records = append(res.Records, Imported{ID: r.ID, Kind: r.Kind, Status: r.Status, Path: path, Project: r.Project, rec: r})
	}

	for _, log := range []struct{ file, kind string }{{"feedback.jsonl", "feedback"}, {"retros.jsonl", "retro"}} {
		lines, err := readLogLines(filepath.Join(conf, log.file))
		if err != nil {
			return nil, err
		}
		if lines == nil {
			continue
		}
		res.Sources = append(res.Sources, log.file)
		for i, l := range lines {
			if strings.TrimSpace(l) == "" {
				continue
			}
			var e map[string]any
			if err := json.Unmarshal([]byte(l), &e); err != nil {
				res.Problems = append(res.Problems, fmt.Sprintf("%s line %d: not one JSON object; not imported", log.file, i+1))
				continue
			}
			ts, _ := e["ts"].(string)
			t, err := time.Parse(time.RFC3339, ts)
			if err != nil {
				res.Problems = append(res.Problems, fmt.Sprintf("%s line %d: ts %q is not RFC 3339; not imported", log.file, i+1, ts))
				continue
			}
			project, _ := e["project"].(string)
			title := log.kind + " " + filepath.Base(project)
			if log.kind == "feedback" {
				corrected, _ := e["corrected"].(string)
				if corrected == "" {
					corrected, _ = e["user_quote"].(string)
				}
				title = oneLine(corrected)
			}
			if len(title) > 200 {
				title = strings.ToValidUTF8(title[:200], "") + "…"
			}
			var pretty bytes.Buffer
			json.Indent(&pretty, []byte(l), "", "  ")
			addRec(Record{ID: fmt.Sprintf("%s-%s-%s", prefixes[log.kind], t.Format("2006-01-02-1504"), slugify(title)), Kind: log.kind,
				Status: "closed", Date: t.Format("2006-01-02"), Title: oneLine(title), Body: pretty.String(),
				Project: projectKey(project), Updated: t.UTC().Format(time.RFC3339)}, log.file)
		}
	}

	props := filepath.Join(conf, "proposals")
	if fi, err := os.Lstat(props); err == nil && fi.IsDir() {
		filepath.WalkDir(props, func(p string, e fs.DirEntry, err error) error {
			if err != nil || !e.Type().IsRegular() || !strings.HasSuffix(p, ".md") {
				return nil
			}
			rel, _ := filepath.Rel(conf, p)
			info, err := e.Info()
			if err != nil || info.Size() > maxBody {
				res.Problems = append(res.Problems, rel+": unreadable or over 1 MiB; not imported")
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil || !utf8Text(string(data)) {
				res.Problems = append(res.Problems, rel+": not text; not imported")
				return nil
			}
			meta, body := frontmatter(strings.ReplaceAll(string(data), "\r\n", "\n"))
			body = strings.TrimSpace(body)
			if body == "" {
				return nil
			}
			base := strings.TrimSuffix(filepath.Base(p), ".md")
			if base == "SKILL" {
				base = filepath.Base(filepath.Dir(p))
			}
			date := docDate(meta, base, info.ModTime().Format("2006-01-02"))
			title := heading(body)
			if title == "" {
				title = "proposal " + base
			}
			res.Sources = append(res.Sources, filepath.ToSlash(rel))
			addRec(Record{ID: "PROPOSAL-" + slugOf(strings.ReplaceAll(strings.TrimPrefix(filepath.ToSlash(strings.TrimSuffix(rel, ".md")), "proposals/"), "/", "-")),
				Kind: "proposal", Status: "queued", Date: date, Title: oneLine(title), Body: body,
				Project: projectKey(meta["project"]), Updated: date + "T00:00:00Z"}, filepath.ToSlash(rel))
			return nil
		})
	}

	projects, err := search.ListProjects(o.Home)
	if err != nil {
		res.Problems = append(res.Problems, "the global index's project list: "+err.Error())
	}
	for _, p := range projects {
		scope, _, err := trail.Roots(p.Root)
		if err != nil {
			continue
		}
		mems, files, problems, err := readMemoryStore(scope)
		if err != nil {
			res.Problems = append(res.Problems, scope+": "+err.Error())
			continue
		}
		res.Problems = append(res.Problems, problems...)
		for _, f := range files {
			res.Memory = append(res.Memory, filepath.Join(scope, f))
		}
		for _, m := range mems {
			m.rec.Scope = scope
			m.Path = filepath.Join(scope, m.Path)
			recs = append(recs, m.rec)
			res.Records = append(res.Records, m)
		}
	}
	for _, r := range recs {
		if !validID(r.Kind, r.ID) {
			return nil, fmt.Errorf("brain: import built an invalid id %q", r.ID)
		}
	}
	if dryRun {
		return res, nil
	}
	st, err := openStore(o.Home)
	if err != nil {
		return nil, err
	}
	s := &session{st: st}
	defer s.close()
	stored, err := s.insertNew(recs, false)
	if err != nil {
		return nil, err
	}
	res.Skipped = len(res.Records) - len(stored)
	res.Records = filterStored(res.Records, stored)
	return res, nil
}

// projectKey is a log's project as a global record keys it: the main
// worktree's root when the path is still a checkout, else the path as given.
func projectKey(p string) string {
	if p == "" {
		return ""
	}
	return canonicalProject(p)
}

// readLogLines reads a JSONL log; nil when there is none. A symlinked log
// is refused, as ~/.config/fyr's other files are.
func readLogLines(p string) ([]string, error) {
	fi, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, refuse("%s is not a regular file", p)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxBody)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	if out == nil {
		out = []string{}
	}
	return out, sc.Err()
}

// ImportSummary counts the records by kind, sorted, for the CLI.
func ImportSummary(res *ImportResult) string {
	n := map[string]int{}
	for _, r := range res.Records {
		n[r.Kind]++
	}
	var ks []string
	for k := range n {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	var parts []string
	for _, k := range ks {
		parts = append(parts, fmt.Sprintf("%s %d", k, n[k]))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}
