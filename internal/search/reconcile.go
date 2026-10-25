package search

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

// maxFileBytes is the largest file indexed; larger files, and files with a
// NUL byte in their first 8 KiB, are skipped.
const (
	maxFileBytes = 1 << 20
	binarySniff  = 8 << 10
	gcAge        = 14 * 24 * 60 * 60 // seconds an unreferenced blob is kept
	touchEvery   = 24 * 60 * 60      // seconds between last_seen refreshes
)

// entry is one path of the current view.
type entry struct {
	path, oid, state string
}

// git runs in dir through trail.Command, the hardened builder: the checkout's
// own config is untrusted (no lock, no fsmonitor, no signature program, no
// lazy fetch; BUG-164, BUG-165).
func git(dir string, stdin []byte, args ...string) ([]byte, error) {
	cmd := trail.Command(dir, args...)
	// Git's messages in its C locale: dropReason matches "not a git
	// repository", which a translated catalogue would reword.
	cmd.Env = append(cmd.Env, "LC_ALL=C", "LANGUAGE=")
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// checkoutRoot returns the top of the git work tree holding dir.
func checkoutRoot(dir string) (string, error) {
	out, err := git(dir, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", errors.New("no work tree")
	}
	return root, nil
}

// docsRootOf is the trail's root: .agents/docs when it exists, else docs,
// else .agents/docs.
func docsRootOf(root string) string {
	if d := trail.DocsRoot(root); d != "" {
		return d
	}
	return ".agents/docs"
}

// errUnsafePartialClone is a partial clone on a git that ignores
// GIT_NO_LAZY_FETCH: the project search refuses it, the global one skips it.
var errUnsafePartialClone = errors.New("search: a partial clone on a git older than 2.44, which could fetch through the clone's own config")

// lazyFetchSafe reports whether this git honours GIT_NO_LAZY_FETCH; a
// variable so a test can stand in for an older git.
var lazyFetchSafe = trail.LazyFetchSafe

// indexStat is what git's index records of a file's stat.
type indexStat struct{ size, sec, nsec int64 }

// indexStats reads the size and mtime git's index holds for each entry
// (`ls-files -s --debug`, which refreshes nothing and runs no filter or hook)
// and the index file's own mtime. On any error it returns nothing, and every
// tracked file is hashed.
//
// The output is read line by line without -z: git quotes a name holding a
// newline, a tab, a quote or a backslash, so a header line can never carry
// another entry's lines, and a quoted name is skipped (its file is hashed).
// With -z a name holding a newline could forge another file's stat lines
// (security review of BUG-165's fix, finding 2).
func indexStats(root string) (map[string]indexStat, time.Time) {
	out, err := git(root, nil, "ls-files", "-s", "--debug")
	if err != nil {
		return nil, time.Time{}
	}
	idx, err := git(root, nil, "rev-parse", "--git-path", "index")
	if err != nil {
		return nil, time.Time{}
	}
	ip := strings.TrimSpace(string(idx))
	if !filepath.IsAbs(ip) {
		ip = filepath.Join(root, ip)
	}
	ifi, err := os.Stat(ip)
	if err != nil {
		return nil, time.Time{}
	}
	stats := map[string]indexStat{}
	cur, seen := "", 0
	var st indexStat
	flush := func() {
		if cur != "" && seen == 2 {
			stats[cur] = st
		}
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, " ") {
			// A header: "<mode> <oid> <stage>\t<path>".
			flush()
			cur, seen, st = "", 0, indexStat{}
			if _, p, ok := strings.Cut(line, "\t"); ok && !strings.HasPrefix(p, `"`) {
				cur = p
			}
			continue
		}
		f := strings.Fields(strings.ReplaceAll(line, "\t", " "))
		switch {
		case len(f) >= 2 && f[0] == "mtime:":
			if sec, nsec, ok := strings.Cut(f[1], ":"); ok {
				a, e1 := strconv.ParseInt(sec, 10, 64)
				b, e2 := strconv.ParseInt(nsec, 10, 64)
				if e1 == nil && e2 == nil {
					st.sec, st.nsec = a, b
					seen++
				}
			}
		case len(f) >= 2 && f[0] == "size:":
			if v, err := strconv.ParseInt(f[1], 10, 64); err == nil {
				st.size = v
				seen++
			}
		}
	}
	flush()
	return stats, ifi.ModTime()
}

