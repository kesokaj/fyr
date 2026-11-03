package brain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScratchFileIO(t *testing.T) {
	proj := repo(t)

	// OpenBodyFile: empty, missing, directory, symlink, and valid regular file.
	if _, err := OpenBodyFile(""); !IsUsage(err) {
		t.Fatalf("OpenBodyFile empty: got %v, want usage error", err)
	}
	if _, err := OpenBodyFile(filepath.Join(proj, "missing.md")); err == nil {
		t.Fatal("OpenBodyFile missing: got nil, want error")
	}
	if _, err := OpenBodyFile(proj); err == nil {
		t.Fatal("OpenBodyFile dir: got nil, want error")
	}
	bodyPath := filepath.Join(proj, "body.md")
	if err := os.WriteFile(bodyPath, []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(proj, "link.md")
	if err := os.Symlink(bodyPath, linkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenBodyFile(linkPath); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("OpenBodyFile symlink: got %v", err)
	}
	f, err := OpenBodyFile(bodyPath)
	if err != nil {
		t.Fatalf("OpenBodyFile regular: %v", err)
	}
	f.Close()

	// StdinPiped: idle pipe vs written pipe vs regular file.
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	if StdinPiped(pr) {
		t.Fatal("StdinPiped on idle pipe returned true, want false")
	}
	if _, err := pw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if !StdinPiped(pr) {
		t.Fatal("StdinPiped on non-empty pipe returned false, want true")
	}
	if err := RefusePipedBodyFile(); err == nil {
		t.Fatal("RefusePipedBodyFile returned nil")
	}

	// CheckScratchOut and WriteScratch: path validation, symlink, hardlink, and write.
	if _, _, err := CheckScratchOut(proj, ""); !IsUsage(err) {
		t.Fatalf("CheckScratchOut empty: got %v, want usage error", err)
	}
	for _, bad := range []string{"outside.md", ".agents/scratch/../escape.md", ".agents/scratch/"} {
		if _, _, err := CheckScratchOut(proj, bad); err == nil {
			t.Errorf("CheckScratchOut(%q): got nil, want error", bad)
		}
	}
	relOut := filepath.Join(".agents", "scratch", "sub", "out.md")
	if err := WriteScratch(proj, relOut, "# Plan\n- [ ] step"); err != nil {
		t.Fatalf("WriteScratch: %v", err)
	}
	absOut := filepath.Join(proj, relOut)
	got, err := os.ReadFile(absOut)
	if err != nil || string(got) != "# Plan\n- [ ] step\n" {
		t.Fatalf("ReadFile(%s) = %q, %v", absOut, got, err)
	}
	hardOut := filepath.Join(proj, ".agents", "scratch", "hard.md")
	if err := os.Link(bodyPath, hardOut); err != nil {
		t.Fatal(err)
	}
	if err := WriteScratch(proj, ".agents/scratch/hard.md", "overwrite"); err == nil || !strings.Contains(err.Error(), "hardlink") {
		t.Fatalf("WriteScratch hardlink: got %v, want hardlink refusal", err)
	}
}
