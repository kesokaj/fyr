package harness

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// happyPath is the first refresh, what it writes, and a second run that changes nothing.
func (r *refreshRig) happyPath() {
	t, tmp, dest, pinA, pinG := r.t, r.tmp, r.dest, r.pinA, r.pinG
	out, code := r.run(pinA, pinG)
	if code != 0 {
		t.Fatalf("happy path: exit %d\n%s", code, out)
	}
	for _, name := range []string{"alpha", "beta", "gamma", "delta", "epsilon"} {
		if _, err := os.Stat(filepath.Join(dest, name, "SKILL.md")); err != nil {
			t.Errorf("entry %s not vendored: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "not-asked-for")); err == nil {
		t.Error("an entry outside the four source sets was vendored")
	}
	index, _ := os.ReadFile(filepath.Join(dest, "INDEX.md"))
	for _, want := range []string{
		"- `gamma` — a block scalar description folded over two lines\n",
		"- `beta` — a quoted description: with a colon\n",
		"- `delta` — delta with a metadata map\n",
	} {
		if !strings.Contains(string(index), want) {
			t.Errorf("INDEX.md lacks %q:\n%s", want, index)
		}
	}
	if n := strings.Count(string(index), "\n- `"); n != 5 {
		t.Errorf("INDEX.md has %d entry lines, want 5", n)
	}
	sums, _ := os.ReadFile(filepath.Join(dest, "CHECKSUMS"))
	for _, want := range []string{"  gamma/scripts/run.sh\n", "  LICENSE-agents-cli\n", "  LICENSE-google-skills\n"} {
		if !strings.Contains(string(sums), want) {
			t.Errorf("CHECKSUMS lacks %q", want)
		}
	}
	if strings.Contains(string(sums), "INDEX.md") || strings.Contains(string(sums), "MANIFEST.md") {
		t.Error("CHECKSUMS covers a harness-written file")
	}
	manifest, _ := os.ReadFile(filepath.Join(dest, "MANIFEST.md"))
	for _, want := range []string{pinA[:7], pinG[:7], "## Excluded", "## Notes", "https://github.com/google/agents-cli", "https://github.com/google/skills", "Fetched through an override"} {
		if !strings.Contains(string(manifest), want) {
			t.Errorf("MANIFEST.md lacks %q", want)
		}
	}
	// The manifest names the upstream repositories, never the value of an
	// override: that value may hold a token in its path, and the manifest is
	// committed (the v2.2.0 gate's L4).
	if strings.Contains(string(manifest), tmp) {
		t.Errorf("MANIFEST.md records an override's value:\n%s", manifest)
	}
	if info, err := os.Stat(filepath.Join(dest, "gamma", "scripts", "run.sh")); err != nil || info.Mode()&0o111 != 0 {
		t.Errorf("the executable bit was not cleared: %v %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".fyr-noindex")); err != nil {
		t.Errorf("the search marker was not written: %v", err)
	}
	if !strings.Contains(out, "next: git add") {
		t.Errorf("the script does not say what to run next:\n%s", out)
	}
	// With an override the script says where the value stays on disk: git
	// keeps it in the work clones' config and logs (security review of
	// v2.2.0..1fc0d4c).
	if !strings.Contains(out, "delete that directory if the value is sensitive") {
		t.Errorf("the script does not say an override's value stays in the work directory:\n%s", out)
	}
	good := r.snapshot()
	r.good = good
	if out, code := r.run(pinA, pinG); code != 0 || r.snapshot() != good {
		t.Fatalf("a second run at the same pins changed the catalog (exit %d)\n%s", code, out)
	}
}

// relativeDestWork resolves relative --dest and --work against the caller.
func (r *refreshRig) relativeDestWork() {
	t, script, tmp, agents, gskills, pinA, pinG := r.t, r.script, r.tmp, r.agents, r.gskills, r.pinA, r.pinG
	// Relative --dest and --work resolve against the caller's directory, not
	// against the directory the nested go test runs in (BUG-146).
	relCmd := exec.Command("bash", script, "--dest", "rel-dest", "--work", "rel-work", pinA, pinG)
	relCmd.Dir = tmp
	relCmd.Env = fixtureEnv("CATALOG_AGENTS_CLI_REPO="+agents, "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=")
	if out, err := relCmd.CombinedOutput(); err != nil {
		t.Errorf("relative --dest and --work: %v\n%s", err, out)
	} else if _, err := os.Stat(filepath.Join(tmp, "rel-dest", "gamma", "SKILL.md")); err != nil {
		t.Errorf("relative --dest: the catalog did not land under the caller's directory: %v", err)
	}
}

// missingEntry restores an entry whose directory is gone.
func (r *refreshRig) missingEntry() {
	t, dest, pinA, pinG, good := r.t, r.dest, r.pinA, r.pinG, r.good
	// An entry the old CHECKSUMS names but whose directory is already gone
	// (a half-done refresh, a hand deletion) must not stop the swap half way:
	// the loop's last test once ended the pipeline with status 1, silently,
	// after the other entries were deleted (code review 2026-10-01).
	if err := os.RemoveAll(filepath.Join(dest, "gamma")); err != nil {
		t.Fatal(err)
	}
	if out, code := r.run(pinA, pinG); code != 0 || r.snapshot() != good {
		t.Fatalf("a refresh over a catalog with a missing entry directory did not restore it (exit %d)\n%s", code, out)
	}
}

// bystander leaves a directory the old CHECKSUMS does not name.
func (r *refreshRig) bystander() {
	t, dest, pinA, pinG, good := r.t, r.dest, r.pinA, r.pinG, r.good
	// The swap removes what the old CHECKSUMS names and nothing else: a
	// directory someone put beside the entries survives a refresh.
	r.put(dest, map[string]string{"bystander/keep.txt": "kept\n"})
	if out, code := r.run(pinA, pinG); code != 0 {
		t.Fatalf("a refresh over a catalog with a bystander directory failed (exit %d)\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dest, "bystander", "keep.txt")); err != nil {
		t.Errorf("the swap removed a directory the old CHECKSUMS does not name: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(dest, "bystander")); err != nil {
		t.Fatal(err)
	}
	if r.snapshot() != good {
		t.Fatal("the catalog is not the good one after the bystander fixture")
	}
}

// bareChecksums refreshes over a CHECKSUMS that names no entry.
func (r *refreshRig) bareChecksums() {
	t, repo, script, tmp, agents, gskills, work, pinA, pinG := r.t, r.repo, r.script, r.tmp, r.agents, r.gskills, r.work, r.pinA, r.pinG
	// The same loop's second way out: a CHECKSUMS that names no entry at all
	// (an empty one) once made `grep /` end the pipeline the same way.
	bare := filepath.Join(tmp, "bare-dest")
	r.put(bare, map[string]string{"CHECKSUMS": ""})
	bareCmd := exec.Command("bash", script, "--dest", bare, "--work", work, pinA, pinG)
	bareCmd.Dir = repo
	bareCmd.Env = fixtureEnv("CATALOG_AGENTS_CLI_REPO="+agents, "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=")
	if out, err := bareCmd.CombinedOutput(); err != nil {
		t.Errorf("a refresh over a destination whose CHECKSUMS names no entry failed: %v\n%s", err, out)
	} else if _, err := os.Stat(filepath.Join(bare, "gamma", "SKILL.md")); err != nil {
		t.Errorf("a refresh over a destination whose CHECKSUMS names no entry vendored nothing: %v", err)
	}
}

// manifestExclusion keeps out an entry the manifest excludes.
func (r *refreshRig) manifestExclusion() {
	t, dest, pinA, pinG, good := r.t, r.dest, r.pinA, r.pinG, r.good
	// An entry named under the manifest's ## Excluded stays out without the flag (S5).
	mf := filepath.Join(dest, "MANIFEST.md")
	orig, _ := os.ReadFile(mf)
	if err := os.WriteFile(mf, bytes.Replace(orig, []byte("## Excluded\n"), []byte("## Excluded\n\n- `epsilon` — excluded by the fixture\n"), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := r.run(pinA, pinG); code != 0 {
		t.Errorf("a refresh with a manifest exclusion failed (exit %d)\n%s", code, out)
	} else if _, err := os.Stat(filepath.Join(dest, "epsilon")); err == nil {
		t.Error("an entry listed under ## Excluded was vendored")
	}
	os.WriteFile(mf, orig, 0o644)
	if out, code := r.run(pinA, pinG); code != 0 || r.snapshot() != good {
		t.Fatalf("could not restore the good catalog after the exclusion fixture (exit %d)\n%s", code, out)
	}
}