// noIndexMarker names the file that takes its directory out of the index.
// oldNoIndexMarker is its name from before the rename, honoured the same
// way: it is a privacy control the operator placed, and nothing renames
// their files (plan fyr D12, F8).
const (
	noIndexMarker    = ".fyr-noindex"
	oldNoIndexMarker = ".sh-agent-noindex"
)

func isNoIndexMarker(p string) bool {
	b := path.Base(p)
	return b == noIndexMarker || b == oldNoIndexMarker
}

// gitView lists the checkout's current files: the index entries of
// `git ls-files -s`, each marked modified when its bytes are not the index's
// blob, plus the untracked files of `git ls-files --others` (blob IDs
// computed from their bytes; no `git status`, BUG-165), minus deleted paths,
// symlinks, submodules, secret-shaped names and every path under a
// directory that holds a noindex marker file. A file's content is read
// again when its blob is indexed: a file edited and restored between the two
// reads is indexed with the edit until `index --rebuild` (a known limit; the
// operator's choice at the security review's cap, plan D35).
// prefix limits it to one directory. The second result is the directories a
// marker takes out, from the whole checkout whatever the prefix.
func gitView(root, prefix string) ([]entry, []string, error) {
	under := func(p string) bool { return prefix == "" || strings.HasPrefix(p, prefix) }
	// Every read goes through an os.Root: a path that leaves the checkout
	// through `..` or a symlinked directory is refused by the standard
	// library, whatever git lists (security check of the v1.13.0 gate fixes).
	// A git older than 2.44 ignores GIT_NO_LAZY_FETCH, and a partial clone
	// could then fetch a missing object through its own core.sshCommand.
	if trail.PartialClone(root) && !lazyFetchSafe() {
		return nil, nil, fmt.Errorf("%w: %s; update git, or search a full clone", errUnsafePartialClone, root)
	}
	fsRoot, err := os.OpenRoot(root)
	if err != nil {
		return nil, nil, err
	}
	defer fsRoot.Close()
	out, err := git(root, nil, "ls-files", "-s", "-z")
	if err != nil {
		return nil, nil, err
	}
	var order []string
	view := map[string]entry{}
	// Markers are collected from the whole checkout, not only under prefix:
	// a marker above the prefix covers it (a checkout marked at its root has
	// no docs trail for the global index either).
	markers := map[string]bool{}
	for _, rec := range strings.Split(string(out), "\x00") {
		meta, p, ok := strings.Cut(rec, "\t")
		if ok && isNoIndexMarker(p) {
			markers[p] = true
		}
		if !ok || !under(p) {
			continue
		}
		f := strings.Fields(meta) // mode oid stage
		if len(f) != 3 || f[0] == "120000" || f[0] == "160000" {
			continue
		}
		if _, dup := view[p]; dup { // an unmerged path: its bytes decide below
			continue
		}
		view[p] = entry{path: p, oid: f[1], state: "tracked"}
		order = append(order, p)
	}
	// No `git status`: it runs the clean filters and the fsmonitor hook the
	// checkout's own .gitattributes and config name (BUG-165). A tracked
	// file is compared by its own bytes with the index's blob; untracked
	// files come from ls-files, which reads ignore rules only. A file the
	// checkout filters reads as modified and is indexed from its bytes.
	// A tracked marker deleted from the work tree no longer counts, wherever
	// it sits: the loop below sees only paths under prefix.
	for p := range markers {
		if _, err := fsRoot.Lstat(filepath.FromSlash(p)); err != nil {
			delete(markers, p)
		}
	}
	// The index's own stat data spares the hash of a file git would also call
	// unchanged: same size, same mtime to the nanosecond, and not racily clean
	// (written within the second the index was). Reading it runs nothing.
	stats, indexTime := indexStats(root)
	for _, p := range append([]string(nil), order...) {
		// A plain stat for the comparison (an os.Root walk per file cost
		// more than the hash it saves); anything read still goes through
		// fsRoot, which refuses a path that leaves the checkout.
		if st, ok := stats[p]; ok {
			if fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p))); err == nil && fi.Mode().IsRegular() &&
				fi.Size() == st.size && fi.ModTime().Unix() == st.sec && int64(fi.ModTime().Nanosecond()) == st.nsec &&
				st.sec < indexTime.Unix() {
				continue // unchanged: the index's blob stands
			}
		}
		fi, err := fsRoot.Lstat(filepath.FromSlash(p))
		if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxFileBytes {
			delete(view, p) // deleted, no longer a regular file, or too large
			continue
		}
		// The blob ID comes from the file's own bytes: `git hash-object
		// --stdin-paths` strips a trailing CR and unquotes a leading '"'
		// in a name, binding an ID to another file (security review,
		// finding 3).
		data, err := readAll(fsRoot, filepath.FromSlash(p))
		if err != nil {
			delete(view, p)
			continue
		}
		if oid := blobID(data); oid != view[p].oid {
			view[p] = entry{path: p, oid: oid, state: "modified"}
		}
	}
	out, err = git(root, nil, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, nil, err
	}
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		fi, err := fsRoot.Lstat(filepath.FromSlash(p))
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if isNoIndexMarker(p) {
			markers[p] = true // a marker not yet added counts at once
		}
		if !under(p) || fi.Size() > maxFileBytes {
			continue
		}
		data, err := readAll(fsRoot, filepath.FromSlash(p))
		if err != nil {
			continue
		}
		if _, known := view[p]; !known {
			order = append(order, p)
		}
		view[p] = entry{path: p, oid: blobID(data), state: "untracked"}
	}
	// A directory holding a noindex marker is data the project carries, not its
	// own text: nothing below it is indexed. The marker counts in every state
	// git reports it in (committed, staged, not yet added): the first search
	// after the marker is written must already leave the directory out.
	var marked []string
	for p := range markers {
		dir := path.Dir(p) + "/"
		if dir == "./" {
			dir = "" // a marker at the root: nothing of the checkout is indexed
		}
		marked = append(marked, dir)
	}
	noIndex := func(p string) bool {
		for _, dir := range marked {
			if strings.HasPrefix(p, dir) {
				return true
			}
		}
		return false
	}
	entries := make([]entry, 0, len(view))
	for _, p := range order {
		if harness.SecretShaped(path.Base(p)) {
			continue // never indexed, even untracked (security review, finding 6)
		}
		if noIndex(p) {
			continue
		}
		if e, ok := view[p]; ok {
			entries = append(entries, e)
			delete(view, p) // an unmerged path appears once
		}
	}
	// The marked directories go back to the caller, sorted, the root as
	// "./": the search header names them, so an empty answer is not read
	// as "nothing there".
	leftOut := make([]string, 0, len(marked))
	for _, dir := range marked {
		if dir == "" {
			dir = "./"
		}
		leftOut = append(leftOut, dir)
	}
	sort.Strings(leftOut)
	return entries, leftOut, nil
}

