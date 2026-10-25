package search

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

// Options are the flags of `fyr search` and `fyr index`.
type Options struct {
	Dir     string   // working directory the command runs in
	Home    string   // home directory (--home), for the global search index
	Words   []string // search words
	Refs    string   // --refs target
	Open    bool     // --open
	Kind    string   // --kind filter
	Limit   int      // --limit
	Global  bool     // --global
	Rebuild bool     // index --rebuild
}

const (
	defaultLimit = 10
	maxLimit     = 50
	perFileCap   = 2
	snippetMax   = 200
)

var (
	errNotCheckout = errors.New("search: not inside a git checkout; use grep")
	errNotIgnored  = errors.New(`search: .agents/scratch/search.db is not gitignored; add ".agents/scratch/*" to .gitignore (/init-project does)`)
	idPattern      = regexp.MustCompile(`^(BUG|ADR)-\d{3}[a-z]?$`)
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

// usageError is a refusal of the verb's arguments: the CLI prints it on the
// caller's writer and exits 2, like every other usage error (BUG-168).
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

// IsUsage reports whether err is a usage error from Search or Index.
func IsUsage(err error) bool {
	var u usageError
	return errors.As(err, &u)
}

func validate(o *Options, verb string) error {
	if o.Limit == 0 {
		o.Limit = defaultLimit
	}
	if o.Limit < 1 || o.Limit > maxLimit {
		return usageError{fmt.Sprintf("search: --limit must be between 1 and %d; see fyr help", maxLimit)}
	}
	if o.Kind != "" {
		ok := false
		for _, k := range kinds {
			ok = ok || k == o.Kind
		}
		if !ok {
			return usageError{fmt.Sprintf("search: --kind must be one of: %s; see fyr help", strings.Join(kinds, " "))}
		}
	}
	// index takes no words and none of search's modes, and search no
	// --rebuild: the verb table refuses them before they reach here.
	if verb == "index" {
		return nil
	}
	modes := 0
	for _, set := range []bool{len(o.Words) > 0, o.Refs != "", o.Open} {
		if set {
			modes++
		}
	}
	switch {
	case modes > 1:
		return usageError{"search: words, --refs and --open exclude each other; see fyr help"}
	case modes == 0:
		return usageError{"search: give words, --refs <BUG-NNN|ADR-NNN|path> or --open; see fyr help"}
	}
	return nil
}

// projectDB is where a checkout's index lives.
func projectDB(root string) string {
	return filepath.Join(root, ".agents", "scratch", "search.db")
}

func globalDB(home string) string {
	return filepath.Join(home, harness.StateDir, "search.db")
}

// guardProject refuses what the index must not write through or leave
// unignored.
func guardProject(root string) error {
	for _, d := range []string{".agents", filepath.Join(".agents", "scratch")} {
		if fi, err := os.Lstat(filepath.Join(root, d)); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("search: %s is a symlink; refusing to write through it", filepath.ToSlash(d))
		}
	}
	cmd := trail.Command(root, "check-ignore", "-q", ".agents/scratch/search.db")
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ee) && ee.ExitCode() == 1:
		return errNotIgnored
	default:
		return fmt.Errorf("search: git check-ignore: %w", err)
	}
}

// prepare opens and reconciles the index a query reads: the checkout's
// own, or with --global the global search index, after registering the
// checkout (when there is one) there.
func prepare(o Options, out io.Writer) (*scope, error) {
	start := time.Now()
	root, rootErr := checkoutRoot(o.Dir)
	if rootErr != nil && !o.Global {
		return nil, errNotCheckout
	}
	sc := &scope{byKey: map[string]proj{}}
	if rootErr == nil {
		if err := guardProject(root); err != nil {
			return nil, err
		}
		path := projectDB(root)
		if o.Rebuild && !o.Global {
			if err := removeDB(path); err != nil {
				return nil, fmt.Errorf("search: %w", err)
			}
		}
		st, err := openStore(path, out)
		if err != nil {
			return nil, err
		}
		docs := docsRootOf(root)
		view, leftOut, err := gitView(root, "")
		if err != nil {
			st.close()
			return nil, fmt.Errorf("search: %w", err)
		}
		n, err := st.apply(source{project: root, dir: root, docsRoot: docs, view: view})
		if err != nil {
			st.close()
			return nil, err
		}
		if _, err := st.db.Exec(`INSERT OR IGNORE INTO projects(root, name, docs_root, last_seen) VALUES (?, ?, ?, ?)`, root, filepath.Base(root), docs, nowFunc().Unix()); err != nil {
			st.close()
			return nil, err
		}
		sc.changed += n
		p := proj{key: root, name: filepath.Base(root), docsRoot: docs}
		if !o.Global {
			sc.st, sc.projects = st, []proj{p}
			sc.leftOut = leftOut
			sc.byKey[root] = p
		} else {
			st.close()
		}
		// Only a checkout the harness scaffolded registers: a third-party
		// clone's docs/ would otherwise answer every --global query and
		// feed /retro's cross-project evidence (security review, finding 2).
		if fi, err := os.Stat(filepath.Join(root, ".agents", "docs")); err == nil && fi.IsDir() {
			if err := register(o.Home, root, docs, out); err != nil {
				sc.close()
				return nil, err
			}
		}
	}
	if o.Global {
		st, n, projects, err := reconcileGlobal(o, out)
		if err != nil {
			return nil, err
		}
		sc.st, sc.projects, sc.global = st, projects, true
		sc.changed += n
		for _, p := range projects {
			sc.byKey[p.key] = p
		}
	}
	if err := sc.st.db.QueryRow(`SELECT count(*) FROM files WHERE project IN `+sc.in(), sc.keys()...).Scan(&sc.files); err != nil {
		sc.close()
		return nil, err
	}
	sc.elapsed = time.Since(start)
	return sc, nil
}

