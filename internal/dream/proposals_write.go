package dream

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/search"
)

type plannedDraft struct {
	dst  string
	body []byte
}

type draftPlan struct {
	items []plannedDraft
	lines []string
}

// write stores validated proposals, ratings, and draft skills, returning the
// count of non-draft-wrapper proposals written.
func (o Options) write(conf string, c Collected, r response, cli string, t0 time.Time) (int, error) {
	nProps, _, err := o.writeCounts(conf, c, r, cli, t0)
	return nProps, err
}

// writeCounts stores validated proposals, ratings, and draft skills (in order:
// planDrafts -> propose -> WriteRatings -> commitDrafts) so drafts and ratings
// only persist after propose() succeeds. Returns non-draft-wrapper proposals
// and validated session ratings written.
func (o Options) writeCounts(conf string, c Collected, r response, cli string, t0 time.Time) (int, int, error) {
	day := t0.Format("2006-01-02")
	plan, err := o.planDrafts(filepath.Join(conf, "proposals", "dream"), c.Name, day, r.Skills)
	if err != nil {
		return 0, 0, err
	}
	ratings := o.validRatings(c, r.Ratings, t0)
	head := o.header(c, cli, day)
	ps := o.buildProposals(head, r.Proposals, ratings)
	wrapperTitle := "Dream, harness — draft skills for " + c.Name
	hasWrapper := len(plan.lines) > 0
	if hasWrapper {
		ps = append(ps, brain.Proposal{Title: wrapperTitle, Body: head + strings.Join(plan.lines, "\n") + "\n"})
	}
	proj := proposalProject(o, c.Root)
	ps, err = dedupeProposals(o.Home, proj, ps)
	if err != nil {
		return 0, 0, err
	}
	nProps := len(ps)
	if hasWrapper && nProps > 0 && ps[nProps-1].Title == wrapperTitle {
		nProps--
	}
	if len(ps) > 0 {
		if _, err := propose(o.Home, proj, ps, t0); err != nil {
			return 0, 0, err
		}
	}
	if err := o.persistRatings(c.Root, ratings); err != nil {
		return 0, 0, err
	}
	if err := commitDrafts(plan); err != nil {
		return 0, 0, err
	}
	return nProps, len(ratings), nil
}

func proposalProject(o Options, root string) string {
	if root == search.GlobalWorkspace {
		if o.HarnessRepo != "" {
			return o.HarnessRepo
		}
		return o.Home
	}
	return root
}

func (o Options) planDrafts(dir, name, day string, skills []draft) (draftPlan, error) {
	var plan draftPlan
	reserved := map[string]bool{}
	safeName := safeFileName(name)
	for _, d := range skills {
		if !skillName.MatchString(d.Name) {
			plan.lines = append(plan.lines, fmt.Sprintf("Draft skill %q dropped: not a kebab-case name", o.line(d.Name)))
			continue
		}
		dst, err := uniqueFree(filepath.Join(dir, "drafts"), day+"-"+safeName+"-"+d.Name, "", reserved)
		if err != nil {
			return draftPlan{}, err
		}
		reserved[dst] = true
		desc, _ := json.Marshal(o.clean(d.Description))
		body := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", d.Name, desc, strings.TrimSpace(o.clean(d.Body)))
		plan.items = append(plan.items, plannedDraft{dst: dst, body: []byte(body)})
		plan.lines = append(plan.lines, "Draft skill: "+filepath.Join(dir, "drafts", filepath.Base(dst), "SKILL.md"))
	}
	return plan, nil
}

