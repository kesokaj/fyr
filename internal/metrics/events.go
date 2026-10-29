package metrics

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/trail"
)

// closeSlack is how long after a session's last step its handoff and commit
// still count as its own (spec A1).
const closeSlack = 30 * time.Minute

var (
	statusLine = regexp.MustCompile(`(?m)^## Status:\s*(PASS|FAIL|BLOCKED|IN PROGRESS)\b`)
	finding    = regexp.MustCompile(`\*\*\[(high|medium|low)\b`)
	hexRun     = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	// A record's time is the minute its ID was stamped with (plan
	// 2026-10-25-1754 D3); verifyTitle is the verifier's own title (D12).
	recordStamp = regexp.MustCompile(`^[A-Z]+-(\d{4}-\d{2}-\d{2})-(\d{4})(-|$)`)
	verifyTitle = regexp.MustCompile(`^Verif(ication|y)\b`)
)

type window struct {
	id         string
	start, end int64 // ms; end includes closeSlack
}

type stamped struct {
	ts  int64
	ref string
}

// putFunc stores one event, keyed by (session, kind, ref).
type putFunc func(sid, kind, ref, value string, ts int64) error

// joinEvents derives every main session's events from git, the trail and
// the feedback (spec § Data, events payloads): a migrated project's trail
// from its records, and nothing from an unmigrated one's files, whose join
// went with the crossing (plan 2026-10-27-ledger Phase 10); corrections from
// the global feedback records for every root (D11). It is idempotent: each
// event's key is (session, kind, ref), and an event whose value changes (a
// commit that became ambiguous when an overlapping session arrived) is
// updated in place. A project root that is gone or is not git gets no
// commit events and returns a note.
func (s *Store) joinEvents(home string) (notes []string, err error) {
	byRoot, err := s.mainWindows()
	if err != nil {
		return nil, err
	}
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
	if err := rewriteCorrectionRefs(tx); err != nil {
		return nil, err
	}
	put := func(sid, kind, ref, value string, ts int64) error {
		_, err := tx.Exec(`INSERT INTO events(session_id, ts, kind, ref, value) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(session_id, kind, ref) DO UPDATE SET value = excluded.value, ts = excluded.ts`, sid, ts, kind, ref, value)
		return err
	}
	for _, root := range roots {
		rootNotes, err := joinRoot(home, root, byRoot[root], put)
		if err != nil {
			return nil, err
		}
		notes = append(notes, rootNotes...)
	}
	return notes, tx.Commit()
}

// joinRecords derives a migrated project's handoff, verifier, review and bug
// events from its records (plan 2026-10-25-1754, § The join). A handoff,
// verifier log or review belongs to every session whose window overlaps the
// minute stamped in its ID; one with no valid stamp gives no event. A bug
// belongs to the session that owns the first commit its body cites. Only
// patterns are taken from a body, never its text.
func joinRecords(recs []brain.Record, ws []window, sessionOf map[string]string, put putFunc) error {
	for _, r := range recs {
		if r.Kind == "bug" {
			ts := int64(0)
			if t, err := time.ParseInLocation("2006-01-02", r.Date, time.Local); err == nil {
				ts = t.UnixMilli()
			}
			for _, h := range hexRun.FindAllString(r.Body, -1) {
				if sha, id := matchCommit(h, sessionOf); id != "" {
					if err := put(id, "bug", r.ID, sha, ts); err != nil {
						return err
					}
					break
				}
			}
			continue
		}
		ts, ok := recordTime(r.ID)
		if !ok {
			continue
		}
		var kind, value string
		switch r.Kind {
		case "handoff":
			kind = "handoff"
		case "testing":
			m := statusLine.FindStringSubmatch(r.Body)
			if m == nil || !verifyTitle.MatchString(r.Title) {
				continue
			}
			kind, value = "verifier", m[1]
		case "review":
			kind, value = "review", findings(r.Body)
		default:
			continue
		}
		for _, w := range ws {
			// The window [start, end] against the stamp's minute [ts, ts+60s).
			if w.start < ts+60_000 && ts <= w.end {
				if err := put(w.id, kind, r.ID, value, ts); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// recordTime is the minute stamped in a record's ID, read in the machine's
// zone (D15); a date-only ID or an impossible minute has none.
func recordTime(id string) (int64, bool) {
	m := recordStamp.FindStringSubmatch(id)
	if m == nil {
		return 0, false
	}
	t, err := time.ParseInLocation("2006-01-02 1504", m[1]+" "+m[2], time.Local)
	if err != nil {
		return 0, false
	}
	return t.UnixMilli(), true
}

// findings is a review's value: its findings counted by severity.
func findings(body string) string {
	n := map[string]int{}
	for _, m := range finding.FindAllStringSubmatch(body, -1) {
		n[m[1]]++
	}
	return "high=" + strconv.Itoa(n["high"]) + ",medium=" + strconv.Itoa(n["medium"]) + ",low=" + strconv.Itoa(n["low"])
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
		if err := rekey(tx, r.sid, r.kind, r.ref, fileRef(k, r.ref)); err != nil {
			return err
		}
	}
	return nil
}

// rewriteCorrectionRefs turns a correction ref stored as its feedback
// entry's ts, in any offset, into the UTC form the join now writes (D11),
// so a correction an older ingest stored from the feedback log as written
// and the record of the same entry count once. A ref that is not a time is
// left alone.
func rewriteCorrectionRefs(tx *sql.Tx) error {
	type row struct{ sid, ref string }
	rows, err := tx.Query(`SELECT session_id, ref FROM events WHERE kind = 'correction'`)
	if err != nil {
		return err
	}
	var old []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.sid, &r.ref); err != nil {
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
		if _, ref, ok := correctionRef(r.ref); ok && ref != r.ref {
			if err := rekey(tx, r.sid, "correction", r.ref, ref); err != nil {
				return err
			}
		}
	}
	return nil
}

// rekey moves one event to a new ref; when the session already holds an
// event of that kind under the new ref, the old row is a duplicate and goes.
func rekey(tx *sql.Tx, sid, kind, from, to string) error {
	res, err := tx.Exec(`UPDATE OR IGNORE events SET ref = ? WHERE session_id = ? AND kind = ? AND ref = ?`, to, sid, kind, from)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		_, err = tx.Exec(`DELETE FROM events WHERE session_id = ? AND kind = ? AND ref = ?`, sid, kind, from)
		return err
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

// correctionRef is a feedback entry's time and its event reference: the ts
// in UTC, so an entry written +02:00 in one place and Z in another is one
// correction (D11).
func correctionRef(ts string) (int64, string, bool) {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return 0, "", false
	}
	return t.UnixMilli(), t.UTC().Format(time.RFC3339Nano), true
}

// feedbackRecords is the global feedback records' times and references; a
// body that is not JSON or has no RFC 3339 ts is skipped.
func feedbackRecords(recs []brain.Record) []stamped {
	var out []stamped
	for _, r := range recs {
		var e struct {
			TS string `json:"ts"`
		}
		if json.Unmarshal([]byte(r.Body), &e) != nil {
			continue
		}
		if ts, ref, ok := correctionRef(e.TS); ok {
			out = append(out, stamped{ts: ts, ref: ref})
		}
	}
	return out
}
