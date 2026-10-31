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
// counted, and an uncommitted spec edit is not stale. A git call that fails
// leaves a note, never a silent pass (code review 2026-10-25, finding 2).
func (k *checker) specsRule(specs []brain.Record) (out []Finding, notes []string) {
	h := k.trailHistory(specs)
	for _, r := range specs {
		code := k.specCode(r)
		if len(code) == 0 {
			out = append(out, Finding{r.ID, 0, "spec-unlinked", "names no code: no existing path but *.md files and .agents/trail.jsonl"})
			continue
		}
		commit, err := h.commitOf(r)
		if err != nil {
			notes = append(notes, r.ID+": trail history unreadable: spec not checked")
			continue
		}
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
		data, err := h.git(args...).Output()
		if err != nil {
			notes = append(notes, r.ID+": git rev-list failed: spec not checked")
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
	return out, notes
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
