package check

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
)

// bareGoFile is a Go file name with no directory: the shape a spec's
// Verification table names its tests in (`agy_test.go`).
var bareGoFile = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*\.go$`)

// verificationFindings is rule (a)'s bare-name check: inside a current
// spec's `## Verification` section, an inline span that is one bare Go file
// name, which may end :N or :N-M, must be the base name of a tracked file
// (spec SPEC-fyr-check § Verification file names). The path rule cannot see
// such a name, since it is anchored on no top-level entry, and a spec named
// a file that was never written (tools_test.go, 2026-10-26). Outside that
// section a bare name is often an example (`foo.go`) and is not read; the
// :N is not checked, since the name may match several files.
func (k *checker) verificationFindings(specs []brain.Record) []Finding {
	names := map[string]bool{}
	for _, p := range k.tracked {
		names[path.Base(p)] = true
	}
	var out []Finding
	for _, r := range specs {
		from, to := verificationLines(r.Body)
		for _, c := range scan(r.Body) {
			if c.fence || c.line < from || c.line >= to {
				continue
			}
			p, _, _, _ := splitLine(c.text)
			if bareGoFile.MatchString(p) && !names[p] {
				out = append(out, Finding{r.ID, c.line, "path", fmt.Sprintf("`%s`: no tracked file is named %s", c.text, p)})
			}
		}
	}
	return out
}

// verificationLines is the 1-based line range [from, to) of a body's
// `## Verification` section, up to the next level-two heading; 0, 0 when
// the body has none. A heading inside a fence is not one: the fence rule is
// scan's (same character, a run at least as long, nothing after it).
func verificationLines(body string) (from, to int) {
	to = 1 << 30
	unfenced(body, func(n int, l string) bool {
		switch {
		case from == 0 && strings.HasPrefix(l, "## Verification"):
			from = n + 1
		case from > 0 && strings.HasPrefix(l, "## "):
			to = n
			return false
		}
		return true
	})
	if from == 0 {
		return 0, 0
	}
	return from, to
}
