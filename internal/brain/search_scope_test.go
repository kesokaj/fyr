package brain

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

// markRegistered is `fyr index --register` in root under home (ADR-068).
func markRegistered(t *testing.T, home, root string) {
	t.Helper()
	if _, err := trail.Register(filepath.Join(home, harness.StateDir), root, fixed); err != nil {
		t.Fatal(err)
	}
}

// With --global, search, --refs and --open read the global scope, the
// project's own and every other project scope whose checkout carries this
// home's marker; an unregistered project's records, a clone's included,
// answer none of them (ADR-068, audit-choices D15, ledger D13).
func TestGlobalRecordHitsOnlyRegistered(t *testing.T) {
	home := t.TempDir()
	own, reg, stranger := repo(t), repo(t), repo(t)
	for _, d := range []string{own, reg, stranger} {
		add(t, opts(d, home), "bug", "the osprey "+filepath.Base(d), "osprey BUG-001")
	}
	markRegistered(t, home, reg)
	clone := filepath.Join(t.TempDir(), "clone")
	git(t, filepath.Dir(clone), "clone", "-q", reg, clone)
	add(t, opts(clone, home), "bug", "the osprey clone", "osprey BUG-001")
	g := opts(own, home)
	g.Global = true
	scopesOf := func(hits []Hit) string {
		var b strings.Builder
		for _, h := range hits {
			b.WriteString(h.Scope + " ")
		}
		return b.String()
	}
	check := func(what string, hits []Hit, err error) {
		t.Helper()
		got := scopesOf(hits)
		if err != nil || !strings.Contains(got, own) || !strings.Contains(got, reg) || strings.Contains(got, stranger) || strings.Contains(got, clone) {
			t.Errorf("%s: scopes %q, %v; want %s and %s only", what, got, err, own, reg)
		}
	}
	hits, err := Search(g, []string{"osprey"}, "", 10)
	check("search", hits, err)
	hits, err = Refs(g, "BUG-001", "", 10)
	check("refs", hits, err)
	hits, err = OpenRecords(g, "bug", 10)
	check("open", hits, err)
}
