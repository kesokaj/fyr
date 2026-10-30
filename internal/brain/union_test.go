package brain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BUG-197: two branches whose new records sort on both sides of one
// unchanged line, merged under merge=union, carry that line twice. The read
// takes the byte-identical pair as one record, and the next export drops the
// copy. The trail is written by the real generator, never by hand.
func TestUnionMergeOfSortedExports(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte(".agents/trail.jsonl merge=union\n"), 0o644)
	add(t, o, "guide", "the fixture", "g", "id", "GUIDE-fixture")
	git(t, dir, "add", ".gitattributes", ".agents/trail.jsonl")
	git(t, dir, "commit", "-qm", "base")
	git(t, dir, "switch", "-qc", "theirs")
	add(t, o, "change", "p2 change", "c2")
	add(t, o, "handoff", "p2 handoff", "h2")
	git(t, dir, "commit", "-qam", "p2")
	git(t, dir, "switch", "-q", "-")
	add(t, o, "change", "p1 change", "c1")
	add(t, o, "handoff", "p1 handoff", "h1")
	git(t, dir, "commit", "-qam", "p1")
	git(t, dir, "merge", "-q", "--squash", "theirs")
	merged, _ := os.ReadFile(trailFile(dir))
	if n := strings.Count(string(merged), `"id":"GUIDE-fixture"`); n != 2 {
		t.Fatalf("the union carries GUIDE-fixture %d times, want 2 (the repro):\n%s", n, merged)
	}

	list, err := List(o, ListFilter{})
	if err != nil {
		t.Fatalf("list after the union merge: %v", err)
	}
	if len(list) != 5 {
		t.Fatalf("listed %d records, want 5: %+v", len(list), list)
	}
	add(t, o, "bug", "the next write", "b")
	got, _ := os.ReadFile(trailFile(dir))
	if n := strings.Count(string(got), `"id":"GUIDE-fixture"`); n != 1 {
		t.Errorf("the next export carries GUIDE-fixture %d times, want 1", n)
	}
}

// A byte-identical pair is one record wherever it sits; a same-ID pair that
// differs by one byte is still refused, naming both lines.
func TestDuplicateLines(t *testing.T) {
	good := `{"date":"2026-10-24","id":"BUG-001","kind":"bug","status":"open","title":"t","updated":"2026-10-24T10:00:00Z","body":"b"}`
	other := `{"date":"2026-10-24","id":"BUG-002","kind":"bug","status":"open","title":"u","updated":"2026-10-24T10:00:00Z","body":"b"}`
	recs, err := ParseTrail(strings.NewReader(good + "\n" + other + "\n" + good + "\n"))
	if err != nil || len(recs) != 2 || recs[0].ID != "BUG-001" || recs[1].ID != "BUG-002" {
		t.Fatalf("a byte-identical pair: %+v, %v, want BUG-001 then BUG-002", recs, err)
	}
	// A CRLF first copy, and a last copy with no final newline.
	if recs, err := ParseTrail(strings.NewReader(good + "\r\n" + other + "\n" + good)); err != nil || len(recs) != 2 {
		t.Fatalf("a pair differing only in its line ending: %+v, %v, want two records", recs, err)
	}
	changed := strings.Replace(good, `"body":"b"`, `"body":"c"`, 1)
	if _, err := ParseTrail(strings.NewReader(good + "\n" + other + "\n" + changed + "\n")); !IsRefusal(err) || !strings.Contains(err.Error(), "line 3: duplicate ID BUG-001 (first at line 1)") {
		t.Errorf("a same-ID pair that differs: %v, want the duplicate refused", err)
	}
}
