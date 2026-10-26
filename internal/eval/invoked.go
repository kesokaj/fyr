package eval

import (
	"bytes"
	"io"
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
		if readsNeedle(f, needle) {
			return true
		}
	}
	return false
}

// readsNeedle reports whether the regular file at path holds needle; a link
// or a FIFO the agent left in place of a database is not read.
func readsNeedle(path string, needle []byte) bool {
	f, err := openRegular(path, os.O_RDONLY)
	if err != nil {
		return false
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	return err == nil && bytes.Contains(b, needle)
}
