package brain

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Security review 2026-10-25 (reviews/2026-10-25-security-brain-store.md),
// one test per finding fixed.

// Finding 1: a planted trail.jsonl of blank lines or one huge line must not
// cost memory in proportion to its line count; the parse streams.
func TestLoadBoundedMemory(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	os.MkdirAll(filepath.Join(dir, ".agents"), 0o755)
	const size = 32 << 20
	if err := os.WriteFile(trailFile(dir), []byte(strings.Repeat("\n", size)), 0o644); err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if _, err := List(o, ListFilter{}); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if grew := after.TotalAlloc - before.TotalAlloc; grew > size/2 {
		t.Errorf("loading %d MiB of blank lines allocated %d MiB; the parse must stream", size>>20, grew>>20)
	}
}

func TestLoadRefusesAnOverlongLine(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".agents"), 0o755)
	line := `{"date":"2026-10-24","id":"BUG-001","kind":"bug","status":"open","title":"t","updated":"2026-10-24T10:00:00Z","body":"` + strings.Repeat("a", maxLine) + `"}`
	os.WriteFile(trailFile(dir), []byte("\n"+line+"\n"), 0o644)
	_, err := List(opts(dir, home), ListFilter{})
	if !IsRefusal(err) || !strings.Contains(err.Error(), "line 2: over") {
		t.Fatalf("a line over %d bytes is refused with its number before it is decoded, got %v", maxLine, err)
	}
}

// A valid line longer than the reader's 64 KiB buffer loads whole, and the
// line after it keeps its number.
func TestLoadTakesALongValidLine(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".agents"), 0o755)
	body := strings.Repeat("é", 300<<10/2)
	long := `{"date":"2026-10-24","id":"BUG-001","kind":"bug","status":"open","title":"t","updated":"2026-10-24T10:00:00Z","body":"` + body + `"}`
	bad := `{"date":"2026-10-24","id":"BUG-001","kind":"bug","status":"open","title":"t","updated":"2026-10-24T10:00:00Z","body":"b"}`
	os.WriteFile(trailFile(dir), []byte(long+"\r\n"+bad), 0o644)
	if _, err := List(opts(dir, home), ListFilter{}); !IsRefusal(err) || !strings.Contains(err.Error(), "line 2: duplicate ID BUG-001 (first at line 1)") {
		t.Fatalf("line numbers across a long line: %v", err)
	}
	os.WriteFile(trailFile(dir), []byte(long), 0o644)
	recs, _, err := Get(opts(dir, home), []string{"BUG-001"})
	if err != nil || recs[0].Body != body {
		t.Fatalf("a %d KiB line with no final newline loads whole: %v", len(long)>>10, err)
	}
}

// The growth guard: bytes past the cap fail the read even when the size
// check passed (a file that grew after its fstat).
func TestCappedReaderRefusesGrowth(t *testing.T) {
	old := maxTrail
	maxTrail = 1 << 10
	defer func() { maxTrail = old }()
	if _, err := io.ReadAll(&capped{r: strings.NewReader(strings.Repeat("x", 1<<10)), left: maxTrail}); err != nil {
		t.Errorf("exactly the cap reads: %v", err)
	}
	if _, err := io.ReadAll(&capped{r: strings.NewReader(strings.Repeat("x", 1<<10+1)), left: maxTrail}); !IsRefusal(err) {
		t.Errorf("one byte past the cap: %v, want a refusal", err)
	}
}

func TestLoadRefusesAnOversizeFile(t *testing.T) {
	old := maxTrail
	maxTrail = 1 << 10
	defer func() { maxTrail = old }()
	dir, home := repo(t), t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".agents"), 0o755)
	os.WriteFile(trailFile(dir), []byte(strings.Repeat("\n", 2<<10)), 0o644)
	// The size check names the file's path; the growth guard (above) does not.
	if _, err := List(opts(dir, home), ListFilter{}); !IsRefusal(err) || !strings.Contains(err.Error(), trailFile(dir)+" is over") {
		t.Fatalf("a file over the cap is refused by its size, got %v", err)
	}
}

