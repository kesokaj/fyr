package check

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/trail"
)

// historyNote is why rule (b) did not run (plan D13): its count is only
// true over the whole history.
func (k *checker) historyNote() string {
	out, _ := k.git("rev-parse", "--is-shallow-repository").Output()
	switch {
	case strings.TrimSpace(string(out)) == "true":
		return "a shallow clone: specs not checked"
	case trail.PartialClone(k.top) || !trail.Readable(k.top):
		return "a partial clone: specs not checked"
	}
	return ""
}

// specsRule is rule (b) over the current spec records (plan D9, D10): a spec
// whose code was committed to after the commit that carries its current
// trail line is stale; one naming no code is unlinked. Ancestry, not
// clocks: a commit carrying both the code and the spec's update is not
// counted, and an uncommitted spec edit is not stale.
func (k *checker) specsRule(specs []brain.Record) []Finding {
	var out []Finding
	for _, r := range specs {
		code := k.specCode(r)
		if len(code) == 0 {
			out = append(out, Finding{r.ID, 0, "spec-unlinked", "names no code: no existing path but *.md files and .agents/trail.jsonl"})
			continue
		}
		commit := k.specCommit(r)
		if commit == "" {
			continue
		}
		args := []string{"rev-list", "--count", commit + "..HEAD", "--"}
		for _, p := range code {
			args = append(args, ":(literal)"+p)
		}
		// specCode already dropped .agents and .agents/trail.jsonl, so only a
		// *.md file below a code directory needs excluding.
		args = append(args, ":(glob,exclude)**/*.md")
		data, err := k.git(args...).Output()
		if err != nil {
			continue
		}
		if n, _ := strconv.Atoi(strings.TrimSpace(string(data))); n > 0 {
			noun := "commits"
			if n == 1 {
				noun = "commit"
			}
			out = append(out, Finding{r.ID, 0, "spec-stale", fmt.Sprintf("%d %s to %s since %.12s", n, noun, strings.Join(code, " "), commit)})
		}
	}
	return out
}

// specCode is a spec's code (D10): the path candidates its spans name that
// exist and git does not ignore, minus *.md files, .agents and
// .agents/trail.jsonl, each once, in the order named.
func (k *checker) specCode(r brain.Record) []string {
	var ps []string
	seen := map[string]bool{}
	for _, c := range scan(r.Body) {
		if c.fence {
			continue
		}
		cand, ok := k.pathCandidate(r.ID, c)
		if !ok || seen[cand.p] || strings.HasSuffix(cand.p, ".md") || cand.p == ".agents" || cand.p == ".agents/trail.jsonl" {
			continue
		}
		seen[cand.p] = true
		if _, err := k.root.Stat(filepath.FromSlash(cand.p)); err == nil {
			ps = append(ps, cand.p)
		}
	}
	ign := k.ignored(ps)
	var code []string
	for _, p := range ps {
		if !ign[p] {
			code = append(code, p)
		}
	}
	return code
}

// specCommit is the oldest commit whose trail.jsonl diff adds r's current
// line, matched on its id and its updated stamp, which the export writes in
// that order; "" when no commit carries it yet.
func (k *checker) specCommit(r brain.Record) string {
	pat := `"id":"` + breQuote(r.ID) + `".*"updated":"` + breQuote(r.Updated) + `"`
	out, err := trail.Log(k.top, "-G", pat, "--format=%H", "--", ".agents/trail.jsonl")
	if err != nil {
		return ""
	}
	hs := strings.Fields(string(out))
	if len(hs) == 0 {
		return ""
	}
	return hs[len(hs)-1]
}

// breQuote escapes the characters a POSIX basic regular expression, git's
// -G default, treats as special.
func breQuote(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`\.[]*^$`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
