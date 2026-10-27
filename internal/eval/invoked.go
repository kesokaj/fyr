package eval

import (
	"bytes"
	"fmt"
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
	ok, _ := invokedNotes(sb, agent)
	return ok
}

// invokedNotes is invoked plus a note for each database it did not read
// because it is over the scan cap (maxScan): such a file is neither a pass
// nor a silent miss (IN51 of the 2026-10-27 audit, plan D16).
func invokedNotes(sb, agent string) (bool, []string) {
	files, _ := filepath.Glob(filepath.Join(sb, ".gemini", "antigravity-cli", "conversations", "*.db"))
	needle := []byte(`"TypeName":"` + agent + `"`)
	var notes []string
	for _, f := range files {
		found, big := readsNeedle(f, needle)
		if big {
			notes = append(notes, fmt.Sprintf("%s: over %d MiB, not read", f, maxScan>>20))
		}
		if found {
			return true, notes
		}
	}
	return false, notes
}

// readsNeedle reports whether the regular file at path holds needle, and
// whether it was left unread for being over maxScan; a link or a FIFO the
// agent left in place of a database is not read.
func readsNeedle(path string, needle []byte) (found, big bool) {
	f, err := openRegular(path, os.O_RDONLY)
	if err != nil {
		return false, false
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || st.Size() > maxScan {
		return false, err == nil
	}
	b, err := io.ReadAll(io.LimitReader(f, maxScan))
	return err == nil && bytes.Contains(b, needle), false
}
