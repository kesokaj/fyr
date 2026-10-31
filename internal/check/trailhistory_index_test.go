package check

import (
	"bytes"
	"math/rand"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
)

// index finds what `git log -G '"id":"<ID>".*"updated":"<stamp>"'` matched,
// line by line: the oracle is that regular expression, over hand-written
// edge lines and lines drawn from the same pieces.
func TestIndexMatchesTheRegex(t *testing.T) {
	specs := []brain.Record{{ID: "SPEC-a", Updated: "2026-10-25T10:00:00Z"}, {ID: "SPEC-b", Updated: "2026-10-25T12:00:00+02:00"}}
	k := &checker{}
	h := k.trailHistory(specs)
	oracle := func(data string) map[string]bool {
		held := map[string]bool{}
		for _, s := range specs {
			re := regexp.MustCompile(regexp.QuoteMeta(`"id":"`+s.ID+`"`) + `.*` + regexp.QuoteMeta(`"updated":"`+s.Updated+`"`))
			for _, l := range strings.Split(data, "\n") {
				if re.MatchString(l) {
					held[s.ID] = true
				}
			}
		}
		return held
	}
	cases := []string{
		`{"id":"SPEC-a","updated":"2026-10-25T10:00:00Z"}`,
		`{"id":"SPEC-a""updated":"2026-10-25T10:00:00Z"}`,          // adjacent: .* is empty
		`{"id":"SPEC-a"updated":"2026-10-25T10:00:00Z"}`,           // one quote cannot close one and open the other
		`{"updated":"2026-10-25T10:00:00Z","id":"SPEC-a"}`,         // the stamp before the id
		`{"id":"zz"id":"SPEC-a","updated":"2026-10-25T10:00:00Z"}`, // a key opening at the last value's quote
		`{"id":"SPEC-a","updated":"2026-10-25T10:00:00Z` + "\n" + `"}`,
		`{"id":"SPEC-a","updated":"2026-10-25T12:00:00+02:00"}`, // the other spec's stamp
		`{"id":"SPEC-b","body":"x","updated":"2026-10-25T12:00:00+02:00"}`,
		`{"id":"SPEC-ab","updated":"2026-10-25T10:00:00Z"}`,
		`{"id":"SPEC-a`,
	}
	pieces := []string{`"id":"`, `SPEC-a`, `SPEC-b`, `"`, `"updated":"`, `2026-10-25T10:00:00Z`, `2026-10-25T12:00:00+02:00`, `,`, "\n", `x`}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 5000; i++ {
		var b strings.Builder
		for j := rng.Intn(14); j >= 0; j-- {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		cases = append(cases, b.String())
	}
	for _, c := range cases {
		got, want := h.index([]byte(c)), oracle(c)
		if len(got) != len(want) || got["SPEC-a"] != want["SPEC-a"] || got["SPEC-b"] != want["SPEC-b"] {
			t.Fatalf("index(%q) = %v, the regex %v", c, got, want)
		}
	}
}

// A crafted line of nothing but one spec's "id" is read in one pass: 8 MiB
// in well under the time a scan to the line's end per key would take, which
// was 49 s at 1 MiB (P2's second review).
func TestIndexIsLinearOnALongLine(t *testing.T) {
	h := (&checker{}).trailHistory([]brain.Record{{ID: "SPEC-a", Updated: "2026-10-25T10:00:00Z"}})
	line := bytes.Repeat([]byte(`"id":"SPEC-a"`), 8<<20/13)
	start := time.Now()
	if held := h.index(line); held["SPEC-a"] {
		t.Fatal("a line with no stamp holds the spec")
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("index took %v over 8 MiB", d)
	}
}

// Rule (b)'s count reads the history without replace refs too: a replace
// ref that swaps HEAD for the spec's own commit would count nothing since it
// (P2's second security review, finding 2).
func TestSpecRuleCountIgnoresReplaceRefs(t *testing.T) {
	dir := staleAfterOneCode(t)
	git(t, dir, "replace", "HEAD", "HEAD~1")
	added := git(t, dir, "--no-replace-objects", "rev-parse", "HEAD~1")
	if got := git(t, dir, "rev-list", "--count", added+"..HEAD", "--", "internal/a.go"); got != "0" {
		t.Fatalf("the replace ref does not hide the code commit, so it proves nothing: %s", got)
	}
	same(t, run(t, dir).Findings, "SPEC-a:0 spec-stale")
}
