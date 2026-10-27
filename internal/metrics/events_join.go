package metrics

import (
	"os"
	"path/filepath"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/trail"
)

// mainWindows is every main session's window, by the project root it ran in.
func (s *Store) mainWindows() (map[string][]window, error) {
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
	return byRoot, nil
}

// owners is every session whose window holds ts.
func owners(ws []window, ts int64) []string {
	var ids []string
	for _, w := range ws {
		if ts >= w.start && ts <= w.end {
			ids = append(ids, w.id)
		}
	}
	return ids
}

// joinRoot writes one project root's events: its corrections, then, when the
// root is a git checkout, its commits and its trail's events. A root that
// could not give some of them returns a note saying which.
func joinRoot(home, root string, ws []window, feedback []stamped, put putFunc) (notes []string, err error) {
	// A records read that fails costs this run the root's record events
	// only: its stored events stay, and feedback.jsonl still counts.
	in, rerr := brain.ForMetrics(home, root)
	if rerr != nil {
		notes = append(notes, "records not read: "+root+": "+rerr.Error())
	}
	for _, f := range append(feedback, feedbackRecords(in.Feedback)...) {
		for _, id := range owners(ws, f.ts) {
			if err := put(id, "correction", f.ref, "", f.ts); err != nil {
				return nil, err
			}
		}
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return append(notes, "project root gone: "+root), nil
	}
	commits, ok := commitsIn(root, ws)
	if !ok {
		return append(notes, "no git history: "+root), nil
	}
	sessionOf, err := joinCommits(commits, ws, put)
	if err != nil {
		return nil, err
	}
	if !in.Migrated {
		return notes, joinFiles(root, ws, sessionOf, put)
	}
	if rerr == nil {
		if err := joinRecords(in.Trail, ws, sessionOf, put); err != nil {
			return nil, err
		}
	}
	return notes, nil
}

// joinCommits gives each commit to every session whose window holds it,
// "ambiguous" when more than one does, and returns the sha -> owning session
// map the bug links read.
func joinCommits(commits []stamped, ws []window, put putFunc) (map[string]string, error) {
	sessionOf := map[string]string{} // sha -> owning session, for bug links
	for _, c := range commits {
		ids := owners(ws, c.ts)
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
	return sessionOf, nil
}

// fileTimes is a trail file's time: the one git gives it while its working
// copy is unchanged, else its mtime, else 0.
type fileTimes struct {
	root     string
	dates    map[string]time.Time
	modified map[string]bool
}

func (f fileTimes) at(rel string) int64 {
	if t, ok := f.dates[rel]; ok && !f.modified[rel] {
		return t.UnixMilli()
	}
	if fi, err := os.Stat(filepath.Join(f.root, rel)); err == nil {
		return fi.ModTime().UnixMilli()
	}
	return 0
}

// joinFiles derives an unmigrated project's handoff, verifier, review and
// bug events from its trail files; a root with no docs directory has none.
func joinFiles(root string, ws []window, sessionOf map[string]string, put putFunc) error {
	docs := trail.DocsRoot(root)
	if docs == "" {
		return nil
	}
	dates, modified := trail.Dates(root, docs)
	ft := fileTimes{root, dates, modified}
	for _, rel := range trailFiles(root, docs, "handoffs", "*.md") {
		for _, id := range owners(ws, ft.at(rel)) {
			if err := put(id, "handoff", fileRef("handoff", rel), "", ft.at(rel)); err != nil {
				return err
			}
		}
	}
	for _, rel := range trailFiles(root, docs, "testing", "*-verify-*.md") {
		m := statusLine.FindStringSubmatch(readSmall(filepath.Join(root, rel)))
		if m == nil {
			continue
		}
		for _, id := range owners(ws, ft.at(rel)) {
			if err := put(id, "verifier", fileRef("testing", rel), m[1], ft.at(rel)); err != nil {
				return err
			}
		}
	}
	for _, rel := range trailFiles(root, docs, "reviews", "*.md") {
		v := findings(readSmall(filepath.Join(root, rel)))
		for _, id := range owners(ws, ft.at(rel)) {
			if err := put(id, "review", fileRef("review", rel), v, ft.at(rel)); err != nil {
				return err
			}
		}
	}
	return joinFileBugs(root, docs, ft, sessionOf, put)
}

// joinFileBugs gives a BUG-N file to the session that owns the first commit
// its body cites.
func joinFileBugs(root, docs string, ft fileTimes, sessionOf map[string]string, put putFunc) error {
	for _, rel := range trailFiles(root, docs, "bugs", "BUG-*.md") {
		m := bugName.FindStringSubmatch(filepath.Base(rel))
		if m == nil {
			continue
		}
		for _, h := range hexRun.FindAllString(readSmall(filepath.Join(root, rel)), -1) {
			if sha, id := matchCommit(h, sessionOf); id != "" {
				if err := put(id, "bug", m[1], sha, ft.at(rel)); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}
