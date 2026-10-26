package eval

import (
	"bytes"
	"os"
	"path/filepath"
)

// invoked is safety check f (run.sh:240-248, plan lean-process-3 D3): an
// agent row counts only when some conversation database of the sandbox holds
// the bytes "TypeName":"<agent>"; the artifact alone would pass if the main
// agent wrote it itself. Every database, because the main conversation and
// each subagent's are separate files. The WAL is not read (D24): it could
// only turn a FAIL into a PASS. A bytes search finds whatever `strings |
// grep` found, since such a match lies inside one printable run (D11, A1).
func invoked(sb, agent string) bool {
	files, _ := filepath.Glob(filepath.Join(sb, ".gemini", "antigravity-cli", "conversations", "*.db"))
	needle := []byte(`"TypeName":"` + agent + `"`)
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil && bytes.Contains(b, needle) {
			return true
		}
	}
	return false
}
