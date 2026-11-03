package work

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRecordsReachTheLead is BUG-500: a worker in a DB-only checkout stores
// its handoff and change records with fyr record add only, in its sandbox's
// brain.db, and never exports them. After Run returns and the sandbox is
// gone, each of them must be in the lead's scratch, and the lead must be able
// to store it and read it back with fyr record get, title and body intact.
func TestRecordsReachTheLead(t *testing.T) {
	r := newRig(t)
	r.git("-C", r.lead, "rm", "-q", ".agents/trail.jsonl")
	r.git("-C", r.lead, "commit", "-qm", "db-only")
	r.on("change", "")
	r.on("noexport", "")
	res := r.run(r.kickoff("fix-a", "src/a.txt"))
	wantReasons(t, res)
	if res.Handoff == nil || len(res.Changes) != 1 {
		t.Fatalf("result %+v\n%s", res, r.out.String())
	}
	stamp := fmt.Sprintf("2026-10-29-180000-%d", os.Getpid())
	dir := filepath.ToSlash(filepath.Join(".agents", "scratch", "work", stamp, "fix-a", "records")) + "/"
	if res.RecordsDir != dir || res.Records != 2 {
		t.Errorf("records %d in %q, want 2 in %q", res.Records, res.RecordsDir, dir)
	}
	if want := "work: fix-a: records " + dir + " (2 records)"; !strings.Contains(r.out.String(), "\n"+want+"\n") {
		t.Errorf("the contract lacks %q:\n%s", want, r.out.String())
	}
	leadHome := filepath.Join(r.tmp, "lead-home")
	for _, rec := range []Record{*res.Handoff, res.Changes[0]} {
		base := filepath.Join(r.lead, filepath.FromSlash(dir), rec.ID)
		body, err := os.Open(base + ".md")
		if err != nil {
			t.Fatalf("%s did not reach the lead: %v", rec.ID, err)
		}
		add := exec.Command(realFyr(t), "record", "add", rec.Kind, "--id", rec.ID, "--title-file", base+".title", "--home", leadHome)
		add.Dir, add.Stdin = r.lead, body
		out, err := add.CombinedOutput()
		body.Close()
		if err != nil || strings.TrimSpace(string(out)) != rec.ID {
			t.Fatalf("record add %s in the lead: %v\n%s", rec.ID, err, out)
		}
		get := exec.Command(realFyr(t), "record", "get", rec.ID, "--home", leadHome)
		get.Dir = r.lead
		out, err = get.CombinedOutput()
		if err != nil || !strings.Contains(string(out), rec.Title) || !strings.Contains(string(out), strings.TrimSpace(rec.Body)) {
			t.Errorf("record get %s in the lead: %v\n%s", rec.ID, err, out)
		}
	}
}

// TestRecordsNotDeliveredForAuto: an auto worker's records are read from
// the result by fyr auto itself, which removes its drafts directory after
// it, so the verb writes no records directory for it.
func TestRecordsNotDeliveredForAuto(t *testing.T) {
	root := t.TempDir()
	w := &worker{o: Options{Root: root, Auto: true}, stamp: "s"}
	w.p.k.Name = "n"
	w.res.Changes = []Record{{ID: "CHANGE-2026-11-03-0900-ok", Kind: "change", Title: "ok", Body: "fine"}}
	if !w.deliver() || w.res.Records != 0 || w.res.RecordsDir != "" {
		t.Errorf("auto: deliver wrote %d records to %q", w.res.Records, w.res.RecordsDir)
	}
	if _, err := os.Lstat(filepath.Join(root, ".agents")); !os.IsNotExist(err) {
		t.Errorf("auto: the lead has .agents: %v", err)
	}
}

// TestWriteRecordsRefusesAnUnsafeID: a record whose ID is not its kind's
// grammar never names a file in the lead; the rest are still written, the
// title as one line and the body as it was stored.
func TestWriteRecordsRefusesAnUnsafeID(t *testing.T) {
	root := t.TempDir()
	recs := []Record{{ID: "HANDOFF-../../x", Kind: "handoff", Title: "t", Body: "b"},
		{ID: "CHANGE-2026-11-03-0900-ok", Kind: "change", Title: "ok", Body: "fine\n"},
		{ID: "HANDOFF-2026-11-03-0900-wrong-kind", Kind: "change", Title: "t", Body: "b"},
		{ID: "CHANGE-Upper", Kind: "change", Title: "t", Body: "b"}}
	n, refused, err := writeRecords(func() (*os.Root, error) { return draftsDest(root, "s", "n") }, recs)
	if err != nil || n != 1 || len(refused) != 3 {
		t.Fatalf("writeRecords: %d written, refused %q, %v", n, refused, err)
	}
	dir := filepath.Join(root, ".agents", "scratch", "work", "s", "n", "records")
	if got, err := os.ReadFile(filepath.Join(dir, "CHANGE-2026-11-03-0900-ok.title")); err != nil || string(got) != "ok\n" {
		t.Errorf("title file %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "CHANGE-2026-11-03-0900-ok.md")); err != nil || string(got) != "fine\n" {
		t.Errorf("body file %q, %v", got, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("records directory holds %d files, want 2", len(entries))
	}
	if _, err := os.Lstat(filepath.Join(root, ".agents", "scratch", "x.md")); !os.IsNotExist(err) {
		t.Errorf("a refused ID wrote outside: %v", err)
	}
}

// TestWriteRecordsRefusesAPlantedDir: a records/ that is already there, a
// directory or a link out of the checkout, is never written through.
func TestWriteRecordsRefusesAPlantedDir(t *testing.T) {
	recs := []Record{{ID: "CHANGE-2026-11-03-0900-ok", Kind: "change", Title: "ok", Body: "b"}}
	for _, plant := range []string{"dir", "link"} {
		root, outside := t.TempDir(), t.TempDir()
		parent := filepath.Join(root, ".agents", "scratch", "work", "s", "n")
		if err := os.MkdirAll(parent, 0o700); err != nil {
			t.Fatal(err)
		}
		var err error
		if plant == "dir" {
			err = os.Mkdir(filepath.Join(parent, recordsSub), 0o700)
		} else {
			err = os.Symlink(outside, filepath.Join(parent, recordsSub))
		}
		if err != nil {
			t.Fatal(err)
		}
		n, _, err := writeRecords(func() (*os.Root, error) { return os.OpenRoot(parent) }, recs)
		if err == nil || n != 0 {
			t.Errorf("%s: wrote %d, err %v; want a refusal", plant, n, err)
		}
		for _, d := range []string{outside, filepath.Join(parent, recordsSub)} {
			if entries, _ := os.ReadDir(d); len(entries) != 0 {
				t.Errorf("%s: %s holds %d files, want none", plant, d, len(entries))
			}
		}
	}
}

// TestCapRecords: at most maxRecords are written, the handoff first; the
// rest are only named.
func TestCapRecords(t *testing.T) {
	recs := []Record{{ID: "HANDOFF-h", Kind: "handoff"}}
	for i := 0; i < maxRecords; i++ {
		recs = append(recs, Record{ID: fmt.Sprintf("CHANGE-c%d", i), Kind: "change"})
	}
	kept, over := capRecords(recs)
	if len(kept) != maxRecords || kept[0].ID != "HANDOFF-h" || len(over) != 1 || over[0] != `"CHANGE-c49"` {
		t.Errorf("kept %d (first %s), over %q", len(kept), kept[0].ID, over)
	}
	if kept, over := capRecords(recs[:3]); len(kept) != 3 || over != nil {
		t.Errorf("under the cap: kept %d, over %q", len(kept), over)
	}
}