func commitDrafts(plan draftPlan) error {
	for _, it := range plan.items {
		if err := os.MkdirAll(it.dst, 0o700); err != nil {
			return err
		}
		if err := harness.WriteAtomic(filepath.Join(it.dst, "SKILL.md"), it.body, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (o Options) validProposal(p proposal) (proposal, bool) {
	kind := strings.ToLower(o.line(p.Kind))
	switch kind {
	case "rule", "skill", "procedure", "memory":
	default:
		return proposal{}, false
	}
	title := o.line(p.Title)
	text := strings.TrimSpace(o.clean(p.Text))
	if title == "" || text == "" {
		return proposal{}, false
	}
	target := "project"
	if strings.ToLower(o.line(p.Target)) == "harness" {
		target = "harness"
	}
	sigID := cleanSignalID(o.line(p.SignalID))
	return proposal{Title: title, Kind: kind, Target: target, File: p.File, Text: text, Evidence: p.Evidence, SignalID: sigID}, true
}

func cleanSignalID(raw string) string {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "sig:") || len(s) < 5 || len(s) > 96 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == ':' || c == '_' || c == '-') {
			return ""
		}
	}
	return s
}

func (o Options) buildProposals(head string, props []proposal, ratings []metrics.SessionRating) []brain.Proposal {
	var ps []brain.Proposal
	for _, raw := range props {
		p, ok := o.validProposal(raw)
		if !ok {
			continue
		}
		// The target leads the title, so /retro tells a project's proposals
		// from the harness's without opening each (security review of the
		// dream over records, finding 3).
		ps = append(ps, brain.Proposal{Title: "Dream, " + p.Target + " — " + p.Title, Body: head + o.section(p)})
	}
	sessionsByTarget := targetSessionCounts(ratings)
	for _, rt := range ratings {
		if needsSkillProposal(rt) {
			target := resolvedSkillTarget(rt.TargetSkill)
			ps = append(ps, o.ratingProposal(head, rt, sessionsByTarget[target]))
		}
	}
	return ps
}

// propose is brain.ProposeAll; a test replaces it.
var propose = brain.ProposeAll

const (
	leftOutPrefix          = "Left out: "
	maxDroppedHeaderBytes  = 1024
	maxDroppedSummaryBytes = maxDroppedHeaderBytes - len(leftOutPrefix)
	maxDroppedSampleBytes  = 100
	maxDroppedKindsBytes   = 360
	maxDroppedKindBytes    = 32
)

// header is what every proposal of one project's dream carries: where it came
// from and what the model read.
func (o Options) header(c Collected, cli, day string) string {
	counts := map[string]int{}
	for _, in := range c.Inputs {
		counts[in.Kind]++
	}
	var inputs []string
	for _, k := range promptOrder {
		inputs = append(inputs, fmt.Sprintf("%s %d", k, counts[k]))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Dream of %s, %s, by %s with no tool granted, from redacted inputs: %s.\n", o.line(c.Root), day, cli, strings.Join(inputs, ", "))
	if len(c.Dropped) > 0 {
		fmt.Fprintf(&b, "%s%s\n", leftOutPrefix, formatDroppedHeader(cleanDropped(c.Dropped, o.line)))
	}
	b.WriteString("\n")
	return b.String()
}

func cleanDropped(dropped []string, clean func(string) string) []string {
	out := make([]string, len(dropped))
	for i, d := range dropped {
		if clean != nil {
			out[i] = clean(d)
		} else {
			out[i] = d
		}
	}
	return out
}

func formatDroppedHeader(dropped []string) string {
	if len(dropped) == 0 {
		return ""
	}
	prefix := fmt.Sprintf("%d (%s)", len(dropped), formatDroppedKinds(dropped, maxDroppedKindsBytes))
	limit := min(len(dropped), shownDropped)
	samples := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		samples = append(samples, formatDroppedSample(dropped[i], maxDroppedSampleBytes))
	}
	out := fmt.Sprintf("%s; + %d more", prefix, len(dropped))
	for k := 1; k <= len(samples); k++ {
		cand := prefix + ": " + strings.Join(samples[:k], "; ")
		if rest := len(dropped) - k; rest > 0 {
			cand += fmt.Sprintf("; + %d more", rest)
		}
		if len(cand) > maxDroppedSummaryBytes {
			break
		}
		out = cand
	}
	if len(out) > maxDroppedSummaryBytes {
		return clipUTF8(out, maxDroppedSummaryBytes)
	}
	return out
}

