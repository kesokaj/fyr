package work

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// maxTrail caps the trail.jsonl a branch may hold: it is agent-written and
// read whole.
const maxTrail = 256 << 20

// maxLine is one trail line's cap, fyr record's own (internal/brain's
// maxLine: a 1 MiB body escaped, and the other keys).
const maxLine = 6<<20 + 64<<10

// trailLine is one exported record as it is read: a missing key is nil.
type trailLine struct {
	Date    *string `json:"date"`
	ID      *string `json:"id"`
	Kind    *string `json:"kind"`
	Status  *string `json:"status"`
	Title   *string `json:"title"`
	Updated *string `json:"updated"`
	Body    *string `json:"body"`
}

// trailAt is the records of rev's trail.jsonl by ID; a rev without the
// file has none, and one git cannot read is an error, so a damaged base
// never passes the record scope open. The blob is named once, then read
// with cat-file, which runs no filter or textconv, through a capped reader.
func trailAt(root, rev, runner string) (map[string]Record, error) {
	ls, err := leadGit(root, "ls-tree", "-z", rev, "--", trailRel).Output()
	if err != nil {
		return nil, fmt.Errorf("at %s: git ls-tree: %v", short(rev), err)
	}
	if len(ls) == 0 {
		return map[string]Record{}, nil
	}
	meta, _, _ := strings.Cut(string(ls), "\t")
	f := strings.Fields(meta)
	if len(f) != 3 || f[1] != "blob" || (f[0] != "100644" && f[0] != "100755") {
		return nil, fmt.Errorf("at %s: %s is not a regular file", short(rev), trailRel)
	}
	cmd := capMemory(leadGit(root, "cat-file", "blob", f[2]), runner)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	blob, rerr := io.ReadAll(io.LimitReader(out, maxTrail+1))
	io.Copy(io.Discard, out)
	if err := cmd.Wait(); err != nil || rerr != nil {
		return nil, fmt.Errorf("at %s: git cat-file: %v", short(rev), errors.Join(err, rerr))
	}
	if len(blob) > maxTrail {
		return nil, fmt.Errorf("at %s: over %d MiB", short(rev), maxTrail>>20)
	}
	return parseTrail(blob)
}

// parseTrail decodes one JSON object per line with exactly the record's
// seven keys, a real date and an RFC 3339 updated, each line at most
// maxLine; a second line of one ID is refused unless it is byte-identical,
// as fyr record reads it (BUG-197: a merge=union base). The kind, ID grammar
// and status tables are internal/brain's, which this package cannot link
// (ADR-063): the lead's `fyr record list` after the merge checks those.
func parseTrail(b []byte) (map[string]Record, error) {
	out := map[string]Record{}
	lines := map[string][]byte{}
	for n, l := range bytes.Split(b, []byte("\n")) {
		if len(bytes.TrimSpace(l)) == 0 {
			continue
		}
		r, err := parseLine(l)
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", n+1, err)
		}
		l = bytes.TrimRight(l, "\r")
		if first, dup := lines[r.ID]; dup {
			if !bytes.Equal(first, l) {
				return nil, fmt.Errorf("line %d: duplicate ID %s", n+1, r.ID)
			}
			continue
		}
		lines[r.ID] = l
		out[r.ID] = r
	}
	return out, nil
}

func parseLine(l []byte) (Record, error) {
	if len(l) > maxLine {
		return Record{}, fmt.Errorf("over %d bytes", maxLine)
	}
	var t trailLine
	dec := json.NewDecoder(bytes.NewReader(l))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return Record{}, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return Record{}, errors.New("more than one JSON value")
	}
	for _, k := range []struct {
		name string
		v    *string
	}{{"date", t.Date}, {"id", t.ID}, {"kind", t.Kind}, {"status", t.Status}, {"title", t.Title}, {"updated", t.Updated}, {"body", t.Body}} {
		if k.v == nil {
			return Record{}, fmt.Errorf("no %q key", k.name)
		}
	}
	r := Record{ID: *t.ID, Kind: *t.Kind, Status: *t.Status, Date: *t.Date, Title: *t.Title, Updated: *t.Updated, Body: *t.Body}
	if r.ID == "" {
		return Record{}, errors.New("no id")
	}
	if d, err := time.Parse(time.DateOnly, r.Date); err != nil || d.Format(time.DateOnly) != r.Date {
		return Record{}, fmt.Errorf("date %q is not YYYY-MM-DD", r.Date)
	}
	if _, err := time.Parse(time.RFC3339, r.Updated); err != nil {
		return Record{}, fmt.Errorf("updated %q is not RFC 3339", r.Updated)
	}
	return r, nil
}

// clockSkew is how far past the window an added record's updated may lie:
// the worker's fyr reads this machine's clock on the host runner, and a
// container's (a VM's on the Mac) on the docker one (D140).
const clockSkew = time.Minute

// window is one worker's span on the wall clock, from its start to the
// scope check (D140), and the lead's zone, which the worker is given as
// its TZ (workers D144): a date is held only when it is the day, in loc,
// of an instant of the window. A nil loc holds no date.
type window struct {
	from, to time.Time
	loc      *time.Location
}

// outside names r's updated or date when the window does not hold it, ""
// when it does; parseLine has checked both. updated must also be the UTC
// form fyr record writes: the store sorts it as text, so an offset or a
// fraction in the window would still rank after the lead's own stamps.
func (w window) outside(r Record) string {
	from, to := w.from.Add(-clockSkew).Truncate(time.Second), w.to.Add(clockSkew)
	span := from.UTC().Format(time.RFC3339) + ".." + to.UTC().Format(time.RFC3339)
	u, err := time.Parse(time.RFC3339, r.Updated)
	if err != nil || u.UTC().Format(time.RFC3339) != r.Updated {
		return fmt.Sprintf("updated %.128q not in fyr record's UTC form", r.Updated)
	}
	if u.Before(from) || u.After(to) {
		return fmt.Sprintf("updated %.128q outside %s", r.Updated, span)
	}
	if w.loc == nil {
		return fmt.Sprintf("date %q not checked: the window has no zone", r.Date)
	}
	if from.In(w.loc).Format(time.DateOnly) <= r.Date && r.Date <= to.In(w.loc).Format(time.DateOnly) {
		return ""
	}
	return fmt.Sprintf("date %q not a day of %s in %s", r.Date, span, w.loc)
}
