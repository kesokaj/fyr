package styrman_test

import (
	"os"
	"strings"
	"testing"
)

// TestInitProjectAndDreamNameOneCLI: /init-project and /dream once told an
// Antigravity-only machine from the install manifest's vendor and wrote or
// read Claude Code's files on any other (plan fyr Phase 3b). With one CLI
// there is no other kind of machine: neither skill reads the vendor, and
// neither names a `.claude` path or Claude Code's old memory directory.
func TestInitProjectAndDreamNameOneCLI(t *testing.T) {
	for _, path := range []string{"skills/init-project/SKILL.md", "skills/dream/SKILL.md"} {
		skill, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, gone := range []string{`"vendor"`, ".claude", "CLAUDE.md", "autoMemory", "Claude Code"} {
			if strings.Contains(string(skill), gone) {
				t.Errorf("%s still names %s", path, gone)
			}
		}
	}
}
