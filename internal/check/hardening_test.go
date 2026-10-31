package check

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Rule (b)'s reads of trail.jsonl's history start no textconv program the
// checkout's own .gitattributes picks (security review 2026-10-25, finding
// 1); a plain git log -G does, which proves the fixture's driver is live.
func TestSpecRuleRunsNoTextconv(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "textconv-ran")
	prog := filepath.Join(t.TempDir(), "fake-conv")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\ntouch '"+marker+"'\ncat \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := repo(t, map[string]string{
		".gitattributes":      ".agents/trail.jsonl diff=probe\n",
		"internal/a.go":       "package a\n",
		".agents/trail.jsonl": specTrail(t, "SPEC-a", "2026-10-25T10:00:00Z", "`internal/a.go`"),
	})
	commit(t, dir, map[string]string{"internal/a.go": "package a\n\nvar X = 1\n"})
	git(t, dir, "config", "diff.probe.textconv", prog)
	if out, err := exec.Command("git", "-C", dir, "log", "-G", `"id":"SPEC-a"`, "--format=%H", "--", ".agents/trail.jsonl").CombinedOutput(); err != nil {
		t.Fatalf("plain git log: %v\n%s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the fixture does not run its textconv on a plain git log -G, so it proves nothing: %v", err)
	}
	os.Remove(marker)
	same(t, run(t, dir).Findings, "SPEC-a:0 spec-stale")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("fyr check ran the checkout's textconv program")
	}
}

// A stamp with a numeric offset, which RFC 3339 and the trail allow, is a
// literal in -G's extended regular expression: the spec is still found and
// goes stale (code and security review 2026-10-25).
func TestSpecRuleOffsetStamp(t *testing.T) {
	dir := repo(t, map[string]string{
		"internal/a.go":       "package a\n",
		".agents/trail.jsonl": specTrail(t, "SPEC-a", "2026-10-25T12:00:00+02:00", "`internal/a.go`"),
	})
	same(t, run(t, dir).Findings)
	commit(t, dir, map[string]string{"internal/a.go": "package a\n\nvar X = 1\n"})
	same(t, run(t, dir).Findings, "SPEC-a:0 spec-stale")
}

// A git call of rule (b) that fails leaves a note naming the spec, never a
// silent pass (code review 2026-10-25, finding 2). The failures are made by
// removing one loose object each call needs and the other does not.
func TestSpecRuleGitFailureIsANote(t *testing.T) {
	fixture := func(t *testing.T) string {
		dir := repo(t, map[string]string{
			"internal/x/f.go":     "package x\n",
			".agents/trail.jsonl": specTrail(t, "SPEC-a", "2026-10-25T10:00:00Z", "`internal/x/f.go`"),
		})
		commit(t, dir, map[string]string{"internal/x/f.go": "package x\n\nvar X = 1\n"})
		same(t, run(t, dir).Findings, "SPEC-a:0 spec-stale")
		return dir
	}
	drop := func(t *testing.T, dir, rev string) {
		h := git(t, dir, "rev-parse", rev)
		if err := os.Remove(filepath.Join(dir, ".git", "objects", h[:2], h[2:])); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct{ name, rev, fails, note string }{
		// rev-list compares internal/x's trees; the trail's history reads
		// only .agents.
		{"rev-list", "HEAD:internal/x", "rev-list --count HEAD~1..HEAD -- internal/x/f.go", "SPEC-a: git rev-list failed: spec not checked"},
		// The bisection reads the first commit's trail.jsonl blob, as git log
		// -G did.
		{"log", "HEAD~1:.agents/trail.jsonl", "log -G SPEC-a --format=%H -- .agents/trail.jsonl", "SPEC-a: trail history unreadable: spec not checked"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := fixture(t)
			drop(t, dir, c.rev)
			if out, err := exec.Command("git", append([]string{"-C", dir}, strings.Fields(c.fails)...)...).CombinedOutput(); err == nil {
				t.Fatalf("git %s still succeeds, so the fixture proves nothing:\n%s", c.fails, out)
			}
			rep := run(t, dir)
			same(t, rep.Findings)
			if !strings.Contains(notes(rep), c.note) {
				t.Errorf("notes %q, want %q", notes(rep), c.note)
			}
		})
	}
}

// A trailing slash after a symlink that leaves the checkout is stripped
// before os.Root sees the name, so a Go before 1.25.12 (GO-2026-4970) cannot
// follow it out: the span is reported as not resolving inside the checkout
// (security review 2026-10-25, finding 2).
func TestDocsRuleTrailingSlashAfterASymlink(t *testing.T) {
	outside := t.TempDir()
	os.Mkdir(filepath.Join(outside, "sub"), 0o755)
	dir := repo(t, map[string]string{"internal/a.go": "package a\n", "README.md": "`internal/out//` `internal/out///`\n"})
	if err := os.Symlink(outside, filepath.Join(dir, "internal", "out")); err != nil {
		t.Fatal(err)
	}
	rep := run(t, dir)
	same(t, rep.Findings, "README.md:1 path", "README.md:1 path")
	for _, f := range rep.Findings {
		if !strings.Contains(f.Text, "internal/out does not resolve inside this checkout") {
			t.Errorf("finding %q does not name internal/out as leaving the checkout", f.Text)
		}
	}
}

// A line of unclosed backtick runs of distinct lengths is scanned in one
// pass (security review 2026-10-25, finding 4): the quadratic scan took
// seconds on this line, the linear one milliseconds.
func TestSpansLinearOnUnclosedRuns(t *testing.T) {
	var b strings.Builder
	for k := 1; k <= 3000; k++ {
		b.WriteString(strings.Repeat("`", k))
		b.WriteByte('x')
	}
	l := b.String()
	start := time.Now()
	if got := spans(l, 1); len(got) != 0 {
		t.Fatalf("spans found %d spans on a line of unclosed runs", len(got))
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("spans took %v over a %d-byte line of unclosed runs, want under 1s", d, len(l))
	}
}