// Finding 2: a title is capped like a body.
func TestTitleLimit(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	long := strings.Repeat("a", maxTitle+1)
	if _, err := Add(o, AddInput{Kind: "bug", Title: long, Body: strings.NewReader("b")}); !IsUsage(err) {
		t.Errorf("add with a %d-byte title: %v, want a usage error", len(long), err)
	}
	if _, err := Add(o, AddInput{Kind: "bug", Title: strings.Repeat("a", maxTitle), Body: strings.NewReader("b")}); err != nil {
		t.Errorf("a title of exactly the cap is taken: %v", err)
	}
	line := `{"date":"2026-10-24","id":"BUG-009","kind":"bug","status":"open","title":"` + long + `","updated":"2026-10-24T10:00:00Z","body":"b"}`
	os.WriteFile(trailFile(dir), []byte(line+"\n"), 0o644)
	if _, err := List(o, ListFilter{}); !IsRefusal(err) || !strings.Contains(err.Error(), "line 1") {
		t.Errorf("a loaded title over the cap is refused with its line, got %v", err)
	}
}

// Finding 4: the load never reads through a symlink, even one swapped in
// after the guard's Lstat.
func TestOpenNonBlockingRefusesASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "elsewhere")
	os.WriteFile(target, []byte("x"), 0o644)
	link := filepath.Join(dir, "trail.jsonl")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the fixture is not a symlink: %v", err)
	}
	if f, err := openNonBlocking(link); err == nil {
		f.Close()
		t.Fatal("openNonBlocking followed a symlink")
	}
}

// Finding 5: stored text cannot carry the snippet's match marks (or any
// other C0 control but tab and newline).
func TestControlCharactersRefused(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	if _, err := Add(o, AddInput{Kind: "bug", Title: "t", Body: strings.NewReader("a \x01mark\x02 b")}); !IsRefusal(err) {
		t.Errorf("a body with \\x01: %v, want a refusal", err)
	}
	if _, err := Add(o, AddInput{Kind: "bug", Title: "a \x1b[31m b", Body: strings.NewReader("b")}); !IsUsage(err) {
		t.Errorf("a title with ESC: %v, want a usage error", err)
	}
	if _, err := Add(o, AddInput{Kind: "bug", Title: "t", Body: strings.NewReader("tab\there\nnext line\r\n")}); err != nil {
		t.Errorf("tab, newline and CRLF are text: %v", err)
	}
	line := `{"date":"2026-10-24","id":"BUG-009","kind":"bug","status":"open","title":"t","updated":"2026-10-24T10:00:00Z","body":"\u0001mark\u0002"}`
	os.WriteFile(trailFile(dir), []byte(line+"\n"), 0o644)
	if _, err := List(o, ListFilter{}); !IsRefusal(err) || !strings.Contains(err.Error(), "line 1") {
		t.Errorf("a loaded body with \\x01 is refused with its line, got %v", err)
	}
}

// Finding 6: with --global, two projects of the same base name are told
// apart.
func TestGlobalLabelsTellScopesApart(t *testing.T) {
	home := t.TempDir()
	a := filepath.Join(t.TempDir(), "same")
	b := filepath.Join(t.TempDir(), "same")
	for _, d := range []string{a, b} {
		os.MkdirAll(d, 0o755)
		git(t, d, "init", "-q")
		add(t, opts(d, home), "bug", "zebra crossing", "zebra")
	}
	markRegistered(t, home, b)
	o := opts(a, home)
	o.Global = true
	hits, err := Search(o, []string{"zebra"}, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].Label == hits[1].Label {
		t.Errorf("two scopes named %q print the same label: %+v", "same", hits)
	}
}

// Finding 7: no unexported kind reaches the export, whichever way a row
// got into a project scope.
func TestEveryUnexportedKindStaysOut(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	add(t, o, "bug", "visible", "visible")
	s, err := openSession(o, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	for kind := range unexported {
		err := s.write(func(c *sql.Conn) error {
			_, err := c.ExecContext(context.Background(), `INSERT INTO records(scope, id, kind, status, date, title, body, project, updated)
				VALUES (?, ?, ?, 'closed', '2026-10-25', 'secret', 'secret-`+kind+`', '', '2026-10-25T00:00:00Z')`, s.scope, prefixes[kind]+"-x", kind)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	text := trailText(t, dir)
	if !strings.Contains(text, "visible") || strings.Contains(text, "secret") {
		t.Errorf("an unexported kind reached trail.jsonl:\n%s", text)
	}
}
