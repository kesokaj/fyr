package brain

import (
	"os"
	"strings"
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
	after := since.UTC().Format(time.RFC3339)
	if in.Memory, err = read(`scope = ? AND kind = 'memory' AND status = 'closed'`, scope); err != nil {
		return in, err
	}
	if in.Trail, err = read(`scope = ? AND kind IN ('bug', 'review', 'retro', 'change', 'handoff') AND updated > ?`, scope, after); err != nil {
		return in, err
	}
	if in.Feedback, err = read(`scope = '' AND kind = 'feedback' AND project = ? AND updated > ?`, scope, after); err != nil {
		return in, err
	}
	var last string
	if err := st.db.QueryRow(`SELECT coalesce(max(updated), '') FROM records WHERE scope = ?`, scope).Scan(&last); err != nil {
		return in, err
	}
	if t, err := time.Parse(time.RFC3339, last); err == nil {
		in.Last = t
	}
	return in, nil
}

// Propose stores one dream proposal as a global, queued `proposal` record
// about project (D15) and returns its ID. The model wrote the title and the
// body, so both are normalized first (a control character as "?", the title
// cut to one line of maxTitle bytes): a stray byte never costs the proposal.
func Propose(home, project, title, body string, now time.Time) (string, error) {
	r := normalize(Record{Title: title, Body: body})
	return Add(Options{Home: home, Global: true, Now: func() time.Time { return now }},
		AddInput{Kind: "proposal", Title: r.Title, Project: project, Body: strings.NewReader(r.Body)})
}
