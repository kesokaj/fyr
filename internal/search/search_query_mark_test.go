package search

import (
	"strings"
	"testing"
)

// TestMarkTargetLongerThanTheSnippet: a target over snippetMax runes, one
// line in any checkout file, panicked on the slice (the v5.5.0 gate).
func TestMarkTargetLongerThanTheSnippet(t *testing.T) {
	for _, n := range []int{snippetMax + 1, snippetMax + 60} {
		target := strings.Repeat("a/", n/2+1)[:n]
		if got := markTarget("see "+target+" here", target); !strings.Contains(got, "»") {
			t.Errorf("n=%d: %q", n, got)
		}
	}
}
