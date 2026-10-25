package brain

import (
	"os"
	"strings"
	"testing"
	"time"
)

// D30: `fyr search --refs` and `--open` answer from records too, since the
// trail is records: the skills ask `--refs <path> --kind bug` before
// touching an area.
func TestRefsFindsRecordsNamingAPathOrID(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	b1 := add(t, o, "bug", "export race", "The bug was in `internal/brain/export.go:120`; see ADR-063.")
	add(t, o, "bug", "unrelated", "Nothing about it: internal/brain/exports.go is another file.")
	c := add(t, o, "change", "a change", "Touched internal/brain/export.go and fixed "+b1+".")

	hits, err := Refs(o, "internal/brain/export.go", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(hits); got != b1+" "+c && got != c+" "+b1 {
		t.Errorf("records naming the path: %q", got)
	}
	hits, _ = Refs(o, "internal/brain/export.go", "bug", 10)
	if got := ids(hits); got != b1 {
		t.Errorf("--kind bug: %q", got)
	}
	// An ID: its own record first, then the records naming it.
	hits, _ = Refs(o, b1, "", 10)
	if got := ids(hits); got != b1+" "+c {
		t.Errorf("refs of %s: %q", b1, got)
	}
	// A directory with a trailing slash matches every path under it.
	hits, _ = Refs(o, "internal/brain/", "", 10)
	if len(hits) != 3 {
		t.Errorf("refs of a directory: %q", ids(hits))
	}
}

func TestOpenRecords(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	open := add(t, o, "bug", "still broken", "x")
	fixed := add(t, o, "bug", "done", "x", "status", "fixed")
	plan := add(t, o, "plan", "a plan", "- [ ] left")
	hits, err := OpenRecords(o, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	got := ids(hits)
	if !strings.Contains(got, open) || !strings.Contains(got, plan) || strings.Contains(got, fixed) {
		t.Errorf("open records: %q", got)
	}
	hits, _ = OpenRecords(o, "plan", 10)
	if ids(hits) != plan {
		t.Errorf("--kind plan: %q", ids(hits))
	}
}

func ids(hits []Hit) string {
	var out []string
	for _, h := range hits {
		out = append(out, h.ID)
	}
	return strings.Join(out, " ")
}

// D31: among near-equal matches the newer record ranks first; a clearly
// better older match still wins.
func TestSearchPrefersTheNewerOfNearEqualRecords(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	old := opts(dir, home)
	old.Now = func() time.Time { return time.Now().AddDate(0, -2, 0) }
	o := opts(dir, home)
	o.Now = time.Now
	a := add(t, old, "bug", "zebra crossing broke", "the zebra crossing broke again")
	b := add(t, o, "bug", "zebra crossing broke", "the zebra crossing broke again")
	hits, err := Search(o, []string{"zebra", "crossing"}, "", 10)
	if err != nil || len(hits) != 2 || hits[0].ID != b {
		t.Fatalf("the newer of two equal records first: %q %v (old %s, new %s)", ids(hits), err, a, b)
	}
	c := add(t, old, "bug", "okapi okapi okapi", "okapi okapi okapi okapi okapi")
	add(t, o, "bug", "something else", "a long body that mentions an okapi once among many other words of no interest at all here")
	hits, _ = Search(o, []string{"okapi"}, "", 10)
	if len(hits) == 0 || hits[0].ID != c {
		t.Errorf("a clearly better older match still ranks first: %q", ids(hits))
	}
}

// D32: `fyr status` dates the last retro from the global retro records too;
// with no store it creates none.
func TestLastRetro(t *testing.T) {
	home := t.TempDir()
	if _, ok := LastRetro(home); ok {
		t.Fatal("no store, no retro")
	}
	if _, err := os.Lstat(Path(home)); !os.IsNotExist(err) {
		t.Fatalf("LastRetro created a store: %v", err)
	}
	g := Options{Home: home, Dir: t.TempDir(), Global: true, Now: func() time.Time { return fixed }}
	if _, err := Add(g, AddInput{Kind: "retro", Title: "Retro index — x (2026-10-25)", Project: "/p/x", Body: strings.NewReader("{}")}); err != nil {
		t.Fatal(err)
	}
	got, ok := LastRetro(home)
	if !ok || got.Format("2006-01-02") != "2026-10-25" {
		t.Errorf("LastRetro = %v, %v", got, ok)
	}
}
