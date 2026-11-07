package auto

import (
	"strings"
	"testing"
)

// TestWithRunnerKeepsVerdict pins the order withRunner writes (BUG-506): a
// runner's reason longer than the whole line is what the cut at lineLimit
// takes, and the verdict and the branch before it survive.
func TestWithRunnerKeepsVerdict(t *testing.T) {
	verdict := "FAIL (no commits); branch work/x kept"
	got := short(withRunner(verdict, "host ("+strings.Repeat("r", lineLimit)+")"))
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("precondition: the line reached the %d-rune cut:\n%s", lineLimit, got)
	}
	if want := verdict + "; runner host (r"; !strings.HasPrefix(got, want) {
		t.Fatalf("the cut line lost %q:\n%s", want, got)
	}
}
