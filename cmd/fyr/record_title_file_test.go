package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// add --title-file reads the title from a file, one trailing newline
// dropped (ADR-501); with --title too it is a usage error; a missing file,
// one over the cap, or a title of two lines is refused and stores nothing.
func TestRecordAddTitleFile(t *testing.T) {
	recordRepo(t)
	home := t.TempDir()
	dir := t.TempDir()
	file := func(name, text string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	title := "Handoff — 2026-11-03 — a worker's $(title) `with` 'quotes'"
	good := file("good.title", title+"\n")
	id, err := recordCmd(t, "the body\n", "record", "add", "handoff", "--id", "HANDOFF-2026-11-03-0900-w", "--title-file", good, "--home", home)
	if err != nil || strings.TrimSpace(id) != "HANDOFF-2026-11-03-0900-w" {
		t.Fatalf("add --title-file: %v %q", err, id)
	}
	if out, err := recordCmd(t, "", "record", "get", "HANDOFF-2026-11-03-0900-w", "--home", home); err != nil || !strings.Contains(out, "  "+title+"\n") || !strings.Contains(out, "the body") {
		t.Errorf("get: %v %q", err, out)
	}
	if out, err := recordCmd(t, "b\n", "record", "add", "change", "--title", "t", "--title-file", good, "--home", home); exitOf(err) != 2 || !strings.Contains(out, "not both") {
		t.Errorf("both: exit %d %q", exitOf(err), out)
	}
	for name, p := range map[string]string{
		"missing":   filepath.Join(dir, "none.title"),
		"over cap":  file("big.title", strings.Repeat("a", maxTitleFile+1)),
		"two lines": file("two.title", "one\ntwo\n"),
		"empty":     file("empty.title", "\n"),
	} {
		if out, err := recordCmd(t, "b\n", "record", "add", "change", "--title-file", p, "--home", home); err == nil {
			t.Errorf("%s: stored %q", name, out)
		}
	}
	if out, _ := recordCmd(t, "", "record", "list", "--kind", "change", "--home", home); strings.TrimSpace(out) != "" {
		t.Errorf("a refused add stored a record: %q", out)
	}
	if out, err := recordCmd(t, "", "record", "edit", "HANDOFF-2026-11-03-0900-w", "--title-file", good, "--home", home); exitOf(err) != 2 {
		t.Errorf("edit takes no --title-file: exit %d %q", exitOf(err), out)
	}
}
