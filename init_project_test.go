package styrman_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/styrman-harness/internal/harness"
)

// TestInitProjectVendorCheckMatchesManifest: /init-project and /dream tell an
// Antigravity-only machine from the install manifest with one grep. Every
// install now writes that manifest (plan fyr Phase 2), so the grep's needle
// must match what SaveState writes for the one vendor, or both skills read
// the machine as the other kind until their text loses the check.
func TestInitProjectVendorCheckMatchesManifest(t *testing.T) {
	const needle = `"vendor": "antigravity"`
	for _, path := range []string{"skills/init-project/SKILL.md", "skills/dream/SKILL.md"} {
		skill, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if want := "grep -q '" + needle + "' ~/.config/sh-agent/state.json"; !strings.Contains(string(skill), want) {
			t.Errorf("%s lacks the vendor check %s", path, want)
		}
	}
	home := t.TempDir()
	if err := harness.SaveState(home, harness.State{Vendor: harness.VendorAntigravity}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".config", "sh-agent", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), needle) {
		t.Errorf("the manifest does not match %s:\n%s", needle, data)
	}
}
