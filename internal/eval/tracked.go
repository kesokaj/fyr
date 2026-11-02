package eval

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/kesokaj/fyr/internal/sandbox"
	"github.com/kesokaj/fyr/internal/trail"
)

// SuitePath is where a checkout keeps its suite, relative to its root: a
// tracked file, so the rows a run reads are the rows a commit holds and
// TestSuiteHarness reads the same file on every machine (ADR-500, amending
// ADR-091, which kept the suite as the GUIDE-eval record).
const SuitePath = ".agents/eval/GUIDE-eval.md"

// ReadTrackedSuite parses root's suite file. found is false when the file
// does not exist, so a caller falls back to the GUIDE-eval record. The file
// must be regular (a link is refused) and at most 1 MiB. With atHead it is
// also refused unless HEAD holds it byte for byte, so a run always names the
// commit its scenarios came from (D3, D8 of the fyr eval plan).
func ReadTrackedSuite(root string, atHead bool) (rows []Row, found bool, err error) {
	path := filepath.Join(root, filepath.FromSlash(SuitePath))
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	f, err := sandbox.OpenRegular(path, os.O_RDONLY)
	if err != nil {
		return nil, true, fmt.Errorf("%s: %v", SuitePath, err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, suiteFileLimit+1))
	if err != nil {
		return nil, true, fmt.Errorf("%s: %v", SuitePath, err)
	}
	if len(b) > suiteFileLimit {
		return nil, true, fmt.Errorf("%s: over %d bytes", SuitePath, suiteFileLimit)
	}
	if atHead {
		head, herr := trail.Command(root, "show", "HEAD:"+SuitePath).Output()
		if herr != nil {
			return nil, true, fmt.Errorf("%s is not committed; commit it first", SuitePath)
		}
		if !bytes.Equal(head, b) {
			return nil, true, fmt.Errorf("%s has uncommitted changes; commit them first", SuitePath)
		}
	}
	rows, err = ParseSuite(string(b))
	if err != nil {
		return nil, true, fmt.Errorf("%s: %v", SuitePath, err)
	}
	return rows, true, nil
}
