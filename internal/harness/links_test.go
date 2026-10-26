package harness

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// deployedSkillFiles returns the planned skill paths, sorted.
func deployedSkillFiles(files []File) []string {
	var rels []string
	for _, f := range files {
		if strings.HasPrefix(f.Rel, filepath.Join(AgyRoot, "skills")+string(filepath.Separator)) {
			rels = append(rels, f.Rel)
		}
	}
	sort.Strings(rels)
	return rels
}

// TestInstallDoesNotFollowLinkedDest: the unchanged branch read, stat'ed
// and chmodded the destination through a symlink, and a changed file's
// rename replaced the link with a regular file (audit of 2026-09-30, IN3).
// A linked destination is kept, never written through, and never enters the
// manifest.
func TestInstallDoesNotFollowLinkedDest(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(DirSource(repo), home, files, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	rels := deployedSkillFiles(files)
	if len(rels) < 2 {
		t.Fatalf("fixture has %d deployed skill files, need 2", len(rels))
	}
	var planned = map[string]File{}
	for _, f := range files {
		planned[f.Rel] = f
	}
	targets := t.TempDir()
	// Link 1: the target holds the planned bytes but a different mode.
	same := filepath.Join(targets, "same")
	os.WriteFile(same, planned[rels[0]].Data, 0o600)
	// Link 2: the target holds other bytes.
	other := filepath.Join(targets, "other")
	os.WriteFile(other, []byte("the operator's own file\n"), 0o600)
	for i, target := range []string{same, other} {
		dest := filepath.Join(home, rels[i])
		os.Remove(dest)
		if err := os.Symlink(target, dest); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := Install(DirSource(repo), home, files, false, &out); err != nil {
		t.Fatalf("install: %v\n%s", err, out.String())
	}
	for i, target := range []string{same, other} {
		dest := filepath.Join(home, rels[i])
		if fi, err := os.Lstat(dest); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s is no longer a symlink (err %v)", rels[i], err)
		}
		if fi, err := os.Stat(target); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("the target of %s changed mode: %v", rels[i], fi.Mode())
		}
	}
	if got, _ := os.ReadFile(other); string(got) != "the operator's own file\n" {
		t.Errorf("the target of %s was written through: %q", rels[1], got)
	}
	if n := strings.Count(out.String(), "(a symlink; fyr never writes through a link)"); n != 2 {
		t.Errorf("want two keep lines for links, got %d:\n%s", n, out.String())
	}
	st, _, _ := LoadState(home)
	for _, rel := range st.Files {
		if rel == rels[0] || rel == rels[1] {
			t.Errorf("a linked destination entered the manifest: %s", rel)
		}
	}
	// Status and Diff report a link as foreign, never as drift or a diff.
	report, err := Status(home, files)
	if err != nil {
		t.Fatal(err)
	}
	if report.Drift() {
		t.Errorf("status reports drift for linked destinations: %+v", report.Counts)
	}
	if report.Counts["foreign"] < 2 {
		t.Errorf("status counts %d foreign, want the two links", report.Counts["foreign"])
	}
	var diff bytes.Buffer
	if err := Diff(home, files, &diff); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(diff.String(), rels[1]) {
		t.Errorf("diff printed the linked destination:\n%s", diff.String())
	}
}

// TestUninstallAndLinkedDirs: an old manifest may name a path that is the
// operator's link now, or a file under a linked directory; neither is
// removed, written or chmodded through (security review of IN3's fix).
func TestUninstallAndLinkedDirs(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(DirSource(repo), home, files, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	rels := deployedSkillFiles(files)
	// The whole skills directory becomes the operator's link to a
	// tree of their own; the manifest still lists every file under it.
	skillsDir := filepath.Join(home, AgyRoot, "skills")
	mine := filepath.Join(t.TempDir(), "mine")
	if err := os.Rename(skillsDir, mine); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(mine, skillsDir); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(mine, strings.TrimPrefix(rels[0], filepath.Join(AgyRoot, "skills")+string(filepath.Separator))))
	// A reinstall with a changed skill writes nothing through the link.
	os.WriteFile(filepath.Join(repo, "skills", "alpha", "SKILL.md"), []byte("---\nname: alpha\ndescription: changed\n---\nnew body\n"), 0o644)
	files, _ = Plan(DirSource(repo), nil)
	var out bytes.Buffer
	if err := Install(DirSource(repo), home, files, false, &out); err != nil {
		t.Fatalf("install: %v\n%s", err, out.String())
	}
	after, _ := os.ReadFile(filepath.Join(mine, "alpha", "SKILL.md"))
	if string(after) != string(before) {
		t.Errorf("install wrote through the linked skills directory")
	}
	if !strings.Contains(out.String(), "keep    ~/"+rels[0]+" (a symlink; fyr never writes through a link)") {
		t.Errorf("no keep line for a file under a linked directory:\n%s", out.String())
	}
	// Uninstall removes nothing under the link and keeps the link.
	out.Reset()
	if err := Uninstall(home, false, &out); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out.String())
	}
	if fi, err := os.Lstat(skillsDir); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("uninstall removed the operator's link (err %v)", err)
	}
	if _, err := os.Stat(filepath.Join(mine, "alpha", "SKILL.md")); err != nil {
		t.Errorf("uninstall removed a file through the link: %v", err)
	}
}

