package brain

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// ForMetrics gives the metrics join a registered migrated checkout's
// handoff, testing, review and bug records, and for every root, migrated, unmigrated or gone,
// the global feedback about it alone (plan 2026-10-25-1754 S1, D11). With no
// store and no trail.jsonl it gives nothing and creates nothing.
func TestForMetrics(t *testing.T) {
	home := t.TempDir()
	migrated, unmigrated := repo(t), repo(t)
	os.MkdirAll(filepath.Join(unmigrated, ".agents", "docs", "handoffs"), 0o755)
	gone := filepath.Join(t.TempDir(), "gone")

	// No brain.db yet: an unmigrated checkout and a gone root read nothing.
	for _, root := range []string{unmigrated, gone} {
		in, err := ForMetrics(home, root)
		if err != nil || in.Migrated || in.Trail != nil || in.Feedback != nil {
			t.Errorf("%s with no store: %+v %v, want an empty input", root, in, err)
		}
	}
	if _, err := os.Lstat(Path(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ForMetrics created the store: %v", err)
	}

	o := opts(migrated, home)
	for _, kind := range []string{"handoff", "testing", "review", "bug", "change"} {
		add(t, o, kind, kind+" record", "body")
	}
	markRegistered(t, home, migrated)
	g := opts(t.TempDir(), home)
	g.Global = true
	for _, p := range []string{migrated, unmigrated, gone, repo(t)} {
		add(t, g, "feedback", "about "+filepath.Base(p), `{"ts":"2026-10-25T14:30:00Z"}`, "project", p)
	}

	in, err := ForMetrics(home, migrated)
	if err != nil || !in.Migrated {
		t.Fatalf("migrated: %+v %v", in, err)
	}
	var kinds []string
	for _, r := range in.Trail {
		kinds = append(kinds, r.Kind)
	}
	sort.Strings(kinds)
	if len(kinds) != 4 || kinds[0] != "bug" || kinds[1] != "handoff" || kinds[2] != "review" || kinds[3] != "testing" {
		t.Errorf("migrated trail kinds %v, want bug handoff review testing", kinds)
	}
	if len(in.Feedback) != 1 || in.Feedback[0].Title != "about "+filepath.Base(migrated) {
		t.Errorf("migrated feedback %+v, want its own one", in.Feedback)
	}

	for _, root := range []string{unmigrated, gone} {
		in, err := ForMetrics(home, root)
		if err != nil || in.Migrated || in.Trail != nil {
			t.Errorf("%s: %+v %v, want not migrated and no trail", root, in, err)
		}
		if len(in.Feedback) != 1 || in.Feedback[0].Title != "about "+filepath.Base(root) {
			t.Errorf("%s feedback %+v, want its own one", root, in.Feedback)
		}
	}
}

// TestKindOfID: every kind's prefix maps back to its kind (TEST is testing);
// a word with no known prefix and dash is no kind.
func TestKindOfID(t *testing.T) {
	for k, p := range prefixes {
		if got := KindOfID(p + "-2026-10-25-1900-x"); got != k {
			t.Errorf("KindOfID(%s-…) = %q, want %q", p, got, k)
		}
	}
	for _, id := range []string{"FOO-1", "HANDOFF", "handoff-x", "", "-x", "TESTING-x"} {
		if got := KindOfID(id); got != "" {
			t.Errorf("KindOfID(%q) = %q, want none", id, got)
		}
	}
}

// An unregistered checkout holding .agents/trail.jsonl reads as not
// migrated: its trail is not loaded, so no scope of it appears in the store,
// and its feedback is still read; registered, it joins (audit-choices SD49).
func TestForMetricsUnregisteredReadsNoTrail(t *testing.T) {
	home, src := t.TempDir(), t.TempDir()
	stranger := repo(t)
	add(t, opts(stranger, src), "handoff", "a stranger's handoff", "body")
	if _, err := Export(opts(stranger, src)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(trailPath(stranger)); err != nil {
		t.Fatal(err)
	}
	g := opts(t.TempDir(), home)
	g.Global = true
	add(t, g, "feedback", "about the stranger", `{"ts":"2026-10-25T14:30:00Z"}`, "project", stranger)
	in, err := ForMetrics(home, stranger)
	if err != nil || in.Migrated || in.Trail != nil || len(in.Feedback) != 1 {
		t.Errorf("unregistered: %+v %v, want not migrated, no trail, its feedback", in, err)
	}
	if scopes, err := Scopes(home); err != nil || len(scopes) != 0 {
		t.Errorf("an unregistered trail.jsonl became a scope: %q %v", scopes, err)
	}
	markRegistered(t, home, stranger)
	if in, err := ForMetrics(home, stranger); err != nil || !in.Migrated || len(in.Trail) != 1 {
		t.Errorf("registered: %+v %v, want its handoff", in, err)
	}
}
