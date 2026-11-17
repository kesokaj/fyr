package dream

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/search"
)

func defaultWriteRatings(home, _ string, rows []metrics.SessionRating) error {
	if len(rows) == 0 {
		return nil
	}
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		return err
	}
	defer s.Close()
	return s.UpsertSessionRatings(rows)
}

// write stores validated ratings, draft skills under proposals/dream/drafts/,
// and each proposal (plus rating-triggered harness skill proposals) as global
// queued `proposal` records. It returns the count of non-draft-wrapper proposals.
func (o Options) write(conf string, c Collected, r response, cli string, t0 time.Time) (int, error) {
	day := t0.Format("2006-01-02")
	draftLines, err := o.writeDrafts(filepath.Join(conf, "proposals", "dream"), c.Name, day, r.Skills)
	if err != nil {
		return 0, err
	}
	ratings := o.validRatings(c.Root, r.Ratings, t0)
	if len(ratings) > 0 {
		writeRatings := o.WriteRatings
		if writeRatings == nil {
			writeRatings = defaultWriteRatings
		}
		if err := writeRatings(o.Home, c.Root, ratings); err != nil {
			return 0, err
		}
	}
	head := o.header(c, cli, day)
	ps := o.buildProposals(head, r.Proposals, ratings)
	nProps := len(ps)
	if len(draftLines) > 0 {
		ps = append(ps, brain.Proposal{Title: "Dream, harness — draft skills for " + c.Name, Body: head + strings.Join(draftLines, "\n") + "\n"})
	}
	if _, err := propose(o.Home, proposalProject(o, c.Root), ps, t0); err != nil {
		return 0, err
	}
	return nProps, nil
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

func (o Options) writeDrafts(dir, name, day string, skills []draft) ([]string, error) {
	var drafts []draft
	var dropped []string
	for _, d := range skills {
		if skillName.MatchString(d.Name) {
			drafts = append(drafts, d)
		} else {
			dropped = append(dropped, d.Name)
		}
	}
	var lines []string
	for _, d := range drafts {
		dst, err := unique(filepath.Join(dir, "drafts"), day+"-"+name+"-"+d.Name, "")
		if err != nil {
			return nil, err
		}
		desc, _ := json.Marshal(o.clean(d.Description))
		body := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", d.Name, desc, strings.TrimSpace(o.clean(d.Body)))
		if err := os.MkdirAll(dst, 0o700); err != nil {
			return nil, err
		}
		if err := harness.WriteAtomic(filepath.Join(dst, "SKILL.md"), []byte(body), 0o600); err != nil {
			return nil, err
		}
		lines = append(lines, "Draft skill: "+filepath.Join(dir, "drafts", filepath.Base(dst), "SKILL.md"))
	}
	for _, n := range dropped {
		lines = append(lines, fmt.Sprintf("Draft skill %q dropped: not a kebab-case name", o.line(n)))
	}
	return lines, nil
}

func (o Options) buildProposals(head string, props []proposal, ratings []metrics.SessionRating) []brain.Proposal {
	var ps []brain.Proposal
	for _, p := range props {
		// The target leads the title, so /retro tells a project's proposals
		// from the harness's without opening each (security review of the
		// dream over records, finding 3).
		target := "project"
		if p.Target == "harness" {
			target = "harness"
		}
		ps = append(ps, brain.Proposal{Title: "Dream, " + target + " — " + o.line(p.Title), Body: head + o.section(p)})
	}
	for _, rt := range ratings {
		if needsSkillProposal(rt) {
			ps = append(ps, o.ratingProposal(head, rt))
		}
	}
	return ps
}

func needsSkillProposal(r metrics.SessionRating) bool {
	return r.SkillAction == "rewrite-skill" || r.SkillAction == "new-skill" || r.OutcomeScore <= 3 || r.TokenEfficiency <= 3
}

func (o Options) validRatings(root string, raw []sessionRating, t0 time.Time) []metrics.SessionRating {
	seen := map[string]bool{}
	var out []metrics.SessionRating
	for _, sr := range raw {
		if r, ok := o.validateRating(root, sr, t0); ok && !seen[r.SessionID] {
			seen[r.SessionID] = true
			out = append(out, r)
		}
	}
	return out
}

func (o Options) validateRating(root string, sr sessionRating, t0 time.Time) (metrics.SessionRating, bool) {
	id := strings.TrimSpace(o.line(sr.id()))
	if id == "" || strings.ContainsAny(id, `/\ `) || id == "." || id == ".." ||
		strings.ContainsFunc(id, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return metrics.SessionRating{}, false
	}
	if sr.OutcomeScore < 1 || sr.OutcomeScore > 5 || sr.TokenEfficiency < 1 || sr.TokenEfficiency > 5 {
		return metrics.SessionRating{}, false
	}
	pa := strings.ToLower(strings.TrimSpace(o.line(sr.PlanAlignment)))
	switch pa {
	case "matched", "partial", "missed", "no-plan":
	default:
		return metrics.SessionRating{}, false
	}
	sa := strings.ToLower(strings.TrimSpace(o.line(sr.SkillAction)))
	if sa == "" {
		sa = "none"
	}
	switch sa {
	case "none", "rewrite-skill", "new-skill":
	default:
		return metrics.SessionRating{}, false
	}
	out := metrics.SessionRating{
		SessionID:     id,
		ProjectRoot:   root,
		EvaluatedAt:   t0.UnixMilli(),
		OutcomeScore:  sr.OutcomeScore,
		PlanAlignment: pa,
		SkillAction:   sa,
		TargetSkill:   cleanTargetSkill(o.line(sr.TargetSkill)),
		Summary:       strings.TrimSpace(o.line(sr.Summary)),
	}
	setInt(&out.TokenEfficiency, sr.TokenEfficiency)
	return out, true
}

func setInt(dst *int, v int) { *dst = v }

func cleanTargetSkill(s string) string {
	s = strings.TrimSpace(filepath.ToSlash(s))
	if s == "" || strings.HasPrefix(s, "/") || strings.ContainsRune(s, 0) {
		return ""
	}
	for _, seg := range strings.Split(s, "/") {
		if seg == ".." {
			return ""
		}
	}
	trailing := strings.HasSuffix(s, "/")
	c := path.Clean(s)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return ""
	}
	if trailing && !strings.HasSuffix(c, "/") {
		c += "/"
	}
	return c
}