// register records a checkout in the global search index, at most once a
// day per checkout.
func register(home, root, docs string, out io.Writer) error {
	st, err := openStore(globalDB(home), out)
	if err != nil {
		return err
	}
	defer st.close()
	now := nowFunc().Unix()
	var last int64
	err = st.db.QueryRow(`SELECT last_seen FROM projects WHERE root = ?`, root).Scan(&last)
	if err == nil && now-last < touchEvery {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = st.db.Exec(`INSERT INTO projects(root, name, docs_root, last_seen) VALUES (?, ?, ?, ?)
		ON CONFLICT(root) DO UPDATE SET docs_root = excluded.docs_root, last_seen = excluded.last_seen`,
		root, filepath.Base(root), docs, now)
	if isBusy(err) {
		return errBusy
	}
	return err
}

// reconcileGlobal brings the global search index up to date: the files of
// ~/.config/fyr/, then the trail of every registered checkout, dropping
// any that is gone.
func reconcileGlobal(o Options, out io.Writer) (*store, int, []proj, error) {
	path := globalDB(o.Home)
	if o.Rebuild {
		dropped := 0
		if st, err := tryOpen(path); err == nil {
			st.db.QueryRow(`SELECT count(*) FROM projects`).Scan(&dropped)
			st.close()
		}
		if err := removeDB(path); err != nil {
			return nil, 0, nil, fmt.Errorf("search: %w", err)
		}
		if dropped > 0 {
			fmt.Fprintf(out, "note: %d project registrations dropped; each returns on its next search\n", dropped) // numbers only
		}
	}
	st, err := openStore(path, out)
	if err != nil {
		return nil, 0, nil, err
	}
	fail := func(err error) (*store, int, []proj, error) {
		st.close()
		return nil, 0, nil, err
	}
	confDir := filepath.Dir(path)
	view, err := configView(confDir)
	if err != nil {
		return fail(err)
	}
	changed, err := st.apply(source{project: configProject, dir: confDir, view: view})
	if err != nil {
		return fail(err)
	}
	rows, err := st.db.Query(`SELECT root, docs_root FROM projects ORDER BY rowid`)
	if err != nil {
		return fail(err)
	}
	type reg struct{ root, docs string }
	var regs []reg
	for rows.Next() {
		var r reg
		if err := rows.Scan(&r.root, &r.docs); err != nil {
			rows.Close()
			return fail(err)
		}
		regs = append(regs, r)
	}
	rows.Close()
	projects := []proj{{key: configProject, name: configProject}}
	names := map[string]int{configProject: 1}
	for _, r := range regs {
		reason, err := dropReason(r.root)
		if err != nil {
			// A check that errors keeps the registration (context § Hard
			// rules): skip this checkout for this query and say why.
			fmt.Fprintln(out, printable(fmt.Sprintf("note: skipped %s this time (%v)", r.root, err)))
			continue
		}
		if reason != "" {
			if err := dropProject(st, r.root); err != nil {
				return fail(err)
			}
			fmt.Fprintln(out, printable(fmt.Sprintf("note: dropped %s (%s)", r.root, reason)))
			continue
		}
		docs := ".agents/docs"
		v, _, err := gitView(r.root, docs+"/") // the global header covers many projects: no left-out list
		if errors.Is(err, errUnsafePartialClone) {
			// One checkout this git cannot read safely never stops the rest.
			fmt.Fprintln(out, printable(fmt.Sprintf("note: skipped %s this time (a partial clone, and this git is older than 2.44)", r.root)))
			continue
		}
		if err != nil {
			return fail(fmt.Errorf("search: %w", err))
		}
		n, err := st.apply(source{project: r.root, dir: r.root, docsRoot: docs, view: v})
		if err != nil {
			return fail(err)
		}
		changed += n
		name := filepath.Base(r.root)
		names[name]++
		if names[name] > 1 {
			name = fmt.Sprintf("%s~%d", name, names[name])
		}
		projects = append(projects, proj{key: r.root, name: name, docsRoot: docs})
	}
	return st, changed, projects, nil
}

// dropReason says why a registration should go, or "" when it stays; an
// error means the check could not run, and the registration stays. It goes
// only on positive evidence: the root is gone, git says it is not a
// checkout (or a different one), or .agents/docs is gone or not a directory
// (a plain docs/ is never served in its place; security review, rounds 2-3).
func dropReason(root string) (string, error) {
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return "no longer a git checkout", nil
	} else if err != nil {
		return "", err
	}
	got, err := checkoutRoot(root)
	switch {
	case err != nil && strings.Contains(err.Error(), "not a git repository"):
		return "no longer a git checkout", nil
	case err != nil:
		return "", err
	case got != root:
		return "no longer a git checkout", nil
	}
	fi, err := os.Stat(filepath.Join(root, ".agents", "docs"))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "no longer has .agents/docs", nil
	case err != nil:
		return "", err
	case !fi.IsDir():
		return "no longer has .agents/docs", nil
	}
	return "", nil
}

