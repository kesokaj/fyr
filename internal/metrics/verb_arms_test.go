package metrics

import (
	"reflect"
	"testing"
)

// The arms of segments and heredocDelims that their splits move into
// helpers, pinned before the split (PLAN-2026-10-27-funclen Phase 3, D13).

// An escaped quote inside double quotes does not close them; ( and a brace
// outside a word each end the segment before them.
func TestVerbTokenizerArms(t *testing.T) {
	for _, c := range []struct {
		cmd  string
		want []string
	}{
		{`echo "a\"b; c"; ls`, []string{"echo", "ls"}},
		{"echo x(git status)", []string{"echo", "git status"}},
		{"echo x { git status; }", []string{"echo", "git status"}},
		// Pinned after the Phase 3 review (its finding 1): each case fails
		// under one of the tokenizer mutants that survived the tests above.
		{"echo $(ls) x) git status", []string{"echo", "ls", "git status"}}, // $( counts one subshell, and a ) past it ends a pattern
		{"(ls) x) git status", []string{"ls", "git status"}},               // ) closes the subshell ( opened
		{"git \\\nstatus", []string{"git status"}},                         // a backslash-newline joins the lines
		{"ls;; git status", []string{"ls", "git status"}},                  // ;; outside a case opens no pattern
		{"echo a#b; ls", []string{"echo", "ls"}},                           // # inside a word is no comment
		{`echo 'a\'; ls`, []string{"echo", "ls"}},                          // a backslash escapes nothing in single quotes
	} {
		if got := Verb(c.cmd); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Verb(%q) = %q, want %q", c.cmd, got, c.want)
		}
	}
}

// A here-string opens no here-document; an unclosed or empty quoted
// delimiter, and a << with no word after it, leave the rest unparsed.
func TestHeredocDelimsArms(t *testing.T) {
	for _, c := range []struct {
		line     string
		unparsed bool
	}{
		{"cat <<< word", false},
		{"cat <<'EOF", true},
		{"cat <<''", true},
		{"cat << ;", true},
	} {
		ds, unparsed := heredocDelims(c.line)
		if ds != nil || unparsed != c.unparsed {
			t.Errorf("heredocDelims(%q) = %v, %v; want none, %v", c.line, ds, unparsed, c.unparsed)
		}
	}
}
