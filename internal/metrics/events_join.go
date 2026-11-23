package metrics

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
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
	return byRoot, rows.Err()
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

// joinEventsWithBrainHome behaves like joinEvents, additionally reading trail
// and feedback records from brainHome when it names a distinct home (e.g. an
// eval sandbox home holding sb/.config/fyr/brain.db, F-012).
func (s *Store) joinEventsWithBrainHome(home, brainHome string) (notes []string, err error) {
	if strings.TrimSpace(brainHome) == "" || filepath.Clean(brainHome) == filepath.Clean(home) {
		return s.joinEvents(home)
	}
	byRoot, err := s.mainWindows()
	if err != nil {
		return nil, err
	}
	roots := make([]string, 0, len(byRoot))
	for r := range byRoot {
		roots = append(roots, r)
	}
	sort.Strings(roots)

	var pending []pendingEvent
	put := func(sid, kind, ref, value string, ts int64) error {
		pending = append(pending, pendingEvent{sid: sid, kind: kind, ref: ref, value: value, ts: ts})
		return nil
	}
	for _, root := range roots {
		rootNotes, err := joinRootWithBrain(home, brainHome, root, byRoot[root], put)
		if err != nil {
			return nil, err
		}
		notes = append(notes, rootNotes...)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := applyJoinedEvents(tx, pending); err != nil {
		return nil, err
	}
	return notes, tx.Commit()
}

// joinRoot writes one project root's events: its corrections, then, when the
// root is a git checkout, its commits and its trail records' events. A root
// that could not give some of them returns a note saying which. A root whose
// checkout holds no trail records gives commits and corrections only.
func joinRoot(home, root string, ws []window, put putFunc) (notes []string, err error) {
	return joinRootWithBrain(home, "", root, ws, put)
}

func joinRootWithBrain(home, brainHome, root string, ws []window, put putFunc) (notes []string, err error) {
	// A records read that fails costs this run the root's record events
	// only: its stored events stay.
	in, rerr := brain.ForMetrics(home, root)
	if rerr != nil {
		notes = append(notes, "records not read: "+root+": "+rerr.Error())
	}
	extraTrail, extraFB, extraNotes := readExtraBrainRecords(home, brainHome, root)
	notes = append(notes, extraNotes...)
	for _, f := range feedbackRecords(append(in.Feedback, extraFB...)) {
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
	if len(extraTrail) > 0 {
		if err := joinRecords(extraTrail, ws, sessionOf, put); err != nil {
			return nil, err
		}
	}
	return notes, nil
}

func readExtraBrainRecords(home, brainHome, root string) (trailRecs, fb []brain.Record, notes []string) {
	if strings.TrimSpace(brainHome) == "" || filepath.Clean(brainHome) == filepath.Clean(home) {
		return nil, nil, nil
	}
	stateParts := strings.Split(filepath.ToSlash(harness.StateDir), "/")
	if !isRealDirChain(brainHome, stateParts...) {
		return nil, nil, nil
	}
	dbPath := brain.Path(brainHome)
	hasDB := isRegularFile(dbPath)
	if fi, err := os.Lstat(dbPath); err == nil && !fi.Mode().IsRegular() {
		return nil, nil, nil
	}
	bin, berr := brain.ForMetrics(brainHome, root)
	if berr != nil {
		notes = append(notes, "records not read: "+root+": "+berr.Error())
		return nil, nil, notes
	}
	fb = bin.Feedback
	if bin.Migrated {
		return bin.Trail, fb, notes
	}
	if hasDB && trail.Registered(filepath.Join(home, harness.StateDir), root) {
		recs, ok, err := brain.ReadScope(brainHome, root)
		if err != nil {
			notes = append(notes, "records not read: "+root+": "+err.Error())
		} else if ok {
			trailRecs = recs
		}
	}
	return trailRecs, fb, notes
}

func isRealDirChain(base string, parts ...string) bool {
	if st, err := os.Lstat(base); err != nil || !st.IsDir() {
		return false
	}
	cur := base
	for _, p := range parts {
		if p == "" || p == "." {
			continue
		}
		cur = filepath.Join(cur, p)
		st, err := os.Lstat(cur)
		if errors.Is(err, os.ErrNotExist) {
			return true
		}
		if err != nil || !st.IsDir() {
			return false
		}
	}
	return true
}

func isRegularFile(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode().IsRegular()
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
