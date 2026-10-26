package check

import (
	"strings"
	"testing"
)

// A bare Go file name in a current spec's Verification section must be a
// tracked file's base name; a :N on it is not checked; the same name outside
// the section, in a fence, or in a retired spec, is not read, and a `## `
// line inside a fence does not end the section.
func TestVerificationFileNames(t *testing.T) {
	body := strings.Join([]string{
		"Code: `internal/a/a.go`.",
		"",
		"`outside_test.go` is an example.",
		"",
		"## Verification",
		"",
		"| Area | Test |",
		"|---|---|",
		"| a | `a_test.go` (`-run TestA`) |",
		"| b | `tools_test.go`, `a.go:300` |",
		"| c | `internal/a/a_test.go` |",
		"```",
		"go test ./... # fenced_test.go",
		"## a shell comment, not a heading",
		"```",
		"| d | `after_fence_test.go` |",
		"",
		"## After",
		"",
		"`gone_test.go`",
	}, "\n")
	trail := trailLine(t, "SPEC-a", "spec", "current", body) +
		trailLine(t, "SPEC-old", "spec", "retired", "## Verification\n\n`never_test.go`")
	dir := repo(t, map[string]string{
		".agents/trail.jsonl":  trail,
		"internal/a/a.go":      "package a\n",
		"internal/a/a_test.go": "package a\n",
	})
	rep := run(t, dir)
	same(t, rep.Findings, "SPEC-a:10 path", "SPEC-a:16 path")
	if len(rep.Findings) > 0 && !strings.Contains(rep.Findings[0].Text, "no tracked file is named tools_test.go") {
		t.Errorf("text %q", rep.Findings[0].Text)
	}
}