// blobID is the git blob ID of data under SHA-1 ("blob <len>\x00" + data).
// It only has to name content consistently inside this index; a checkout in
// SHA-256 object format gets SHA-1 IDs for its modified files, which never
// collide with its index's IDs.
func blobID(data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// configView lists the global index's own sources in ~/.config/fyr/,
// keyed by their SHA-256 since git does not hold them.
func configView(dir string) ([]entry, error) {
	names := []string{"feedback.jsonl", "retros.jsonl"}
	// Listed by name, never through a pattern built from the config path: a
	// `[` in it would be read as a character class (the golang rule).
	if entries, err := os.ReadDir(filepath.Join(dir, "proposals")); err == nil {
		var props []string
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".md") {
				props = append(props, "proposals/"+e.Name())
			}
		}
		sort.Strings(props)
		names = append(names, props...)
	}
	// Plain reads: ~/.config/fyr is the operator's own state, and a
	// dotfiles arrangement may symlink its proposals directory.
	var out []entry
	for _, n := range names {
		fi, err := os.Stat(filepath.Join(dir, n))
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		out = append(out, entry{path: n, oid: "sha256:" + hex.EncodeToString(sum[:]), state: "tracked"})
	}
	return out, nil
}

// readAll reads one file through the root, so the read cannot leave it. On
// a Go before 1.25.12, os.Root.Open carries advisory GO-2026-4970 (an escape
// through a path ending in "/"); every call here follows an Lstat of the
// same path that reported a regular file, which such a path never is.
func readAll(fsRoot *os.Root, rel string) ([]byte, error) {
	f, err := fsRoot.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// readIndexable returns a file's text, or ok=false when it is too large,
// binary, gone, or outside the root.
func readIndexable(fsRoot *os.Root, rel string) (string, bool) {
	fi, err := fsRoot.Lstat(rel)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxFileBytes {
		return "", false
	}
	data, err := readAll(fsRoot, rel)
	if err != nil {
		return "", false
	}
	sniff := data
	if len(sniff) > binarySniff {
		sniff = sniff[:binarySniff]
	}
	if bytes.IndexByte(sniff, 0) >= 0 {
		return "", false
	}
	return string(data), true
}

// source is what apply needs to know about one project.
type source struct {
	project  string
	dir      string // where the view's paths are read from
	docsRoot string // "" for ~config
	view     []entry
}

// apply brings one project's rows up to its current view and returns how
// many paths changed. When nothing changed and last_seen is fresh it takes
// no write lock at all.
func (s *store) apply(src source) (int, error) {
	now := nowFunc().Unix()
	changed, err := s.diffCount(src)
	if err != nil {
		return 0, err
	}
	if changed == 0 && !s.touchDue(src.project, now) {
		return 0, nil
	}
	if _, err := s.db.Exec(`BEGIN IMMEDIATE`); err != nil {
		if isBusy(err) {
			return 0, errBusy
		}
		return 0, err
	}
	n, err := s.applyLocked(src, now)
	if err != nil {
		s.db.Exec(`ROLLBACK`)
		if isBusy(err) {
			return 0, errBusy
		}
		return 0, err
	}
	if _, err := s.db.Exec(`COMMIT`); err != nil {
		s.db.Exec(`ROLLBACK`)
		if isBusy(err) {
			return 0, errBusy
		}
		return 0, err
	}
	if n > 0 {
		s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	}
	return n, nil
}

// current reads the project's files rows and the blob table's skip marks.
func (s *store) current(project string) (map[string]string, map[string]bool, error) {
	files := map[string]string{}
	rows, err := s.db.Query(`SELECT path, oid FROM files WHERE project = ?`, project)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var p, o string
		if err := rows.Scan(&p, &o); err != nil {
			rows.Close()
			return nil, nil, err
		}
		files[p] = o
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	blobs := map[string]bool{} // oid -> skipped
	rows, err = s.db.Query(`SELECT oid, skipped FROM blobs`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var o string
		var sk int
		if err := rows.Scan(&o, &sk); err != nil {
			return nil, nil, err
		}
		blobs[o] = sk != 0
	}
	return files, blobs, rows.Err()
}

// wanted is the view minus paths whose blob is known to be skipped.
func wanted(view []entry, blobs map[string]bool) map[string]entry {
	w := map[string]entry{}
	for _, e := range view {
		if skipped, known := blobs[e.oid]; known && skipped {
			continue
		}
		w[e.path] = e
	}
	return w
}

func (s *store) diffCount(src source) (int, error) {
	files, blobs, err := s.current(src.project)
	if err != nil {
		return 0, err
	}
	w := wanted(src.view, blobs)
	n := 0
	for p, e := range w {
		if files[p] != e.oid {
			n++
		}
	}
	for p := range files {
		if _, ok := w[p]; !ok {
			n++
		}
	}
	return n, nil
}

func (s *store) touchDue(project string, now int64) bool {
	var v int64
	err := s.db.QueryRow(`SELECT CAST(value AS INTEGER) FROM meta WHERE key = ?`, "touched:"+project).Scan(&v)
	return err != nil || now-v >= touchEvery
}

func (s *store) applyLocked(src source, now int64) (int, error) {
	files, blobs, err := s.current(src.project)
	if err != nil {
		return 0, err
	}
	// Index every blob the database has not seen, parsed by the first path
	// it appears at in the view (D14). Reads go through an os.Root.
	fsRoot, err := os.OpenRoot(src.dir)
	if err != nil {
		return 0, err
	}
	defer fsRoot.Close()
	var in *inserter
	for _, e := range src.view {
		if _, known := blobs[e.oid]; known {
			continue
		}
		text, ok := readIndexable(fsRoot, filepath.FromSlash(e.path))
		if _, err := s.db.Exec(`INSERT INTO blobs(oid, last_seen, skipped) VALUES (?, ?, ?)`, e.oid, now, boolInt(!ok)); err != nil {
			return 0, err
		}
		blobs[e.oid] = !ok
		if !ok {
			continue
		}
		if in == nil {
			if in, err = s.newInserter(); err != nil {
				return 0, err
			}
			defer in.close()
		}
		if err := in.content(e, text, src.docsRoot); err != nil {
			return 0, err
		}
	}
	w := wanted(src.view, blobs)
	n := 0
	for p, oid := range files {
		if e, ok := w[p]; !ok || e.oid != oid {
			if _, err := s.db.Exec(`DELETE FROM files WHERE project = ? AND path = ?`, src.project, p); err != nil {
				return 0, err
			}
			n++
		}
	}
	for p, e := range w {
		if old, ok := files[p]; ok && old == e.oid {
			if _, err := s.db.Exec(`UPDATE files SET state = ? WHERE project = ? AND path = ? AND state != ?`, e.state, src.project, p, e.state); err != nil {
				return 0, err
			}
			continue
		}
		if _, ok := files[p]; !ok {
			n++
		}
		if _, err := s.db.Exec(`INSERT INTO files(project, path, oid, state) VALUES (?, ?, ?, ?)`, src.project, p, e.oid, e.state); err != nil {
			return 0, err
		}
	}
	if _, err := s.db.Exec(`UPDATE blobs SET last_seen = ? WHERE oid IN (SELECT oid FROM files WHERE project = ?)`, now, src.project); err != nil {
		return 0, err
	}
	if _, err := s.db.Exec(`INSERT INTO meta(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, "touched:"+src.project, now); err != nil {
		return 0, err
	}
	return n, s.gc(now)
}

// inserter holds the three insert statements, prepared once per reconcile:
// a full build runs some fifty thousand inserts.
type inserter struct{ chunk, record, ref *sql.Stmt }

func (s *store) newInserter() (*inserter, error) {
	var in inserter
	var err error
	if in.chunk, err = s.db.Prepare(`INSERT INTO chunks(oid, line, heading, body) VALUES (?, ?, ?, ?)`); err != nil {
		return nil, err
	}
	if in.record, err = s.db.Prepare(`INSERT INTO records(oid, id, line, title, status, date) VALUES (?, ?, ?, ?, ?, ?)`); err != nil {
		in.close()
		return nil, err
	}
	if in.ref, err = s.db.Prepare(`INSERT INTO refs(oid, line, target) VALUES (?, ?, ?)`); err != nil {
		in.close()
		return nil, err
	}
	return &in, nil
}

func (in *inserter) close() {
	for _, st := range []*sql.Stmt{in.chunk, in.record, in.ref} {
		if st != nil {
			st.Close()
		}
	}
}

func (in *inserter) content(e entry, text, docsRoot string) error {
	for _, c := range chunkText(e.path, text) {
		if _, err := in.chunk.Exec(e.oid, c.line, c.heading, c.body); err != nil {
			return err
		}
	}
	rel, _ := docsRelative(e.path, docsRoot)
	for _, r := range parseRecords(rel, text) {
		if _, err := in.record.Exec(e.oid, r.id, r.line, r.title, r.status, r.date); err != nil {
			return err
		}
	}
	for _, r := range parseRefs(text) {
		if _, err := in.ref.Exec(e.oid, r.line, r.target); err != nil {
			return err
		}
	}
	return nil
}

// gc drops blobs no project's view references that were last seen more
// than gcAge ago, with everything derived from them; the chunks trigger
// keeps chunks_fts in step.
func (s *store) gc(now int64) error {
	cut := now - gcAge
	old := `SELECT oid FROM blobs WHERE last_seen < ? AND oid NOT IN (SELECT oid FROM files)`
	for _, t := range []string{"chunks", "records", "refs"} {
		if _, err := s.db.Exec(`DELETE FROM `+t+` WHERE oid IN (`+old+`)`, cut); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(`DELETE FROM blobs WHERE oid IN (`+old+`)`, cut)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