func (o Options) ratingProposal(head string, r metrics.SessionRating) brain.Proposal {
	file := "skills/"
	if r.TargetSkill != "" {
		file = r.TargetSkill
	}
	titleTarget := file
	if r.SkillAction == "new-skill" && file == "skills/" {
		titleTarget = "new skill"
	}
	title := fmt.Sprintf("Dream, harness — %s (%s)", o.line(titleTarget), o.line(r.SessionID))
	text := strings.TrimSpace(o.clean(r.Summary))
	if text == "" {
		text = fmt.Sprintf("Conversation %s rated outcome %d/5, token efficiency %d/5 (%s).", r.SessionID, r.OutcomeScore, r.TokenEfficiency, r.PlanAlignment)
	}
	ev := fmt.Sprintf("conversation %s (outcome %d/5, token_efficiency %d/5, plan_alignment %s)",
		o.line(r.SessionID), r.OutcomeScore, r.TokenEfficiency, o.line(r.PlanAlignment))
	body := head + fmt.Sprintf("Target: harness   Kind: skill   File: %s\nSessions: 2\n\n%s\n\nEvidence: %s\n",
		o.line(file), indent(text), ev)
	return brain.Proposal{Title: title, Body: body}
}

// propose is brain.ProposeAll; a test replaces it.
var propose = brain.ProposeAll

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
		fmt.Fprintf(&b, "Left out: %s\n", o.line(strings.Join(c.Dropped, "; ")))
	}
	b.WriteString("\n")
	return b.String()
}

// section is one proposal's text. The model's text is indented four spaces,
// line by line, so it reads as quoted data, never as headings or fields of
// the record (audit 2026-09-30, SD3).
func (o Options) section(p proposal) string {
	target := "project"
	if p.Target == "harness" {
		target = "harness"
	}
	return fmt.Sprintf("Target: %s   Kind: %s   File: %s\n\n%s\n\nEvidence: %s\n",
		target, o.line(p.Kind), o.line(p.File), indent(strings.TrimSpace(o.clean(p.Text))), o.line(p.Evidence))
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
	for n := 1; n < 1000; n++ {
		name := base
		if n > 1 {
			name += "-" + strconv.Itoa(n)
		}
		p := filepath.Join(dir, name+ext)
		if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
			return p, nil
		}
	}
	return "", fmt.Errorf("no free name for %s in %s", base, dir)
}