func formatDroppedKinds(dropped []string, maxBytes int) string {
	counts := map[string]int{}
	for _, d := range dropped {
		counts[droppedKind(d)]++
	}
	kinds := make([]string, 0, len(counts))
	for k := range counts {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	var b strings.Builder
	for i, k := range kinds {
		part := fmt.Sprintf("%s %d", k, counts[k])
		sep := ""
		if b.Len() > 0 {
			sep = ", "
		}
		tail := ""
		if rem := len(kinds) - (i + 1); rem > 0 {
			tail = fmt.Sprintf(", +%d kinds", rem)
		}
		if b.Len()+len(sep)+len(part)+len(tail) > maxBytes && b.Len() > 0 {
			fmt.Fprintf(&b, ", +%d kinds", len(kinds)-i)
			break
		}
		b.WriteString(sep)
		b.WriteString(part)
	}
	return b.String()
}

func droppedKind(raw string) string {
	s := strings.TrimSpace(strings.ToValidUTF8(raw, "\ufffd"))
	if s == "" {
		return "unknown"
	}
	if strings.HasPrefix(s, "[redacted by fyr]") {
		return "redacted"
	}
	cut := strings.IndexFunc(s, func(r rune) bool { return r == ':' || unicode.IsSpace(r) })
	if cut == 0 {
		return "unknown"
	}
	if cut > 0 {
		s = s[:cut]
	}
	s = clipUTF8(s, maxDroppedKindBytes)
	if s == "" {
		return "unknown"
	}
	return s
}

func formatDroppedSample(raw string, maxBytes int) string {
	s := strings.Join(strings.Fields(strings.ToValidUTF8(raw, "\ufffd")), " ")
	if s == "" {
		return "(empty)"
	}
	if len(s) <= maxBytes {
		return s
	}
	if maxBytes <= 3 {
		return clipUTF8(s, maxBytes)
	}
	head := clipUTF8(s, maxBytes-3)
	if head == "" {
		return clipUTF8(s, maxBytes)
	}
	return head + "..."
}

// section is one proposal's text. The model's text is indented four spaces,
// line by line, so it reads as quoted data, never as headings or fields of
// the record (audit 2026-09-30, SD3).
func (o Options) section(p proposal) string {
	target := "project"
	if strings.ToLower(o.line(p.Target)) == "harness" {
		target = "harness"
	}
	kind := strings.ToLower(o.line(p.Kind))
	sigLine := ""
	if sig := cleanSignalID(o.line(p.SignalID)); sig != "" {
		sigLine = "Signal: " + sig + "\n"
	}
	return fmt.Sprintf("Target: %s   Kind: %s   File: %s\n%s\n%s\n\nEvidence: %s\n",
		target, kind, o.line(p.File), sigLine, indent(strings.TrimSpace(o.clean(p.Text))), o.line(p.Evidence))
}

func (o Options) clean(s string) string { return scrub(s, o.Home) }

// indent prefixes every line of s with four spaces.
func indent(s string) string {
	s = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(s)
	return "    " + strings.ReplaceAll(s, "\n", "\n    ")
}

// line is a one-line field: scrubbed, and its line breaks folded, so a model's
// title can never open a second **Status:** line or a fake section in the
// Status-driven queue (security review 2026-09-30).
func (o Options) line(s string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ").Replace(o.clean(s))), " ")
}

// unique is dir/base+ext, or with -2, -3 … when that exists.
func unique(dir, base, ext string) (string, error) {
	return uniqueFree(dir, base, ext, nil)
}

func uniqueFree(dir, base, ext string, reserved map[string]bool) (string, error) {
	for n := 1; n < 1000; n++ {
		name := base
		if n > 1 {
			name += "-" + strconv.Itoa(n)
		}
		p := filepath.Join(dir, name+ext)
		_, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			if !reserved[p] {
				return p, nil
			}
			continue
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("no free name for %s in %s", base, dir)
}
