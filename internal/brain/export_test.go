package brain

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The export is byte-stable for the same rows whatever order they were
// written in, sorted by ID, ends in a newline, keeps the key order, and
// leaves memory out.
func TestExportByteStable(t *testing.T) {
	home := t.TempDir()
	type rec struct{ kind, id, title, body string }
	rows := []rec{
		{"bug", "BUG-002", "two", "b2"},
		{"bug", "BUG-010", "ten", "b10 <html> & \"quotes\"\nline two"},
		{"adr", "ADR-001", "one", "a1"},
		{"spec", "SPEC-fyr-record", "the spec", "s"},
		{"learning", "LEARNING-2026-10-25-1430-x", "x", "l"},
	}
	write := func(order []int) string {
		dir := repo(t)
		o := opts(dir, home)
		for _, i := range order {
			r := rows[i]
			add(t, o, r.kind, r.title, r.body, "id", r.id)
		}
		add(t, o, "memory", "local only", "never exported")
		if _, err := os.Stat(trailFile(dir)); !os.IsNotExist(err) {
			t.Fatalf("add wrote trail.jsonl without Export")
		}
		if n, err := Export(o); err != nil || n != len(rows) {
			t.Fatalf("Export: n=%d, err=%v, want %d", n, err, len(rows))
		}
		return trailText(t, dir)
	}
	a := write([]int{0, 1, 2, 3, 4})
	b := write([]int{4, 3, 2, 1, 0})
	if a != b {
		t.Fatalf("the same rows exported differently:\n%s\n---\n%s", a, b)
	}
	if !strings.HasSuffix(a, "}\n") {
		t.Errorf("no final newline")
	}
	if strings.Contains(a, "never exported") || strings.Contains(a, `"memory"`) {
		t.Errorf("memory reached the export:\n%s", a)
	}
	lines := strings.Split(strings.TrimSuffix(a, "\n"), "\n")
	var ids []string
	for _, l := range lines {
		if !strings.HasPrefix(l, `{"date":"2026-10-25","id":"`) {
			t.Errorf("key order: %s", l)
		}
		for _, k := range []string{`"date"`, `"id"`, `"kind"`, `"status"`, `"title"`, `"updated"`, `"body"`} {
			if !strings.Contains(l, k+":") {
				t.Errorf("line lacks %s: %s", k, l)
			}
		}
		if strings.Index(l, `"updated"`) > strings.Index(l, `"body"`) || strings.Index(l, `"kind"`) > strings.Index(l, `"status"`) {
			t.Errorf("key order: %s", l)
		}
		i := strings.Index(l, `"id":"`) + 6
		ids = append(ids, l[i:i+strings.Index(l[i:], `"`)])
	}
	if !sort.SliceIsSorted(ids, func(i, j int) bool { return idLess(ids[i], ids[j]) }) {
		t.Errorf("not sorted: %v", ids)
	}
	if !strings.Contains(a, `<html> & \"quotes\"\nline two`) {
		t.Errorf("the body is not written as plain JSON (no HTML escaping): %s", a)
	}
}

