package metrics

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

// closeSlack is how long after a session's last step its handoff and commit
// still count as its own (spec A1).
const closeSlack = 30 * time.Minute

var (
	statusLine = regexp.MustCompile(`(?m)^## Status:\s*(PASS|FAIL|BLOCKED|IN PROGRESS)\b`)
	finding    = regexp.MustCompile(`\*\*\[(high|medium|low)\b`)
	bugName    = regexp.MustCompile(`^(BUG-[0-9]+)`)
	hexRun     = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
)

type window struct {
	id         string
	start, end int64 // ms; end includes closeSlack
}

type stamped struct {
	ts  int64
	ref string
}

// joinEvents derives every main session's events from git, the trail and
// the feedback log (spec § Data, events payloads). It is idempotent: each
// event's key is (session, kind, ref), and an event whose value changes (a
// commit that became ambiguous when an overlapping session arrived) is
// updated in place. A project root that is gone or is not git gets no
// commit events and returns a note.
func (s *Store) joinEvents(home string) (notes []string, err error) {
	rows, err := s.db.Query(`SELECT id, project_root, started_at, ended_at FROM sessions
		WHERE parent_id IS NULL AND project_root IS NOT NULL AND started_at IS NOT NULL AND ended_at IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	byRoot := map[string][]window{}
	tops := map[string]string{}
	for rows.Next() {
		var id, root string
		var st, en int64
		if err := rows.Scan(&id, &root, &st, &en); err != nil {
			rows.Close()
			return nil, err
		}
		// A session started in a subdirectory joins its project's events.
		if top, ok := tops[root]; ok {
			root = top
		} else {
			top = trail.Toplevel(root)
			tops[root], root = top, top
		}
		byRoot[root] = append(byRoot[root], window{id, st, en + closeSlack.Milliseconds()})
	}
	rows.Close()

	feedback := readFeedback(home)
	roots := make([]string, 0, len(byRoot))
	for r := range byRoot {
		roots = append(roots, r)
	}
	sort.Strings(roots)

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := rewriteFileRefs(tx); err != nil {
		return nil, err
	}
	put := func(sid, kind, ref, value string, ts int64) error {
		_, err := tx.Exec(`INSERT INTO events(session_id, ts, kind, ref, value) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(session_id, kind, ref) DO UPDATE SET value = excluded.value, ts = excluded.ts`, sid, ts, kind, ref, value)
		return err
	}
	for _, root := range roots {
		ws := byRoot[root]
		owners := func(ts int64) []string {
			var ids []string
			for _, w := range ws {
				if ts >= w.start && ts <= w.end {
					ids = append(ids, w.id)
				}
			}
			return ids
		}
		for _, f := range feedback[root] {
			for _, id := range owners(f.ts) {
				if err := put(id, "correction", f.ref, "", f.ts); err != nil {
					return nil, err
				}
			}
		}
		if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
			notes = append(notes, "project root gone: "+root)
			continue
		}
		commits, ok := commitsIn(root, ws)
		if !ok {
			notes = append(notes, "no git history: "+root)
			continue
		}
		sessionOf := map[string]string{} // sha -> owning session, for bug links
		for _, c := range commits {
			ids := owners(c.ts)
			value := ""
			if len(ids) > 1 {
				value = "ambiguous"
			}
			for _, id := range ids {
				if err := put(id, "commit", c.ref, value, c.ts); err != nil {
					return nil, err
				}
				if value == "" {
					sessionOf[c.ref] = id
				}
			}
		}
		docs := trail.DocsRoot(root)
		if docs == "" {
			continue
		}
		dates, modified := trail.Dates(root, docs)
		at := func(rel string) int64 {
			if t, ok := dates[rel]; ok && !modified[rel] {
				return t.UnixMilli()
			}
			if fi, err := os.Stat(filepath.Join(root, rel)); err == nil {
				return fi.ModTime().UnixMilli()
			}
			return 0
		}
		for _, rel := range trailFiles(root, docs, "handoffs", "*.md") {
			for _, id := range owners(at(rel)) {
				if err := put(id, "handoff", fileRef("handoff", rel), "", at(rel)); err != nil {
					return nil, err
				}
			}
		}
		for _, rel := range trailFiles(root, docs, "testing", "*-verify-*.md") {
			m := statusLine.FindStringSubmatch(readSmall(filepath.Join(root, rel)))
			if m == nil {
				continue
			}
			for _, id := range owners(at(rel)) {
				if err := put(id, "verifier", fileRef("testing", rel), m[1], at(rel)); err != nil {
					return nil, err
				}
			}
		}
		for _, rel := range trailFiles(root, docs, "reviews", "*.md") {
			n := map[string]int{}
			for _, m := range finding.FindAllStringSubmatch(readSmall(filepath.Join(root, rel)), -1) {
				n[m[1]]++
			}
			v := "high=" + strconv.Itoa(n["high"]) + ",medium=" + strconv.Itoa(n["medium"]) + ",low=" + strconv.Itoa(n["low"])
			for _, id := range owners(at(rel)) {
				if err := put(id, "review", fileRef("review", rel), v, at(rel)); err != nil {
					return nil, err
				}
			}
		}
		for _, rel := range trailFiles(root, docs, "bugs", "BUG-*.md") {
			m := bugName.FindStringSubmatch(filepath.Base(rel))
			if m == nil {
				continue
			}
			for _, h := range hexRun.FindAllString(readSmall(filepath.Join(root, rel)), -1) {
				if sha, id := matchCommit(h, sessionOf); id != "" {
					if err := put(id, "bug", m[1], sha, at(rel)); err != nil {
						return nil, err
					}
					break
				}
			}
		}
	}
	return notes, tx.Commit()
}

// refKinds is each trail event's record kind: the kind whose ID its ref is.
var refKinds = map[string]string{"handoff": "handoff", "verifier": "testing", "review": "review"}

// fileRef is a trail file's event reference: the ID the import gives it, so
// the same item keeps one reference before and after the project migrates.
func fileRef(kind, rel string) string {
	return brain.ImportedID(kind, strings.TrimSuffix(filepath.Base(rel), ".md"))
}

// rewriteFileRefs turns the file-name references an older ingest stored
// into ID form, inside the join's transaction. A row whose ID form the
// session already holds is a duplicate and is deleted. A ref already in ID
// form is left alone even when it ends in .md (notes.md.md imports as
// HANDOFF-notes.md), so a later run changes nothing.
func rewriteFileRefs(tx *sql.Tx) error {
	type row struct{ sid, kind, ref string }
	rows, err := tx.Query(`SELECT session_id, kind, ref FROM events
		WHERE kind IN ('handoff', 'verifier', 'review') AND ref GLOB '*.md'`)
	if err != nil {
		return err
	}
	var old []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.sid, &r.kind, &r.ref); err != nil {
			rows.Close()
			return err
		}
		old = append(old, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range old {
		// ImportedID(kind, "") is the kind's prefix and dash; a ref that is
		// that prefix plus a slug the rule leaves as it is is an ID already.
		k := refKinds[r.kind]
		if tail, ok := strings.CutPrefix(r.ref, brain.ImportedID(k, "")); ok && brain.ImportedID(k, tail) == r.ref {
			continue
		}
		res, err := tx.Exec(`UPDATE OR IGNORE events SET ref = ? WHERE session_id = ? AND kind = ? AND ref = ?`,
			fileRef(k, r.ref), r.sid, r.kind, r.ref)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			if _, err := tx.Exec(`DELETE FROM events WHERE session_id = ? AND kind = ? AND ref = ?`, r.sid, r.kind, r.ref); err != nil {
				return err
			}
		}
	}
	return nil
}

// commitsIn lists root's commits between the earliest window start and the
// latest window end, newest first, through trail.Log (no lock, no hook, no
// signature program).
func commitsIn(root string, ws []window) ([]stamped, bool) {
	lo, hi := ws[0].start, ws[0].end
	for _, w := range ws {
		lo, hi = min(lo, w.start), max(hi, w.end)
	}
	out, err := trail.Log(root, "--all",
		"--since=@"+strconv.FormatInt(lo/1000, 10), "--until=@"+strconv.FormatInt(hi/1000+1, 10), "--format=%H %ct")
	if err != nil {
		return nil, false
	}
	var cs []stamped
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		sha, secs, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if n, err := strconv.ParseInt(secs, 10, 64); err == nil {
			cs = append(cs, stamped{ts: n * 1000, ref: sha})
		}
	}
	return cs, true
}

func matchCommit(prefix string, sessionOf map[string]string) (string, string) {
	for sha, id := range sessionOf {
		if strings.HasPrefix(sha, prefix) {
			return sha, id
		}
	}
	return "", ""
}

// trailFiles lists docs/<dir>/<pattern> relative to root, slash-separated.
func trailFiles(root, docs, dir, pattern string) []string {
	matches, _ := filepath.Glob(filepath.Join(root, docs, dir, pattern))
	var out []string
	for _, m := range matches {
		if fi, err := os.Lstat(m); err == nil && fi.Mode().IsRegular() {
			rel, _ := filepath.Rel(root, m)
			out = append(out, filepath.ToSlash(rel))
		}
	}
	return out
}

// readSmall reads up to 1 MiB of a trail file; only patterns are taken from
// it, never its text.
func readSmall(p string) string {
	f, _, err := openRegular(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, 1<<20))
	return string(b)
}

// readFeedback is the feedback log's entries by project root: their times,
// and the entry's ts as its reference. Nothing else is taken from a line.
func readFeedback(home string) map[string][]stamped {
	out := map[string][]stamped{}
	f, err := os.Open(filepath.Join(home, harness.StateDir, "feedback.jsonl"))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var e struct {
			TS      string `json:"ts"`
			Project string `json:"project"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Project == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, e.TS); err == nil {
			out[filepath.Clean(e.Project)] = append(out[filepath.Clean(e.Project)], stamped{ts: t.UnixMilli(), ref: e.TS})
		}
	}
	return out
}
