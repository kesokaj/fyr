package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillCatalogInstalledPathsAndZeroPhantom(t *testing.T) {
	if got := LoadSkillCatalog(""); len(got) != 0 {
		t.Fatalf("LoadSkillCatalog(\"\") = %+v, want empty", got)
	}
	emptyHome := t.TempDir()
	if got := LoadSkillCatalog(emptyHome); len(got) != 0 {
		t.Fatalf("LoadSkillCatalog(emptyHome) = %+v, want empty (0 phantom skills)", got)
	}

	home := t.TempDir()
	skillsRoot := filepath.Join(home, ".gemini", "config", "skills")
	repoSkillsDir := filepath.Join("..", "..", "skills")
	entries, err := os.ReadDir(repoSkillsDir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", repoSkillsDir, err)
	}
	installedCount := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		src := filepath.Join(repoSkillsDir, e.Name(), "SKILL.md")
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		dstDir := filepath.Join(skillsRoot, e.Name())
		if err := os.MkdirAll(dstDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dstDir, "SKILL.md"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		installedCount++
	}
	if installedCount < 34 {
		t.Fatalf("expected >= 34 installed skills copied, got %d", installedCount)
	}

	got := LoadSkillCatalog(home)
	if len(got) != installedCount {
		t.Fatalf("LoadSkillCatalog loaded %d skills, want %d", len(got), installedCount)
	}
	byName := make(map[string]string, len(got))
	for _, s := range got {
		wantPath := "~/.gemini/config/skills/" + s.Name + "/SKILL.md"
		if s.Path != wantPath {
			t.Errorf("skill %s Path = %q, want %q (never repo-relative skills/...)", s.Name, s.Path, wantPath)
		}
		if strings.HasPrefix(s.Path, "skills/") {
			t.Errorf("skill %s leaked repo-relative path %q", s.Name, s.Path)
		}
		if s.Description == "" || s.Digest == "" {
			t.Errorf("skill %s missing Description or Digest: %+v", s.Name, s)
		}
		byName[s.Name] = s.Path
	}
	for _, want := range []string{
		"terraform", "operate-gcp", "gcloud-setup", "review-standards",
		"fyr-requirements", "fyr-research", "fyr-init-project", "fyr-auto",
	} {
		if _, ok := byName[want]; !ok {
			t.Errorf("LoadSkillCatalog missing installed skill %q", want)
		}
	}
}
