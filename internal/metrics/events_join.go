package metrics

import (
	"os"

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
// root is a git checkout, its commits and its trail records' events. A root
// that could not give some of them returns a note saying which. A root whose
// checkout holds no trail records gives commits and corrections only.
func joinRoot(home, root string, ws []window, put putFunc) (notes []string, err error) {
	// A records read that fails costs this run the root's record events
	// only: its stored events stay.
	in, rerr := brain.ForMetrics(home, root)
	if rerr != nil {
		notes = append(notes, "records not read: "+root+": "+rerr.Error())
	}
	for _, f := range feedbackRecords(in.Feedback) {
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
	if in.Migrated && rerr == nil {
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