func dropProject(st *store, root string) error {
	if _, err := st.db.Exec(`BEGIN IMMEDIATE`); err != nil {
		if isBusy(err) {
			return errBusy
		}
		return err
	}
	for _, q := range []string{`DELETE FROM files WHERE project = ?`, `DELETE FROM projects WHERE root = ?`} {
		if _, err := st.db.Exec(q, root); err != nil {
			st.db.Exec(`ROLLBACK`)
			return err
		}
	}
	_, err := st.db.Exec(`COMMIT`)
	return err
}

// Index reconciles the index (or rebuilds it) and prints its counts.
func Index(o Options, out io.Writer) error {
	if err := validate(&o, "index"); err != nil {
		return err
	}
	sc, err := prepare(o, out)
	if err != nil {
		return err
	}
	defer sc.close()
	counts := map[string]int{}
	for _, t := range []string{"chunks", "records", "refs"} {
		var n int
		q := `SELECT count(*) FROM ` + t + ` WHERE oid IN (SELECT oid FROM files WHERE project IN ` + sc.in() + `)`
		if err := sc.st.db.QueryRow(q, sc.keys()...).Scan(&n); err != nil {
			return err
		}
		counts[t] = n
	}
	fmt.Fprintf(out, "files    %d\nchunks   %d\nrecords  %d\nrefs     %d\nchanged  %d\nbytes    %d\nms       %d\n",
		sc.files, counts["chunks"], counts["records"], counts["refs"], sc.changed, sc.st.size(), sc.elapsed.Milliseconds())
	return nil
}

