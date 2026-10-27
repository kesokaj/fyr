package brain

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"

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
		// trail.Dirty, never `git status`, which runs the checkout's own
		// clean filters (SD38 of the 2026-10-27 audit).
		dirty, err := trail.Dirty(top)
		if err != nil {
			return nil, fmt.Errorf("checking for uncommitted changes: %w", err)
		}
		if dirty {
			return nil, refuse("the tree has uncommitted changes: commit or stash them first, so the import's removals are the only change")
		}
	}
	b := newBudget()
	files, keeps, err := readDocs(top, root, b)
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
	res.Remove = append(res.Remove, keeps...)
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
