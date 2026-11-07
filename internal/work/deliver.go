package work

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

// recordsSub is the directory under .agents/scratch/work/<stamp>/<name>/
// that step 8 writes the worker's records to.
const recordsSub = "records"

// recordTail is the ID grammar after a handoff's or change record's prefix,
// fyr record's own (internal/brain's slugTail, which this package does not
// link): it also keeps every ID one plain file name.
var recordTail = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

// maxRecords is how many records one worker's records/ holds, as
// maxDrafts caps its drafts: each is two files in the lead's checkout.
const maxRecords = 50

// recordPrefix is each delivered kind's ID prefix.
var recordPrefix = map[string]string{"handoff": "HANDOFF-", "change": "CHANGE-"}

// deliver is step 8's second half (BUG-500, ADR-501): the handoff and change
// records the worker added, which live only in its sandbox's brain.db or on
// its branch, written into the lead's scratch beside the drafts, one
// <ID>.title and one <ID>.md each, for the lead to store with `fyr record
// add <kind> --id <ID> --title-file <ID>.title --body-file <ID>.md`. The verb writes
// no store. An auto worker gets none: fyr auto reads its result and removes
// its drafts directory. False means a copy that could not be made: the
// sandbox is kept.
func (w *worker) deliver() bool {
	if w.o.Auto {
		return true
	}
	var recs []Record
	if w.res.Handoff != nil {
		recs = append(recs, *w.res.Handoff)
	}
	recs = append(recs, w.res.Changes...)
	if len(recs) == 0 {
		return true
	}
	recs, over := capRecords(recs)
	if len(over) > 0 {
		w.b.Note("records not copied (over %d): %s", maxRecords, named(over))
	}
	n, refused, err := writeRecords(w.leadDir, recs)
	if len(refused) > 0 {
		w.b.Note("records not copied (an ID not its kind's grammar): %s", named(refused))
	}
	if err != nil {
		w.b.Note("the records were not copied: %v", err)
		return false
	}
	if n > 0 {
		w.res.Records = n
		w.res.RecordsDir = filepath.ToSlash(filepath.Join(".agents", "scratch", "work", w.stamp, w.p.k.Name, recordsSub)) + "/"
	}
	return true
}

// leadDir is .agents/scratch/work/<stamp>/<name>/ in the lead's checkout,
// made at its first use by the drafts or the records and reused after.
func (w *worker) leadDir() (*os.Root, error) {
	if w.dest == nil {
		d, err := draftsDest(w.o.Root, w.stamp, w.p.k.Name)
		if err != nil {
			return nil, err
		}
		w.dest = d
	}
	return w.dest, nil
}

// closeLeadDir closes what leadDir opened.
func (w *worker) closeLeadDir() {
	if w.dest != nil {
		w.dest.Close()
		w.dest = nil
	}
}

// writeRecords writes each record whose ID is its kind's grammar into
// dir's records/, which it makes and which must not exist yet, and returns
// how many it wrote and the refused IDs, quoted: they are the worker's text.
func writeRecords(dir func() (*os.Root, error), recs []Record) (int, []string, error) {
	var refused []string
	var dest *os.Root
	n := 0
	for _, r := range recs {
		prefix, ok := recordPrefix[r.Kind]
		if !ok || len(r.ID) <= len(prefix) || r.ID[:len(prefix)] != prefix || !recordTail.MatchString(r.ID[len(prefix):]) {
			refused = append(refused, fmt.Sprintf("%.128q", r.ID))
			continue
		}
		if dest == nil {
			var err error
			if dest, err = recordsDest(dir); err != nil {
				return n, refused, err
			}
			defer dest.Close()
		}
		if err := writeDraft(dest, r.ID+".title", []byte(r.Title+"\n")); err != nil {
			return n, refused, err
		}
		if err := writeDraft(dest, r.ID+".md", []byte(r.Body)); err != nil {
			return n, refused, err
		}
		n++
	}
	return n, refused, nil
}

// recordsDest makes records/ under dir, refusing one that is already there:
// nothing the session planted is written through.
func recordsDest(dir func() (*os.Root, error)) (*os.Root, error) {
	parent, err := dir()
	if err != nil {
		return nil, err
	}
	if _, err := parent.Lstat(recordsSub); err == nil {
		return nil, fmt.Errorf("%s exists and is not a directory this run made", recordsSub)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err := parent.Mkdir(recordsSub, 0o700); err != nil {
		return nil, err
	}
	return parent.OpenRoot(recordsSub)
}

// capRecords keeps the first maxRecords, the handoff first, and returns the
// IDs of the rest, quoted: they are the worker's text.
func capRecords(recs []Record) ([]Record, []string) {
	if len(recs) <= maxRecords {
		return recs, nil
	}
	var over []string
	for _, r := range recs[maxRecords:] {
		over = append(over, fmt.Sprintf("%.128q", r.ID))
	}
	return recs[:maxRecords], over
}