// Search answers one query: ranked words, --refs or --open.
func Search(o Options, out io.Writer) error {
	if err := validate(&o, "search"); err != nil {
		return err
	}
	sc, err := prepare(o, out)
	if err != nil {
		return err
	}
	defer sc.close()
	switch {
	case o.Refs != "":
		return sc.refs(o, out)
	case o.Open:
		return sc.open(o, out)
	}
	return sc.words(o, out)
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

// ftsQuery quotes each word as an FTS5 string, so no character of it is
// read as query syntax, joined with AND (a space) or OR.
func ftsQuery(words []string, op string) string {
	var q []string
	for _, w := range words {
		if strings.IndexFunc(w, isWordRune) < 0 {
			continue
		}
		q = append(q, `"`+strings.ReplaceAll(w, `"`, `""`)+`"`)
	}
	return strings.Join(q, op)
}

func isWordRune(r rune) bool {
	return r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r > 127
}

type hit struct {
	project, path, oid string
	id                 int64 // the chunk: what a hit IS, whatever line it prints
	line               int
	snippet            string
}

// find appends to hits the ranked chunks matching q, skipping any already
// in hits, until o.Limit; perFile carries the per-file cap across calls.
func (sc *scope) find(o Options, q string, hits []hit, perFile map[string]int) ([]hit, error) {
	if q == "" || len(hits) >= o.Limit {
		return hits, nil
	}
	// Keyed by chunk, never by line: the every-word round shifts a hit's
	// line to its matched term, so the any-word round saw the same chunk
	// at its first line as new and printed it twice (audit 2026-09-30, SD2).
	have := map[int64]bool{}
	for _, h := range hits {
		have[h.id] = true
	}
	first := len(hits)
	// Snippets are made only for the hits that are printed, below.
	// The full-text match runs once, first: joined directly, the planner
	// drove the loop from files and ran the match per row (seconds).
	rows, err := sc.st.db.Query(`WITH m AS MATERIALIZED (
			SELECT rowid AS id, bm25(chunks_fts, 2.0, 1.0) AS score FROM chunks_fts WHERE chunks_fts MATCH ?)
		SELECT f.project, f.path, c.oid, c.line, c.id
		FROM m JOIN chunks c ON c.id = m.id JOIN files f ON f.oid = c.oid
		WHERE f.project IN `+sc.in()+`
		ORDER BY m.score, f.path`, append([]any{q}, sc.keys()...)...)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	var ids []int64
	for rows.Next() && len(hits) < o.Limit {
		var h hit
		var id int64
		if err := rows.Scan(&h.project, &h.path, &h.oid, &h.line, &id); err != nil {
			rows.Close()
			return nil, err
		}
		key := h.project + "\x00" + h.path
		if have[id] || perFile[key] >= perFileCap || !sc.kindMatches(o, h.project, h.path) {
			continue
		}
		h.id = id
		perFile[key]++
		hits = append(hits, h)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i, id := range ids {
		i += first
		var body, snip string
		err := sc.st.db.QueryRow(`SELECT c.body, snippet(chunks_fts, 1, char(1), char(2), '…', 24)
			FROM chunks_fts JOIN chunks c ON c.id = chunks_fts.rowid WHERE chunks_fts MATCH ? AND chunks_fts.rowid = ?`, q, id).Scan(&body, &snip)
		if err != nil {
			return nil, err
		}
		hits[i].line += matchOffset(body, snip)
		hits[i].snippet = cleanSnippet(snip)
	}
	return hits, nil
}

// matchOffset is the 0-based line of body holding the snippet's first
// marked term, or 0.
func matchOffset(body, snip string) int {
	a := strings.IndexByte(snip, 1)
	b := strings.IndexByte(snip, 2)
	if a < 0 || b <= a {
		return 0
	}
	term := strings.ToLower(snip[a+1 : b])
	for i, l := range strings.Split(body, "\n") {
		if strings.Contains(strings.ToLower(l), term) {
			return i
		}
	}
	return 0
}

// printable replaces control characters (C0, DEL, C1), Unicode's format
// characters, line separators and paragraph separators (classes Cf, Zl, Zp)
// and its other default-ignorable characters (variation selectors, fillers)
// with '?', so indexed text cannot drive the terminal the answer is printed
// to, forge a line of it, or carry text nobody sees (BUG-152; security review,
// finding 4). Tabs and newlines become '?' too, except in snippets, whose
// callers fold whitespace to single spaces first.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return '?'
		}
		// Unicode's own line and paragraph separators forge a line as a
		// newline does. The whole format class goes too, not a list of its
		// members: direction marks and overrides reorder what a terminal
		// shows, and zero-width and tag characters spell text nobody sees.
		if unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) || unicode.Is(unicode.Cf, r) {
			return '?'
		}
		// And what Unicode itself calls ignorable outside that class: the
		// variation selectors, the combining grapheme joiner, the fillers.
		// By property, not by a list: an ordinary combining accent stays.
		if unicode.Is(unicode.Variation_Selector, r) || unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) {
			return '?'
		}
		return r
	}, s)
}

func cleanSnippet(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = printable(strings.NewReplacer("\x01", "»", "\x02", "«").Replace(s))
	if r := []rune(s); len(r) > snippetMax {
		s = string(r[:snippetMax-1]) + "…"
	}
	return s
}

