package brain

import (
	"strings"
	"testing"
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
