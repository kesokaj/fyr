package harness

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestInstallCorrectsTheModeOfAnUnchangedFile: same bytes is not the same
// file. A deployed copy left group-writable stays that way on the
// "unchanged" branch, so the machine that has the problem is the one that
// never gets the fix (security review of v1.0.0, round 2, which measured it
// on a real install).
func TestInstallCorrectsTheModeOfAnUnchangedFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root's umask games make the mode assertions meaningless")
	}
	repo := newRepo(t)
	home := t.TempDir()
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Install(DirSource(repo), home, files, false, &buf); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(home, AgyRoot, "skills", "alpha", "SKILL.md")
	if err := os.Chmod(victim, 0o666); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := Install(DirSource(repo), home, files, false, &buf); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(victim)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o022 != 0 {
		t.Errorf("a second install left %s mode %o — group or other writable", victim, info.Mode().Perm())
	}
	// And a dry run changes nothing.
	if err := os.Chmod(victim, 0o666); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := Install(DirSource(repo), home, files, true, &buf); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(victim); info.Mode().Perm() != 0o666 {
		t.Errorf("--dry-run changed a mode: %o", info.Mode().Perm())
	}
}

// install names each file it changes, on every run: `add` for a file that
// did not exist, `update` for changed bytes (or a previous file it could not
// read), `mode` for a permission it corrects, `remove` for a file the source
// no longer has. Unchanged files print nothing; --dry-run prints the same
// lines and writes nothing (plan sh-agent-works-everywhere, Phase 1, D4, D6-D9).
func TestInstallNamesEveryChange(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()
	lines := func(out string, verb string) []string {
		var got []string
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, verb+" ") {
				got = append(got, l)
			}
		}
		return got
	}
	plan := func() []File {
		t.Helper()
		files, err := Plan(DirSource(repo), []byte("#!/bin/sh\necho fyr\n"))
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	install := func(files []File, dryRun bool) string {
		t.Helper()
		var out bytes.Buffer
		if err := Install(DirSource(repo), home, files, dryRun, &out); err != nil {
			t.Fatalf("install: %v\n%s", err, out.String())
		}
		return out.String()
	}

	// 1. First install: one `add` per managed file, nothing else.
	files := plan()
	out := install(files, false)
	if adds := lines(out, "add"); len(adds) != len(files) {
		t.Errorf("first install: %d `add` lines for %d files:\n%s", len(adds), len(files), out)
	}
	for _, verb := range []string{"update", "remove", "mode"} {
		if got := lines(out, verb); len(got) != 0 {
			t.Errorf("first install printed %s lines: %v", verb, got)
		}
	}
	if !strings.Contains(out, strconv.Itoa(len(files))+" written, 0 unchanged") {
		t.Errorf("first install summary:\n%s", out)
	}

	// A second, identical install: nothing named at all.
	out = install(files, false)
	for _, verb := range []string{"add", "update", "remove", "mode"} {
		if got := lines(out, verb); len(got) != 0 {
			t.Errorf("identical install printed %s lines: %v", verb, got)
		}
	}

	// 2. One changed source file: `update` for each deployed copy of it, nothing else.
	alpha := filepath.Join(repo, "skills", "alpha", "SKILL.md")
	if err := os.WriteFile(alpha, []byte("---\nname: alpha\ndescription: changed\n---\nnew body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files = plan()
	out = install(files, false)
	updates := lines(out, "update")
	if len(updates) == 0 {
		t.Errorf("a changed source file printed no `update` line:\n%s", out)
	}
	for _, l := range updates {
		if !strings.HasSuffix(l, "skills/alpha/SKILL.md") {
			t.Errorf("unexpected update line: %q", l)
		}
	}
	for _, verb := range []string{"add", "remove", "mode"} {
		if got := lines(out, verb); len(got) != 0 {
			t.Errorf("changed-file install printed %s lines: %v", verb, got)
		}
	}

	// 3. A removed source file: `remove … (no longer in the source)` on the real run.
	if err := os.RemoveAll(filepath.Join(repo, "skills", "beta")); err != nil {
		t.Fatal(err)
	}
	files = plan()
	out = install(files, false)
	removes := lines(out, "remove")
	if len(removes) == 0 {
		t.Errorf("a removed source file printed no `remove` line:\n%s", out)
	}
	for _, l := range removes {
		if !strings.Contains(l, "skills/beta/") || !strings.HasSuffix(l, "(no longer in the source)") {
			t.Errorf("unexpected remove line: %q", l)
		}
	}

	// 4. A mode-only difference: one `mode` line, still counted unchanged.
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 file and its umask blurs the mode cases")
	}
	victim := filepath.Join(home, AgyRoot, "skills", "alpha", "SKILL.md")
	if err := os.Chmod(victim, 0o666); err != nil {
		t.Fatal(err)
	}
	out = install(files, false)
	modes := lines(out, "mode")
	if len(modes) != 1 || !strings.Contains(modes[0], "skills/alpha/SKILL.md (0666 -> 0644)") {
		t.Errorf("mode-only difference: want one `mode … (0666 -> 0644)` line, got %v:\n%s", modes, out)
	}
	if !strings.Contains(out, "0 written, ") {
		t.Errorf("a mode-only correction counted as written:\n%s", out)
	}
	if info, _ := os.Stat(victim); info.Mode().Perm() != 0o644 {
		t.Errorf("mode not corrected: %o", info.Mode().Perm())
	}

	// 5. A previous file that cannot be read is written over and the line says why (D6, D9).
	if err := os.Chmod(victim, 0o000); err != nil {
		t.Fatal(err)
	}
	out = install(files, false)
	updates = lines(out, "update")
	if len(updates) != 1 || !strings.Contains(updates[0], "skills/alpha/SKILL.md (previous file unreadable: ") {
		t.Errorf("unreadable previous file: want one `update … (previous file unreadable: …)` line, got %v:\n%s", updates, out)
	}
	if got, err := os.ReadFile(victim); err != nil || !strings.Contains(string(got), "new body") {
		t.Errorf("the unreadable file was not written over: %v %q", err, got)
	}

	// 6. and 7. --dry-run: the same lines for a changed source, a removed
	// source and a mode difference, and nothing written or chmodded.
	if err := os.WriteFile(alpha, []byte("---\nname: alpha\ndescription: changed again\n---\nthird body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(victim, 0o666); err != nil {
		t.Fatal(err)
	}
	gamma := filepath.Join(home, AgyRoot, "skills", "alpha", "references", "notes.md")
	if err := os.Chmod(gamma, 0o666); err != nil {
		t.Fatal(err)
	}
	files = plan()
	out = install(files, true)
	if got := lines(out, "update"); len(got) == 0 || !strings.HasSuffix(got[0], "skills/alpha/SKILL.md") {
		t.Errorf("--dry-run: want `update` lines for the changed skill, got %v:\n%s", got, out)
	}
	if got := lines(out, "mode"); len(got) != 1 || !strings.Contains(got[0], "references/notes.md (0666 -> 0644)") {
		t.Errorf("--dry-run: want one `mode` line for the untouched-but-chmodded file, got %v:\n%s", got, out)
	}
	if got, _ := os.ReadFile(victim); strings.Contains(string(got), "third body") {
		t.Errorf("--dry-run wrote a file")
	}
	if info, _ := os.Stat(gamma); info.Mode().Perm() != 0o666 {
		t.Errorf("--dry-run changed a mode: %o", info.Mode().Perm())
	}

	// 8. A directory at a destination cannot be replaced by the rename: the
	// install errors, and the line is not printed for a change that did not
	// happen (the line prints after the write).
	if err := os.Chmod(victim, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(victim); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err := Install(DirSource(repo), home, files, false, &buf)
	if err == nil {
		t.Fatalf("a directory at a destination did not fail the install:\n%s", buf.String())
	}
	for _, l := range lines(buf.String(), "update") {
		if strings.Contains(l, "skills/alpha/SKILL.md") {
			t.Errorf("a line claimed a write that failed: %q (err %v)", l, err)
		}
	}
}
