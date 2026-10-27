package harness

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A directory where the install would write a file fails the real run, and
// the dry run says so too, non-zero, instead of printing an update and
// exiting 0 (IN41 of the 2026-10-27 audit; review-standards § 8). Both
// refusals name the cause and what to do, never a bare rename error
// (BUG-189, the v3.1.0 gate's § 3c matrix).
func TestDryRunRefusesADirectoryAtAFilePath(t *testing.T) {
	home := t.TempDir()
	rel := filepath.Join(".gemini", "config", "skills", "x", "SKILL.md")
	if err := os.MkdirAll(filepath.Join(home, rel), 0o755); err != nil {
		t.Fatal(err)
	}
	files := []File{{Rel: rel, Data: []byte("---\nname: x\n---\n"), Mode: 0o644}}
	for _, dry := range []bool{true, false} {
		var out bytes.Buffer
		err := Install(Source{}, home, files, dry, &out)
		if err == nil || !strings.Contains(err.Error(), "writing") {
			t.Errorf("dry run %v: err %v, want the write refused\n%s", dry, err, out.String())
		}
		for _, want := range []string{"a directory is in the way", "move or remove it", "fyr install"} {
			if err != nil && !strings.Contains(err.Error(), want) {
				t.Errorf("dry run %v: err %q does not say %q", dry, err, want)
			}
		}
		if strings.Contains(out.String(), "update  ~/"+rel) {
			t.Errorf("dry run %v printed an update for a path it cannot write:\n%s", dry, out.String())
		}
	}
}