// TestUninstallKeepsMetricsStore: uninstall removes what its manifest lists
// and nothing else; the metrics store is the only copy of what expired
// transcripts held, and stays (R4, ADR-055).
func TestUninstallKeepsMetricsStore(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(DirSource(repo), home, files, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(home, StateDir, "metrics.db")
	if err := os.WriteFile(store, []byte("rows"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Uninstall(home, false, &out); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out.String())
	}
	if b, err := os.ReadFile(store); err != nil || string(b) != "rows" {
		t.Fatalf("uninstall touched the metrics store: %v", err)
	}
}

// TestUninstallSkipsLink: a manifest path that has become the operator's
// link is kept by uninstall (security review round 2 of IN3: the guard at
// the one line that answers it had no test).
func TestUninstallSkipsLink(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(DirSource(repo), home, files, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	rel := deployedSkillFiles(files)[0]
	target := filepath.Join(t.TempDir(), "mine")
	os.WriteFile(target, []byte("mine\n"), 0o600)
	dest := filepath.Join(home, rel)
	os.Remove(dest)
	if err := os.Symlink(target, dest); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Uninstall(home, false, &out); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out.String())
	}
	if fi, err := os.Lstat(dest); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("uninstall removed the operator's link at %s (err %v)", rel, err)
	}
	if !strings.Contains(out.String(), "keep    ~/"+rel) {
		t.Errorf("no keep line:\n%s", out.String())
	}
}

// TestUninstallSkipsFilesUnderLink: an old manifest lists files under a
// directory that has since become the operator's link; uninstall removes
// none of them through it (security review round 3: the underLink half of
// the guard had no test of its own).
func TestUninstallSkipsFilesUnderLink(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(DirSource(repo), home, files, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	skillsDir := filepath.Join(home, AgyRoot, "skills")
	mine := filepath.Join(t.TempDir(), "mine")
	if err := os.Rename(skillsDir, mine); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(mine, skillsDir); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadDir(filepath.Join(mine, "alpha"))
	var out bytes.Buffer
	if err := Uninstall(home, false, &out); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out.String())
	}
	after, _ := os.ReadDir(filepath.Join(mine, "alpha"))
	if len(after) != len(before) || len(before) == 0 {
		t.Errorf("uninstall removed files through the linked directory: %d before, %d after", len(before), len(after))
	}
	if fi, err := os.Lstat(skillsDir); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("uninstall removed the operator's link (err %v)", err)
	}
}

// TestResyncSkipsLink: the resync loop removed a path the old manifest named
// even when it had become a symlink (audit of 2026-09-30, IN3).
func TestResyncSkipsLink(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(DirSource(repo), home, files, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	rel := deployedSkillFiles(files)[0]
	skill := strings.Split(rel, string(filepath.Separator))[2]
	target := filepath.Join(t.TempDir(), "mine")
	os.WriteFile(target, []byte("mine\n"), 0o600)
	dest := filepath.Join(home, rel)
	os.Remove(dest)
	if err := os.Symlink(target, dest); err != nil {
		t.Fatal(err)
	}
	// The skill leaves the repo: the resync loop would remove its old path.
	if err := os.RemoveAll(filepath.Join(repo, "skills", skill)); err != nil {
		t.Fatal(err)
	}
	files, err = Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Install(DirSource(repo), home, files, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(dest); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the resync removed the operator's link at %s (err %v)", rel, err)
	}
}
