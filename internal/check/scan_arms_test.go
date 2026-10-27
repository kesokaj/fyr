package check

import "testing"

// A fence's close counts as a non-blank line: an indented line right after
// it is prose, not an indented code block, so its spans are read.
func TestScanIndentAfterFence(t *testing.T) {
	got := scan("\n```\nx\n```\n    `fyr nope`\n")
	var spans []chunk
	for _, c := range got {
		if !c.fence {
			spans = append(spans, c)
		}
	}
	if len(spans) != 1 || spans[0].line != 5 || spans[0].text != "fyr nope" {
		t.Errorf("spans %+v, want `fyr nope` on line 5", spans)
	}
}
