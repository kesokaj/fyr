package brain

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ReadTrail reads every record of a trail the store wrote, and creates no
// brain.db: the home stays empty after the read (ADR-064).
func TestReadTrail(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	want := map[string]bool{
		add(t, o, "bug", "one", "first"):                    true,
		add(t, o, "learning", "two", "second"):              true,
		add(t, o, "spec", "three", "third", "id", "SPEC-x"): true,
	}
	if _, err := Export(o); err != nil {
		t.Fatal(err)
	}
	readHome := t.TempDir()
	t.Setenv("HOME", readHome)
	recs, ok, err := ReadTrail(dir)
	if err != nil || !ok {
		t.Fatalf("ReadTrail: ok %v, err %v", ok, err)
	}
	if len(recs) != len(want) {
		t.Fatalf("read %d records, want %d", len(recs), len(want))
	}
	for _, r := range recs {
		if !want[r.ID] || r.Body == "" || r.Updated == "" {
			t.Errorf("unexpected record %+v", r)
		}
	}
	if entries, _ := os.ReadDir(readHome); len(entries) != 0 {
		t.Errorf("ReadTrail wrote under HOME: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(dir, ".agents", "scratch")); !os.IsNotExist(err) {
		t.Errorf("ReadTrail wrote into the checkout: %v", err)
	}
}

func TestReadTrailNoFile(t *testing.T) {
	recs, ok, err := ReadTrail(repo(t))
	if err != nil || ok || recs != nil {
		t.Fatalf("no trail: %v %v %v, want nil false nil", recs, ok, err)
	}
}

// A symlinked trail.jsonl and a bad line are refusals, the bad line named.
func TestReadTrailRefusals(t *testing.T) {
	good := `{"date":"2026-10-24","id":"BUG-001","kind":"bug","status":"open","title":"t","updated":"2026-10-24T10:00:00Z","body":"b"}`
	t.Run("symlink", func(t *testing.T) {
		dir := repo(t)
		os.MkdirAll(filepath.Join(dir, ".agents"), 0o755)
		target := filepath.Join(t.TempDir(), "outside.jsonl")
		os.WriteFile(target, []byte(good+"\n"), 0o644)
		if err := os.Symlink(target, trailFile(dir)); err != nil {
			t.Fatal(err)
		}
		if fi, err := os.Lstat(trailFile(dir)); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("the fixture's symlink is not there: %v", err)
		}
		if _, _, err := ReadTrail(dir); !IsRefusal(err) || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("a symlinked trail: %v, want a refusal", err)
		}
	})
	t.Run("bad line", func(t *testing.T) {
		dir := repo(t)
		os.MkdirAll(filepath.Join(dir, ".agents"), 0o755)
		os.WriteFile(trailFile(dir), []byte(good+"\n{\"id\": \n"), 0o644)
		_, ok, err := ReadTrail(dir)
		if !IsRefusal(err) || !strings.Contains(err.Error(), "line 2") || ok {
			t.Fatalf("a bad line: ok %v, %v, want a refusal naming line 2", ok, err)
		}
	})
}

// ParseTrail gives ReadTrail's records on the same bytes, and its refusals:
// a bad line named, a duplicate ID whose lines differ, and bytes past the cap (plan fyr-eval
// D28).
func TestParseTrail(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	add(t, o, "bug", "one", "first")
	add(t, o, "guide", "two", "second", "id", "GUIDE-x")
	if _, err := Export(o); err != nil {
		t.Fatal(err)
	}
	want, ok, err := ReadTrail(dir)
	if err != nil || !ok {
		t.Fatalf("ReadTrail: ok %v, err %v", ok, err)
	}
	data, err := os.ReadFile(trailFile(dir))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseTrail(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ParseTrail: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseTrail %+v\nReadTrail %+v", got, want)
	}

	good := `{"date":"2026-10-24","id":"BUG-001","kind":"bug","status":"open","title":"t","updated":"2026-10-24T10:00:00Z","body":"b"}`
	for _, c := range []struct{ name, in, want string }{
		{"bad line", good + "\n{\"id\": \n", "line 2"},
		{"duplicate", good + "\n" + strings.Replace(good, `"body":"b"`, `"body":"c"`, 1) + "\n", "duplicate ID BUG-001"},
	} {
		if _, err := ParseTrail(strings.NewReader(c.in)); !IsRefusal(err) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want a refusal naming %q", c.name, err, c.want)
		}
	}
	old := maxTrail
	maxTrail = int64(len(good) + 1)
	defer func() { maxTrail = old }()
	if _, err := ParseTrail(strings.NewReader(good + "\n")); err != nil {
		t.Errorf("exactly the cap parses: %v", err)
	}
	if _, err := ParseTrail(strings.NewReader(good + "\n\n")); !IsRefusal(err) || !strings.Contains(err.Error(), "over") {
		t.Errorf("one byte past the cap: %v, want the over-size refusal", err)
	}
}
