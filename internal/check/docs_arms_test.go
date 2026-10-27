package check

import (
	"strings"
	"testing"
)

// The arms of verbFindings that its split moves into helpers, pinned before
// the split (PLAN-2026-10-27-funclen Phase 9, D13).

// A --home=… or --repo=… word before the verb is skipped, and a flag before
// a subverb does not hide the subverb.
func TestVerbFindingsFlagForms(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{"fyr --home=/tmp/h nope", "`fyr nope`: fyr has no verb nope"},
		{"fyr --repo=. --home=x nope", "`fyr nope`: fyr has no verb nope"},
		{"fyr record --global frob", "`fyr record frob`: record has no subverb frob"},
	} {
		fs := verbFindings("f.md", chunk{line: 1, text: c.text}, vocab)
		var got []string
		for _, f := range fs {
			got = append(got, f.Text)
		}
		if strings.Join(got, "; ") != c.want {
			t.Errorf("%q: %q, want %q", c.text, got, c.want)
		}
	}
}
