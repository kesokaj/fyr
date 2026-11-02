package brain

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

// Scopes is every project scope the store holds records for: the roots the
// dream considers besides the file index's checkouts (brain plan Phase 4),
// raw; the dream takes only the registered ones (ADR-068, audit-choices
// D15). It creates no store.
func Scopes(home string) ([]string, error) {
	if _, err := os.Lstat(Path(home)); err != nil {
		return nil, nil
	}
	st, err := openStore(home)
	if err != nil {
		return nil, err
	}
	defer st.close()
	rows, err := st.db.Query(`SELECT DISTINCT scope FROM records WHERE scope != '' ORDER BY scope`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DreamInput is what the dream reads of one project in the store.
type DreamInput struct {
	Memory   []Record  // the project's memory records that are not retired
	Trail    []Record  // its bug, review, retro, change and handoff records updated after since
	Feedback []Record  // global feedback records about it, updated after since
	Last     time.Time // the newest update not after now of any record in its scope (zero with none)
}

// ForDream reads project root's records for the dream: its memory, trail
// records and global feedback after since from brain.db, falling back to
// ReadTrail when brain.db holds no exported records for the scope. A project
// with neither records nor a trail.jsonl gives an empty input and creates no
// store. A record updated after now is clock skew or a planted line: it is
// neither news nor the newest update (gate review of v3.0.0, finding 2).
func ForDream(home, root string, since, now time.Time) (DreamInput, error) {
	var in DreamInput
	scope, top, err := trail.Roots(root)
	if err != nil {
		return in, nil
	}
	if err := guardExportPath(top); err != nil {
		return in, err
	}
	trailRecs, all, hasDBExported, err := readDreamStore(home, scope, since, now, &in)
	if err != nil {
		return in, err
	}
	if !hasDBExported {
		fileRecs, ok, err := ReadTrail(top)
		if err != nil {
			return in, err
		}
		if ok {
			for _, r := range fileRecs {
				all = append(all, r)
				switch r.Kind {
				case "bug", "review", "retro", "change", "handoff":
					trailRecs = append(trailRecs, r)
				}
			}
		}
	}
	in.Trail = recordsAfter(trailRecs, since, now)
	for _, r := range all {
		if t, err := time.Parse(time.RFC3339, r.Updated); err == nil && t.After(in.Last) && !t.After(now) {
			in.Last = t
		}
	}
	return in, nil
}

// recordsAfter filters recs to those updated in (since, now]. Times are
// compared parsed, never as text: a loaded line may carry any RFC 3339 offset
// (security review of the dream, finding 4).
func recordsAfter(recs []Record, since, now time.Time) []Record {
	var out []Record
	for _, r := range recs {
		if t, err := time.Parse(time.RFC3339, r.Updated); err == nil && t.After(since) && !t.After(now) {
			out = append(out, r)
		}
	}
	return out
}

func readDreamStore(home, scope string, since, now time.Time, in *DreamInput) (trailRecs, all []Record, hasDBExported bool, err error) {
	if _, err := os.Lstat(Path(home)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, false, nil
		}
		return nil, nil, false, err
	}
	st, err := openStore(home)
	if err != nil {
		return nil, nil, false, err
	}
	defer st.close()
	if in.Memory, err = st.records(`scope = ? AND kind = 'memory' AND status = 'closed'`, scope); err != nil {
		return nil, nil, false, err
	}
	if trailRecs, err = st.records(`scope = ? AND kind IN ('bug', 'review', 'retro', 'change', 'handoff')`, scope); err != nil {
		return nil, nil, false, err
	}
	fb, err := st.records(`scope = '' AND kind = 'feedback' AND project = ?`, scope)
	if err != nil {
		return nil, nil, false, err
	}
	in.Feedback = recordsAfter(fb, since, now)
	if all, err = st.records(`scope = ?`, scope); err != nil {
		return nil, nil, false, err
	}
	for _, r := range all {
		if !unexported[r.Kind] {
			hasDBExported = true
			break
		}
	}
	return trailRecs, all, hasDBExported, nil
}

// records is every record matching the WHERE clause q, oldest update first.
func (st *store) records(q string, args ...any) ([]Record, error) {
	rows, err := st.db.Query(`SELECT `+recordCols+` FROM records WHERE `+q+` ORDER BY updated, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MetricsInput is what the metrics join reads about one project.
type MetricsInput struct {
	Migrated bool     // the checkout at root holds .agents/trail.jsonl
	Trail    []Record // Migrated only: the scope's handoff, testing, review and bug records, every status
	Feedback []Record // global feedback records about the main worktree root of root (root itself when gone)
}

// ForMetrics reads root's records for the metrics join (plan
// 2026-10-25-1754 S1). The global feedback is read for every root, migrated
// or not, and for a root that is gone (D11). The trail is read only for a
// migrated checkout the operator registered under this home, through the
// file-wins load, as ForDream does: an unregistered one reads as not
// migrated, so its trail.jsonl never becomes a brain scope that the dream
// and --global would then read (ADR-068, audit-choices SD49). With no
// brain.db and no trail.jsonl the input is empty and no store is created.
// On an error Migrated is still set, so the caller knows whether the root
// had trail records to miss.
func ForMetrics(home, root string) (MetricsInput, error) {
	var in MetricsInput
	scope, top, rootErr := trail.Roots(root)
	if rootErr == nil && trail.Registered(filepath.Join(home, harness.StateDir), root) {
		ok, err := HasRecords(home, top)
		in.Migrated = err == nil && ok
	}
	if in.Migrated {
		var err error
		if in.Trail, err = metricsTrail(home, scope, top); err != nil {
			return in, err
		}
	}
	if _, err := os.Lstat(Path(home)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return in, nil
		}
		return in, err
	}
	st, err := openStore(home)
	if err != nil {
		return in, err
	}
	defer st.close()
	in.Feedback, err = st.records(`scope = '' AND kind = 'feedback' AND project = ?`, canonicalProject(root))
	return in, err
}

func metricsTrail(home, scope, top string) ([]Record, error) {
	if err := guardExportPath(top); err != nil {
		return nil, err
	}
	hasDBExported, err := hasScopeDBRecords(home, scope)
	if err != nil {
		return nil, err
	}
	if hasDBExported {
		st, err := openStore(home)
		if err != nil {
			return nil, err
		}
		defer st.close()
		return st.records(`scope = ? AND kind IN ('handoff', 'testing', 'review', 'bug')`, scope)
	}
	trailRecs, ok, err := ReadTrail(top)
	if err != nil || !ok {
		return nil, err
	}
	var out []Record
	for _, r := range trailRecs {
		switch r.Kind {
		case "handoff", "testing", "review", "bug":
			out = append(out, r)
		}
	}
	return out, nil
}

// Proposal is one dream proposal to store.
type Proposal struct{ Title, Body string }

// ProposeAll stores a project's dream proposals as global, queued `proposal`
// records about project (D15), all in one transaction, so a failure part-way
// stores none and the next run never duplicates the first ones (security
// review of the dream, finding 7). The model wrote titles and bodies, so
// each is normalized first (a control character as "?", the title cut to
// one line of maxTitle bytes): a stray byte never costs a proposal.
func ProposeAll(home, project string, ps []Proposal, now time.Time) ([]string, error) {
	if len(ps) == 0 {
		return nil, nil
	}
	st, err := openStore(home)
	if err != nil {
		return nil, err
	}
	defer st.close()
	s := &session{st: st}
	project = canonicalProject(project)
	var ids []string
	err = st.tx(func(c *sql.Conn) error {
		ids = nil
		for _, p := range ps {
			r := normalize(Record{Title: p.Title, Body: p.Body})
			id, err := s.allocate(c, "proposal", r.Title, now)
			if err != nil {
				return err
			}
			r.ID, r.Kind, r.Status, r.Date, r.Updated = id, "proposal", "queued", now.Format("2006-01-02"), now.UTC().Format(time.RFC3339)
			if err := checkRecord(r); err != nil {
				return refuse("proposal %q: %v", r.Title, err)
			}
			if _, err := c.ExecContext(context.Background(), `INSERT INTO records(scope, id, kind, status, date, title, body, project, updated)
				VALUES ('', ?, 'proposal', 'queued', ?, ?, ?, ?, ?)`, id, now.Format("2006-01-02"), r.Title, r.Body, project, now.UTC().Format(time.RFC3339)); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return nil
	})
	return ids, err
}