// words ranks chunks holding every word first, then fills the remaining
// slots with chunks holding any of them: an AND of many words alone often
// matched two chunks and stopped there (recall test, D27).
func (sc *scope) words(o Options, out io.Writer) error {
	phrase := strings.Join(o.Words, " ")
	perFile := map[string]int{}
	hits, err := sc.find(o, ftsQuery(o.Words, " "), nil, perFile)
	if err != nil {
		return err
	}
	all := len(hits)
	if len(o.Words) > 1 {
		if hits, err = sc.find(o, ftsQuery(o.Words, " OR "), hits, perFile); err != nil {
			return err
		}
	}
	header := fmt.Sprintf("search: %d hits for %q, %d with every word (%s)", len(hits), phrase, all, sc.status())
	fmt.Fprintln(out, header)
	for _, h := range hits {
		fmt.Fprintf(out, "%s\n    %s\n", sc.label(h.project, h.path, h.oid, h.line), h.snippet)
	}
	return nil
}

// lineText returns line n (1-based) of the blob oid from its chunks.
func (sc *scope) lineText(oid string, n int) string {
	var start int
	var body string
	err := sc.st.db.QueryRow(`SELECT line, body FROM chunks WHERE oid = ? AND line <= ? ORDER BY line DESC LIMIT 1`, oid, n).Scan(&start, &body)
	if err != nil {
		return ""
	}
	lines := strings.Split(body, "\n")
	if i := n - start; i >= 0 && i < len(lines) {
		return lines[i]
	}
	return ""
}

// markTarget wraps the first occurrence of target in »« and cuts the line
// to snippetMax runes around it, so a long line keeps what it was listed for.
func markTarget(line, target string) string {
	r := []rune(printable(strings.Join(strings.Fields(line), " ")))
	i := strings.Index(string(r), target)
	if i < 0 {
		if len(r) > snippetMax {
			return string(r[:snippetMax-1]) + "…"
		}
		return string(r)
	}
	at := len([]rune(string(r)[:i]))
	n := len([]rune(target))
	start := max(0, min(at-(snippetMax-n)/2, len(r)-snippetMax))
	end := min(len(r), start+snippetMax)
	out := string(r[start:at]) + "»" + string(r[at:at+n]) + "«" + string(r[at+n:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(r) {
		out += "…"
	}
	return out
}

func (sc *scope) refs(o Options, out io.Writer) error {
	x := strings.TrimPrefix(strings.TrimSpace(o.Refs), "./")
	isID := idPattern.MatchString(x)
	isDir := !isID && strings.HasSuffix(x, "/")
	var where string
	var args []any
	switch {
	case isID:
		where, args = `r.target = ?`, []any{x}
	case isDir:
		base := strings.TrimSuffix(x, "/")
		where = `(r.target = ? OR r.target = ? OR r.target LIKE ? ESCAPE '\')`
		args = []any{base, x, likeEscape(x) + "%"}
		for _, p := range sc.projects {
			if rel, ok := docsRelative(base, p.docsRoot); ok {
				where = strings.TrimSuffix(where, ")") + ` OR r.target = ? OR r.target LIKE ? ESCAPE '\')`
				args = append(args, rel, likeEscape(rel+"/")+"%")
			}
		}
	default:
		where, args = `r.target = ?`, []any{x}
		for _, p := range sc.projects {
			if rel, ok := docsRelative(x, p.docsRoot); ok {
				where = `(` + where + ` OR r.target = ?)`
				args = append(args, rel)
			}
		}
	}
	// The first line: the ID's own record, or the path's state.
	switch {
	case isID:
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
	case isDir:
		fmt.Fprintf(out, "%s  directory\n", x)
	default:
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
	}
	rows, err := sc.st.db.Query(`SELECT f.project, f.path, r.oid, r.line, r.target FROM refs r JOIN files f ON f.oid = r.oid
		WHERE `+where+` AND f.project IN `+sc.in()+` ORDER BY f.project, f.path, r.line`, append(args, sc.keys()...)...)
	if err != nil {
		return err
	}
	type row struct {
		project, path, oid, target string
		line                       int
	}
	var raw []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.project, &r.path, &r.oid, &r.line, &r.target); err != nil {
			rows.Close()
			return err
		}
		if sc.kindMatches(o, r.project, r.path) {
			raw = append(raw, r)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	// Grouped per file and record, after the rows are closed (the store has
	// one connection, and a query inside an open row loop waits forever):
	// in DECISIONS.md, ADR-041's "supersedes ADR-040" is its own line, not
	// folded into ADR-040's own heading.
	type group struct {
		project, path, oid, target, date string
		line, count                      int
	}
	var groups []*group
	index := map[string]*group{}
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
		g := &group{project: r.project, path: r.path, oid: r.oid, target: r.target, date: date, line: r.line, count: 1}
		index[key] = g
		groups = append(groups, g)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].date != groups[j].date {
			return groups[i].date > groups[j].date
		}
		return groups[i].path < groups[j].path
	})
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
