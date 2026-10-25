package brain

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	Remove   []string // every tracked trail file under the docs root, for `git rm`; a file left as a file is not on it
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

// The import's read caps, over the docs tree, the files that reference it
// and the old memory stores together: far above this repository's (16 MB,
// 1,700 files). Variables so a test can lower them (security review of the
// import, 2026-10-25, finding 6).
var (
	maxImportBytes int64 = 256 << 20
	maxImportFiles       = 50000
)

// budget is what one import may still read.
type budget struct {
	bytes int64
	files int
}

func newBudget() *budget { return &budget{bytes: maxImportBytes, files: maxImportFiles} }

func (b *budget) take(n int, what string) error {
	b.bytes -= int64(n)
	b.files--
	if b.bytes < 0 || b.files < 0 {
		return refuse("the import is over %d bytes or %d files (at %s); import a smaller docs root", maxImportBytes, maxImportFiles, what)
	}
	return nil
}

// readRegular reads a file whole, opened without following a link or
// blocking on a FIFO (openNonBlocking), refusing what is not a regular file
// or is over max bytes, by its size and while reading: a file swapped after
// a check is never read through (security review of the import, finding 7).
func readRegular(path string, max int64) ([]byte, error) {
	f, err := openNonBlocking(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, refuse("%s is not a regular file", path)
	}
	if fi.Size() > max {
		return nil, refuse("%s is over %d bytes", path, max)
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, refuse("%s is over %d bytes", path, max)
	}
	return data, nil
}

var shellPlain = regexp.MustCompile(`^[A-Za-z0-9._/@%+=:,][A-Za-z0-9._/@%+=:,-]*$`)

