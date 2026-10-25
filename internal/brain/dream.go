package brain

import (
	"context"
	"database/sql"
	"os"
	"time"
)

// Scopes is every project scope the store holds records for: the roots the
// dream visits besides the checkouts the file index registered (brain plan
// Phase 4). It creates no store.
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
	Last     time.Time // the newest update of any record in its scope (zero with none)
}

// ForDream reads project root's records for the dream: its checkout's
// trail.jsonl is loaded first when it changed (the file wins, D21), then the
// memory, the trail records and the global feedback after since. A project
// with neither records nor a trail.jsonl gives an empty input and creates no
// store.
func ForDream(home, root string, since time.Time) (DreamInput, error) {
	var in DreamInput
	st, scope, err := openForSearch(Options{Home: home, Dir: root})
	if err != nil || st == nil {
		return in, err
	}
	defer st.close()
	if scope == "" {
		return in, nil
	}
	read := func(q string, args ...any) ([]Record, error) {
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
	// Times are compared parsed, never as text: a loaded line may carry any
	// RFC 3339 offset (security review of the dream, finding 4).
	after := func(recs []Record) []Record {
		var out []Record
		for _, r := range recs {
			if t, err := time.Parse(time.RFC3339, r.Updated); err == nil && t.After(since) {
				out = append(out, r)
			}
		}
		return out
	}
	if in.Memory, err = read(`scope = ? AND kind = 'memory' AND status = 'closed'`, scope); err != nil {
		return in, err
	}
	trail, err := read(`scope = ? AND kind IN ('bug', 'review', 'retro', 'change', 'handoff')`, scope)
	if err != nil {
		return in, err
	}
	in.Trail = after(trail)
	fb, err := read(`scope = '' AND kind = 'feedback' AND project = ?`, scope)
	if err != nil {
		return in, err
	}
	in.Feedback = after(fb)
	all, err := read(`scope = ?`, scope)
	if err != nil {
		return in, err
	}
	for _, r := range all {
		if t, err := time.Parse(time.RFC3339, r.Updated); err == nil && t.After(in.Last) {
			in.Last = t
		}
	}
	return in, nil
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