// Sync (ADR-091): a file synced on demand replaces the project's exported rows
// and updates records_fts; memory stays.
func TestLoadReplacesRowsFromAChangedFile(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	add(t, o, "bug", "kept", "b")
	add(t, o, "bug", "dropped", "b")
	add(t, o, "memory", "local", "m")
	if _, err := Export(o); err != nil {
		t.Fatal(err)
	}
	s := trailText(t, dir)
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	edited := strings.Replace(lines[0], `"title":"kept"`, `"title":"kept, retitled by a pull"`, 1)
	extra := `{"date":"2026-10-24","id":"ADR-063","kind":"adr","status":"accepted","title":"the brain","updated":"2026-10-24T10:00:00Z","body":"decided"}`
	if err := os.WriteFile(trailFile(dir), []byte(edited+"\n"+extra+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(o); err != nil {
		t.Fatal(err)
	}
	list, err := List(o, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range list {
		got[r.ID] = r.Title
	}
	if got["BUG-001"] != "kept, retitled by a pull" || got["ADR-063"] != "the brain" {
		t.Errorf("the file did not win: %v", got)
	}
	if _, ok := got["BUG-002"]; ok {
		t.Errorf("a row the file no longer holds survived: %v", got)
	}
	if len(got) != 3 {
		t.Errorf("rows %v, want BUG-001, ADR-063 and the memory", got)
	}
	if hits, err := Search(o, []string{"decided"}, "", 10); err != nil || len(hits) != 1 || hits[0].ID != "ADR-063" {
		t.Errorf("FTS after Sync: %+v %v, want ADR-063", hits, err)
	}
	// The next write numbers from the synced rows; Export writes them out.
	if id := add(t, o, "bug", "after", "b"); id != "BUG-002" {
		t.Errorf("after sync: %s", id)
	}
	if _, err := Export(o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(trailText(t, dir), "ADR-063") {
		t.Error("the export after a sync dropped a synced record")
	}
}

// A brain.db lost or moved to another machine: the project's records come back
// from trail.jsonl via Sync (R7, ADR-091).
func TestLostStoreRestoredFromTrail(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	add(t, o, "plan", "open plan", "- [ ] x")
	add(t, o, "bug", "a bug", "b")
	if _, err := Export(o); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{Path(home), Path(home) + "-wal", Path(home) + "-shm"} {
		os.Remove(p)
	}
	if _, err := Sync(o); err != nil {
		t.Fatal(err)
	}
	list, err := List(o, ListFilter{Open: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("restored %d open records, want 2: %+v", len(list), list)
	}
}

// A bad file is refused by Sync with its line number — a duplicate ID whose
// lines differ with both (a byte-identical pair is one record: BUG-197), a git
// conflict marker, a line that is not JSON — leaving existing records intact.
func TestLoadRefusals(t *testing.T) {
	good := `{"date":"2026-10-24","id":"BUG-001","kind":"bug","status":"open","title":"t","updated":"2026-10-24T10:00:00Z","body":"b"}`
	other := `{"date":"2026-10-24","id":"BUG-002","kind":"bug","status":"open","title":"u","updated":"2026-10-24T10:00:00Z","body":"b"}`
	for name, tc := range map[string]struct {
		file string
		want []string
	}{
		"duplicate id":    {good + "\n" + other + "\n" + strings.Replace(good, `"title":"t"`, `"title":"v"`, 1) + "\n", []string{"line 3", "line 1", "BUG-001"}},
		"conflict marker": {good + "\n<<<<<<< HEAD\n" + other + "\n=======\n>>>>>>> theirs\n", []string{"line 2", "conflict"}},
		"bad json":        {good + "\n{\"id\": \n", []string{"line 2"}},
		"unknown key":     {strings.Replace(good, `"body"`, `"extra":1,"body"`, 1) + "\n", []string{"line 1"}},
		"missing key":     {strings.Replace(good, `,"updated":"2026-10-24T10:00:00Z"`, "", 1) + "\n", []string{"line 1", "updated"}},
		"memory exported": {strings.Replace(strings.Replace(good, `"bug"`, `"memory"`, 1), `BUG-001`, `MEMORY-2026-10-24-1000-x`, 1) + "\n", []string{"line 1"}},
		"bad status":      {strings.Replace(good, `"open"`, `"maybe"`, 1) + "\n", []string{"line 1", "maybe"}},
		"bad id":          {strings.Replace(good, `BUG-001`, `ADR-001`, 1) + "\n", []string{"line 1"}},
		// An impossible day sorts first in every search (SD39 of the 2026-10-27 audit).
		"impossible date": {strings.Replace(good, `"2026-10-24","id"`, `"9999-99-99","id"`, 1) + "\n", []string{"line 1", "9999-99-99"}},
		"no such day":     {strings.Replace(good, `"2026-10-24","id"`, `"2026-02-30","id"`, 1) + "\n", []string{"line 1", "2026-02-30"}},
		// The spec's RFC 3339 rule had no row (SD45 of the 2026-10-27 audit).
		"bad updated": {strings.Replace(good, `"2026-10-24T10:00:00Z"`, `"yesterday"`, 1) + "\n", []string{"line 1", "yesterday"}},
	} {
		t.Run(name, func(t *testing.T) {
			dir, home := repo(t), t.TempDir()
			o := opts(dir, home)
			os.MkdirAll(filepath.Join(dir, ".agents"), 0o755)
			if err := os.WriteFile(trailFile(dir), []byte(tc.file), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Sync(o)
			if !IsRefusal(err) {
				t.Fatalf("sync on a bad file: %v, want a refusal", err)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("refusal %q lacks %q", err, w)
				}
			}
			if got, _ := os.ReadFile(trailFile(dir)); string(got) != tc.file {
				t.Errorf("a refused sync rewrote the file")
			}
			// Fixed by hand, it syncs.
			os.WriteFile(trailFile(dir), []byte(good+"\n"), 0o644)
			if _, err := Sync(o); err != nil {
				t.Fatalf("sync after fix: %v", err)
			}
			if list, err := List(o, ListFilter{}); err != nil || len(list) != 1 {
				t.Errorf("after the fix: %v %v", list, err)
			}
		})
	}
}

// A symlinked .agents or trail.jsonl is refused: the export never writes
// through a link a commit could carry. Each link is proven to exist first.
func TestSymlinkedExportPathRefused(t *testing.T) {
	t.Run(".agents", func(t *testing.T) {
		dir, home := repo(t), t.TempDir()
		elsewhere := t.TempDir()
		if err := os.Symlink(elsewhere, filepath.Join(dir, ".agents")); err != nil {
			t.Fatal(err)
		}
		if fi, err := os.Lstat(filepath.Join(dir, ".agents")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("the fixture's symlink is not there: %v", err)
		}
		_, err := Add(opts(dir, home), AddInput{Kind: "learning", Title: "t", Body: strings.NewReader("b")})
		if !IsRefusal(err) || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("add with a symlinked .agents: %v, want a refusal", err)
		}
		if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
			t.Errorf("wrote through the link: %v", entries)
		}
	})
	t.Run("trail.jsonl", func(t *testing.T) {
		dir, home := repo(t), t.TempDir()
		os.MkdirAll(filepath.Join(dir, ".agents"), 0o755)
		target := filepath.Join(t.TempDir(), "outside.jsonl")
		os.WriteFile(target, nil, 0o644)
		if err := os.Symlink(target, trailFile(dir)); err != nil {
			t.Fatal(err)
		}
		if fi, err := os.Lstat(trailFile(dir)); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("the fixture's symlink is not there: %v", err)
		}
		_, err := Add(opts(dir, home), AddInput{Kind: "learning", Title: "t", Body: strings.NewReader("b")})
		if !IsRefusal(err) || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("add with a symlinked trail.jsonl: %v, want a refusal", err)
		}
		if b, _ := os.ReadFile(target); len(b) != 0 {
			t.Errorf("wrote through the link: %q", b)
		}
	})
}

// get's output goes through printable: a record cannot drive the terminal
// or forge a line, while its newlines and tabs stay.
func TestGetOutputPrintable(t *testing.T) {
	r := Record{ID: "BUG-001", Kind: "bug", Status: "open", Date: "2026-10-25",
		Title: "evil\x1b[31m title‮", Body: "line one\x1b]0;x\x07\n\tindented forged"}
	got := FormatGet(r)
	if strings.ContainsAny(got, "\x1b\x07‮ ") {
		t.Errorf("control characters survived: %q", got)
	}
	want := "BUG-001  bug  open  2026-10-25  evil?[31m title?\nline one?]0;x?\n\tindented?forged\n"
	if got != want {
		t.Errorf("FormatGet = %q, want %q", got, want)
	}
}

func TestExportSyncRefusalsAndReadScope(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)

	// Sync without .agents/trail.jsonl is refused.
	if _, err := Sync(o); !IsRefusal(err) || !strings.Contains(err.Error(), "no .agents/trail.jsonl") {
		t.Fatalf("Sync without trail.jsonl: %v, want refusal", err)
	}
	// Export and Sync with --global are usage errors.
	g := o
	g.Global = true
	if _, err := Export(g); !IsUsage(err) {
		t.Errorf("Export --global: %v, want usage error", err)
	}
	if _, err := Sync(g); !IsUsage(err) {
		t.Errorf("Sync --global: %v, want usage error", err)
	}

	// HasRecords, ReadScope, LastUpdated on an empty project create no store.
	if ok, err := HasRecords(home, dir); err != nil || ok {
		t.Errorf("HasRecords empty: %v %v", ok, err)
	}
	if recs, ok, err := ReadScope(home, dir); err != nil || ok || len(recs) != 0 {
		t.Errorf("ReadScope empty: %v %v %v", recs, ok, err)
	}
	if _, ok, err := LastUpdated(home, dir); err != nil || ok {
		t.Errorf("LastUpdated empty: %v %v", ok, err)
	}
	if _, err := os.Lstat(Path(home)); !os.IsNotExist(err) {
		t.Errorf("read-only helpers created brain.db: %v", err)
	}

	// Memory-only scope does not count as HasRecords.
	add(t, o, "memory", "local", "m")
	if ok, err := HasRecords(home, dir); err != nil || ok {
		t.Errorf("HasRecords memory-only: %v %v, want false", ok, err)
	}

	// Adding an exported-kind record makes HasRecords, ReadScope, and LastUpdated succeed from brain.db without trail.jsonl.
	add(t, o, "bug", "db only bug", "body")
	if ok, err := HasRecords(home, dir); err != nil || !ok {
		t.Errorf("HasRecords with DB bug: %v %v, want true", ok, err)
	}
	recs, ok, err := ReadScope(home, dir)
	if err != nil || !ok || len(recs) != 1 || recs[0].ID != "BUG-001" {
		t.Errorf("ReadScope from DB: %+v %v %v", recs, ok, err)
	}
	if ts, ok, err := LastUpdated(home, dir); err != nil || !ok || !ts.Equal(fixed) {
		t.Errorf("LastUpdated from DB: %v %v %v, want %v", ts, ok, err, fixed)
	}
}