// ShellQuote is s as one shell word: as it is when it is plain, else in
// single quotes. The import prints paths a caller pastes into `git rm`, and
// git tracks names like `$(x).md` or `-rf` (security review of the import,
// finding 4).
func ShellQuote(s string) string {
	if shellPlain.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// checkRecord refuses a record the store's own rules would not take back:
// an exported kind goes through the load's parser itself, so the import's
// check and the load's are one function (security review of the import,
// finding 1); the others get the same field rules by hand.
func checkRecord(r Record) error {
	if !unexported[r.Kind] {
		line, err := encodeTrail([]Record{r})
		if err != nil {
			return err
		}
		_, err = parseLine(bytes.TrimSuffix(line, []byte("\n")), 1)
		return err
	}
	_, derr := time.Parse("2006-01-02", r.Date)
	_, uerr := time.Parse(time.RFC3339, r.Updated)
	switch {
	case !validID(r.Kind, r.ID):
		return refuse("id %q is not a %s id", r.ID, r.Kind)
	case !allowed(r.Kind, r.Status):
		return refuse("status %q is not one a %s takes", r.Status, r.Kind)
	case derr != nil || uerr != nil:
		return refuse("date %q or updated %q is not a date", r.Date, r.Updated)
	case r.Title == "" || len(r.Title) > maxTitle || !titleText(r.Title):
		return refuse("the title is not one line of text of at most %d bytes", maxTitle)
	case len(r.Body) > maxBody || !bodyText(r.Body):
		return refuse("the body is over 1 MiB or not text")
	}
	return nil
}

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
func readDocs(top, root string, b *budget) ([]docFile, error) {
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
		data, err := readRegular(abs, maxBody)
		if err != nil {
			if IsRefusal(err) {
				return nil, refuse("%s: %v; a record body is at most 1 MiB of a regular file", p, err)
			}
			return nil, err
		}
		if err := b.take(len(data), p); err != nil {
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
			adrs, err := splitADRs(body, f.path, today)
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
			left = append(left, f.path)
			continue
		}
		if !validID(c.rec.Kind, c.rec.ID) {
			return nil, nil, refuse("%s: %q is not a valid %s id", f.path, c.rec.ID, c.rec.Kind)
		}
		c.rec.Date, c.rec.Title, c.rec.Body, c.rec.Updated = date, oneLine(title), body, updated
		c.path, c.rel = f.path, f.rel
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
	return out, left, nil
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

// outsideText is what D8's closure starts from besides the kept records:
// the project's own files that stay files, as git tracks them (an ignored or
// untracked file is not the project's text), each read without following a
// link and within the import's budget.
func outsideText(top string, b *budget) ([]string, error) {
	out, err := trail.Command(top, "ls-files", "-z", "--", "REQUIREMENTS.md", "AGENTS.md", "README.md", "context", "skills", "agents", "rules").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var texts []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		data, err := readRegular(filepath.Join(top, filepath.FromSlash(p)), maxBody)
		if err != nil {
			continue // a link, a FIFO or a large asset: no reference is read from it
		}
		if err := b.take(len(data), p); err != nil {
			return nil, err
		}
		texts = append(texts, string(data))
	}
	return texts, nil
}

// keep applies D8: the roots, then everything they or the outside files
// reference, by path or by ID, transitively.
func keep(cands []candidate, outside []string) []candidate {
	byKey := map[string]int{}
	for i, c := range cands {
		byKey[c.rec.ID] = i
		byKey[c.rel] = i
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
// stores what D8 keeps, exports, and returns every trail file under the docs
// root for the caller to `git rm` by name, plus the old memory store's files
// it took. A dry run reads, decides and checks the same and writes nothing.
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
	b := newBudget()
	files, err := readDocs(top, root, b)
	if err != nil {
		return nil, err
	}
	now := o.now()
	cands, left, err := classify(files, now.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	outside, err := outsideText(top, b)
	if err != nil {
		return nil, err
	}
	kept := keep(cands, outside)
	res := &ImportResult{}
	leftAlone := map[string]bool{}
	for _, p := range left {
		leftAlone[p] = true
		res.Problems = append(res.Problems, p+": not in a trail directory this import knows; left as a file")
	}
	for _, f := range files {
		if !leftAlone[f.path] {
			res.Remove = append(res.Remove, f.path)
		}
	}
	res.Dropped = len(cands) - len(kept)
	mems, memFiles, memProblems, err := readMemoryStore(scope, b)
	if err != nil {
		return nil, err
	}
	res.Problems = append(res.Problems, memProblems...)
	res.Memory = memFiles

	var recs []Record
	for _, c := range kept {
		r := normalize(c.rec)
		r.Scope = scope
		// Checked before anything is written, in a dry run too: a record the
		// load would refuse fails the import here, never a fresh home later
		// (security review of the import, finding 1).
		if err := checkRecord(r); err != nil {
			return nil, refuse("%s (%s): %v", c.path, r.ID, err)
		}
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

// storedKey is a record's key in the store: two projects may each hold
// MEMORY-x.
func storedKey(scope, id string) string { return scope + "\x00" + id }

func filterStored(all []Imported, stored map[string]bool) []Imported {
	var out []Imported
	for _, r := range all {
		if stored[storedKey(r.rec.Scope, r.ID)] {
			out = append(out, r)
		}
	}
	return out
}

// insertNew stores every record whose ID its scope does not hold yet, in one
// transaction, and exports when export is set; it returns the keys
// (storedKey) it stored. The caller checked every record; the check runs
// again here as the last guard before the store.
func (s *session) insertNew(recs []Record, export bool) (map[string]bool, error) {
	stored := map[string]bool{}
	write := s.st.tx
	if export {
		write = s.write
	}
	err := write(func(c *sql.Conn) error {
		ctx := context.Background()
		for _, r := range recs {
			if err := checkRecord(r); err != nil {
				return fmt.Errorf("brain: import built a record the load would refuse: %s: %w", r.ID, err)
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
			stored[storedKey(r.Scope, r.ID)] = true
		}
		return nil
	})
	return stored, err
}

// readMemoryStore reads the old memory store at <root>/.agents/memory (the
// main worktree's, which every worktree shared) as memory records, with the
// trust checks the store always had (ADR-043): a symlinked or non-directory
// .agents or store is refused, and so is a store git tracks anything under (a
// committed memory is the repository's text, not the operator's). No store
// is no records. A file it cannot take is a problem, and only the files it
// took, with the store's index and lock, are listed for removal.
func readMemoryStore(root string, b *budget) ([]Imported, []string, []string, error) {
	if err := guardExportPath(root); err != nil {
		return nil, nil, nil, err
	}
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
	tracked, err := trail.Command(root, "ls-files", "--", ".agents/memory").Output()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("git ls-files .agents/memory: %w", err)
	}
	if len(bytes.TrimSpace(tracked)) > 0 {
		return nil, nil, nil, refuse("git tracks files under %s: a committed memory is the repository's text, never the operator's; not imported", store)
	}
	entries, err := os.ReadDir(store)
	if err != nil {
		return nil, nil, nil, err
	}
	var recs []Imported
	var files, problems []string
	for _, e := range entries {
		rel := filepath.ToSlash(filepath.Join(".agents", "memory", e.Name()))
		if e.Name() == "MEMORY.md" || e.Name() == ".lock" {
			if e.Type().IsRegular() {
				files = append(files, rel)
			}
			continue
		}
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".md") {
			problems = append(problems, rel+": not a memory file; left alone")
			continue
		}
		data, err := readRegular(filepath.Join(store, e.Name()), maxBody)
		if err != nil {
			problems = append(problems, rel+": "+err.Error()+"; not imported")
			continue
		}
		if err := b.take(len(data), rel); err != nil {
			return nil, nil, nil, err
		}
		text := strings.ReplaceAll(string(data), "\r\n", "\n")
		meta, body := frontmatter(text)
		name := strings.TrimSuffix(e.Name(), ".md")
		typ, modified := "project", ""
		for _, l := range strings.Split(text, "\n") {
			l = strings.TrimSpace(l)
			if t, ok := strings.CutPrefix(l, "type: "); ok && memoryTypes[t] {
				typ = t
			}
			if t, ok := strings.CutPrefix(l, "modified: "); ok {
				modified = t
			}
		}
		body = strings.TrimSpace(body)
		if !memorySlug.MatchString(name) || body == "" {
			problems = append(problems, rel+": not a memory the store takes (its name, or an empty body); not imported")
			continue
		}
		day, updated := "", ""
		if t, err := time.Parse(time.RFC3339, modified); err == nil {
			day, updated = t.UTC().Format("2006-01-02"), t.UTC().Format(time.RFC3339)
		} else if info, err := e.Info(); err == nil {
			day, updated = info.ModTime().UTC().Format("2006-01-02"), info.ModTime().UTC().Format(time.RFC3339)
		}
		r := normalize(Record{ID: "MEMORY-" + name, Kind: "memory", Status: "closed", Date: day, Title: meta["description"],
			Body: "type: " + typ + "\n\n" + body, Updated: updated})
		// Every field is valid by here (the slug, a real day, a normalized
		// title and body); insertNew checks the record once more.
		files = append(files, rel)
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

// ImportGlobal migrates ~/.config/fyr's logs into the global scope:
// feedback.jsonl as feedback records, retros.jsonl as retro records,
// proposals/**/*.md as proposal records, each with the project it is about;
// and every indexed project's old memory store into that project's scope. A
// line, file or store it cannot take is a problem, never fatal, and a source
// with a problem is not listed for removal. Records already stored are
// skipped, so it can run again after the logs grew.
func ImportGlobal(o Options, dryRun bool) (*ImportResult, error) {
	if !o.Global {
		return nil, usage("import --global migrates ~/.config/fyr's logs")
	}
	conf := filepath.Join(o.Home, harness.StateDir)
	res := &ImportResult{}
	b := newBudget()
	var recs []Record
	used := map[string]bool{}
	failed := map[string]bool{} // sources with a problem: kept, never listed for removal
	problem := func(source, msg string) {
		failed[source] = true
		res.Problems = append(res.Problems, msg)
	}
	add := func(r Record, source string) {
		r = normalize(r)
		// A -N suffix is tried until it names no ID this import already
		// gave: FOO.md, foo-2.md and foo.md are three records (round 2,
		// finding 1).
		if used[r.ID] {
			base := r.ID
			for n := 2; used[r.ID]; n++ {
				r.ID = fmt.Sprintf("%s-%d", base, n)
			}
		}
		used[r.ID] = true
		if err := checkRecord(r); err != nil {
			problem(source, fmt.Sprintf("%s: %v; not imported", source, err))
			return
		}
		recs = append(recs, r)
		res.Records = append(res.Records, Imported{ID: r.ID, Kind: r.Kind, Status: r.Status, Path: source, Project: r.Project, rec: r})
	}
	var sources []string

	for _, log := range []struct{ file, kind string }{{"feedback.jsonl", "feedback"}, {"retros.jsonl", "retro"}} {
		lines, long, err := readLogLines(filepath.Join(conf, log.file))
		if err != nil {
			problem(log.file, log.file+": "+err.Error()+"; not imported")
			continue
		}
		if lines == nil {
			continue
		}
		sources = append(sources, log.file)
		for _, n := range long {
			problem(log.file, fmt.Sprintf("%s line %d: over %d bytes; not imported", log.file, n, maxLine))
		}
		for i, l := range lines {
			if strings.TrimSpace(l) == "" {
				continue
			}
			src := fmt.Sprintf("%s line %d", log.file, i+1)
			var e map[string]any
			if err := json.Unmarshal([]byte(l), &e); err != nil {
				problem(log.file, src+": not one JSON object; not imported")
				continue
			}
			ts, _ := e["ts"].(string)
			t, err := time.Parse(time.RFC3339, ts)
			if err != nil {
				problem(log.file, fmt.Sprintf("%s: ts %q is not RFC 3339; not imported", src, ts))
				continue
			}
			project, _ := e["project"].(string)
			title := log.kind + " " + filepath.Base(project)
			if log.kind == "feedback" {
				corrected, _ := e["corrected"].(string)
				if corrected == "" {
					corrected, _ = e["user_quote"].(string)
				}
				title = corrected
			}
			// The line as written: indenting it first could multiply a
			// nested line's size before any cap is checked (round 2,
			// finding 2).
			add(Record{ID: fmt.Sprintf("%s-%s-%s", prefixes[log.kind], t.Format("2006-01-02-1504"), slugify(title)), Kind: log.kind,
				Status: "closed", Date: t.Format("2006-01-02"), Title: title, Body: l,
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
			rel = filepath.ToSlash(rel)
			data, err := readRegular(p, maxBody)
			if err != nil {
				problem(rel, rel+": "+err.Error()+"; not imported")
				return nil
			}
			if err := b.take(len(data), rel); err != nil {
				problem(rel, rel+": "+err.Error())
				return filepath.SkipAll
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
			fallback := time.Now().Format("2006-01-02")
			if info, err := e.Info(); err == nil {
				fallback = info.ModTime().Format("2006-01-02")
			}
			date := docDate(meta, base, fallback)
			title := heading(body)
			if title == "" {
				title = "proposal " + base
			}
			sources = append(sources, rel)
			add(Record{ID: "PROPOSAL-" + slugOf(strings.ReplaceAll(strings.TrimPrefix(strings.TrimSuffix(rel, ".md"), "proposals/"), "/", "-")),
				Kind: "proposal", Status: "queued", Date: date, Title: title, Body: body,
				Project: projectKey(meta["project"]), Updated: date + "T00:00:00Z"}, rel)
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
		mems, files, problems, err := readMemoryStore(scope, b)
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
	for _, src := range sources {
		if !failed[src] {
			res.Sources = append(res.Sources, src)
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

// readLogLines reads a JSONL log, opened as readRegular opens a file; nil
// when there is none. A line over maxLine is not kept: its number is
// returned in long and its place left empty, so the other lines keep their
// numbers and the import goes on (security review of the import, finding 8).
func readLogLines(p string) (lines []string, long []int, err error) {
	if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	f, err := openNonBlocking(p)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return nil, nil, refuse("%s is not a regular file", p)
	}
	br := bufio.NewReaderSize(&capped{r: f, left: maxTrail}, 64<<10)
	lines = []string{}
	for n := 1; ; n++ {
		var line []byte
		tooLong := false
		for {
			chunk, err := br.ReadSlice('\n')
			if !tooLong {
				if len(line)+len(chunk) > maxLine {
					tooLong, line = true, nil
				} else {
					line = append(line, chunk...)
				}
			}
			if errors.Is(err, bufio.ErrBufferFull) {
				continue
			}
			if err != nil && !errors.Is(err, io.EOF) {
				return nil, nil, err
			}
			if tooLong {
				long = append(long, n)
				lines = append(lines, "")
			} else if len(line) > 0 {
				lines = append(lines, strings.TrimRight(string(line), "\r\n"))
			}
			if errors.Is(err, io.EOF) {
				return lines, long, nil
			}
			break
		}
	}
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
