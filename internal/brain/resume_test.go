package brain

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResumeGolden(t *testing.T) {
	dir := repo(t)
	home := t.TempDir()
	writeFile := func(name, content string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("AGENTS.md", "# AGENTS.md — demo\n\nFollow the trail.\n")
	writeFile("PRD.md", "# PRD.md\n\n## Must\n- R1: Demo.\n")
	writeFile("TODO.md", "# TODO.md\n\n## Now\n\n- [ ] Step 1\n- [x] Step 0\n\n## Next\n\n- Later\n")

	day := func(d int) Options {
		return Options{
			Home: home,
			Dir:  dir,
			Now:  func() time.Time { return time.Date(2026, 11, d, 10, 0, 0, 0, time.UTC) },
		}
	}

	add(t, day(1), "plan", "Closed plan", "- [x] Done", "id", "PLAN-2026-11-01-closed")
	add(t, day(2), "plan", "Open plan", "- [ ] Task A\n- [x] Task B", "id", "PLAN-2026-11-02-open")
	add(t, day(2), "bug", "Open bug", "Fix it", "id", "BUG-001", "status", "open")
	add(t, day(2), "spec", "Current spec", "Spec body", "id", "SPEC-demo-api", "status", "current")
	add(t, day(1), "handoff", "Older handoff", "Old summary", "id", "HANDOFF-2026-11-01-old")
	add(t, day(3), "handoff", "Newest handoff", "## Summary\nReady for Step 1.", "id", "HANDOFF-2026-11-03-new")
	add(t, day(1), "change", "Change one", "Body 1", "id", "CHANGE-2026-11-01-one")
	add(t, day(2), "change", "Change two", "Body 2", "id", "CHANGE-2026-11-02-two")
	add(t, day(3), "change", "Change three", "Body 3", "id", "CHANGE-2026-11-03-three")
	add(t, day(4), "change", "Change four", "Body 4", "id", "CHANGE-2026-11-04-four")
	if _, err := MemoryAdd(day(3), MemoryEntry{
		Type:        "feedback",
		Name:        "table-tests",
		Description: "Prefer table-driven tests",
	}, strings.NewReader("Use subtests for edge cases.")); err != nil {
		t.Fatal(err)
	}
	add(t, day(2), "memory", "Retired note", "type: user\n\nOld detail", "id", "MEMORY-retired", "status", "retired")

	got, err := Resume(day(4))
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	want := strings.Join([]string{
		"## AGENTS.md",
		"# AGENTS.md — demo",
		"",
		"Follow the trail.",
		"",
		"## Open records",
		"SPEC-demo-api  spec  current  2026-11-02  Current spec",
		"PLAN-2026-11-02-open  plan  open  2026-11-02  Open plan",
		"BUG-001  bug  open  2026-11-02  Open bug",
		"",
		"## Newest handoff",
		"HANDOFF-2026-11-03-new  handoff  -  2026-11-03  Newest handoff",
		"## Summary",
		"Ready for Step 1.",
		"",
		"## TODO.md Now",
		"## Now",
		"",
		"- [ ] Step 1",
		"- [x] Step 0",
		"",
		"## Open plans",
		"PLAN-2026-11-02-open  plan  open  2026-11-02  Open plan",
		"- [ ] Task A",
		"- [x] Task B",
		"",
		"## Recent changes",
		"CHANGE-2026-11-04-four  change  closed  2026-11-04  Change four",
		"Body 4",
		"",
		"CHANGE-2026-11-03-three  change  closed  2026-11-03  Change three",
		"Body 3",
		"",
		"CHANGE-2026-11-02-two  change  closed  2026-11-02  Change two",
		"Body 2",
		"",
		"## Memory",
		"- table-tests (feedback) — Prefer table-driven tests",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("Resume golden mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestResumeReadOnlyDoesNotCreateDB(t *testing.T) {
	dir := repo(t)
	home := t.TempDir()
	out, err := Resume(opts(dir, home))
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if _, err := os.Stat(Path(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("brain.db created by read-only Resume: err=%v", err)
	}
	for _, want := range []string{
		"(missing: AGENTS.md)",
		"(missing: PRD.md)",
		"## Open records\n(none)",
		"## Newest handoff\n(none)",
		"## TODO.md Now\n(missing: TODO.md)",
		"## Open plans\n(none)",
		"## Recent changes\n(none)",
		"## Memory\n(none)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestResumeFileEraNoticeAndTrailFallback(t *testing.T) {
	dir := repo(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs", "handoffs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "handoffs", "2026-10-01-init.md"), []byte("# Handoff\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := Resume(opts(dir, home))
	if err != nil {
		t.Fatalf("Resume file-era: %v", err)
	}
	if !strings.Contains(out, "(trail is still files; run 'fyr record import')") {
		t.Errorf("expected file-era notice in:\n%s", out)
	}

	// Write a trail.jsonl in a clean checkout and verify Resume reads it without creating brain.db.
	dir2 := repo(t)
	exportHome := t.TempDir()
	add(t, opts(dir2, exportHome), "handoff", "Exported handoff", "Trail body", "id", "HANDOFF-2026-10-25-exp")
	if _, err := Export(opts(dir2, exportHome)); err != nil {
		t.Fatal(err)
	}
	out2, err := Resume(opts(dir2, home))
	if err != nil {
		t.Fatalf("Resume trail fallback: %v", err)
	}
	if _, err := os.Stat(Path(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("brain.db created during trail fallback: err=%v", err)
	}
	if !strings.Contains(out2, "HANDOFF-2026-10-25-exp  handoff  -  2026-10-25  Exported handoff\nTrail body") {
		t.Errorf("trail fallback missing exported handoff:\n%s", out2)
	}
}

func TestResumeRefusesNonCheckout(t *testing.T) {
	_, err := Resume(opts(t.TempDir(), t.TempDir()))
	if !IsRefusal(err) {
		t.Fatalf("err = %v, want IsRefusal", err)
	}
	dir := repo(t)
	home := t.TempDir()
	target := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(target, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AGENTS.md", "TODO.md"} {
		link := filepath.Join(dir, name)
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		_, err := Resume(opts(dir, home))
		_ = os.Remove(link)
		if err == nil {
			t.Fatalf("Resume followed symlinked %s, want error", name)
		}
	}
}
