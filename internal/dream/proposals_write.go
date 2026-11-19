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
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
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

type plannedDraft struct {
	dst  string
	body []byte
}

type draftPlan struct {
	items []plannedDraft
	lines []string
}

// write stores validated proposals, ratings, and draft skills (in order:
// planDrafts -> propose -> WriteRatings -> commitDrafts) so drafts and ratings
// only persist after propose() succeeds. Returns non-draft-wrapper proposals.
func (o Options) write(conf string, c Collected, r response, cli string, t0 time.Time) (int, error) {
	day := t0.Format("2006-01-02")
	plan, err := o.planDrafts(filepath.Join(conf, "proposals", "dream"), c.Name, day, r.Skills)
	if err != nil {
		return 0, err
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
		return 0, err
	}
	nProps := len(ps)
	if hasWrapper && nProps > 0 && ps[nProps-1].Title == wrapperTitle {
		nProps--
	}
	if len(ps) > 0 {
		if _, err := propose(o.Home, proj, ps, t0); err != nil {
			return 0, err
		}
	}
	if err := o.persistRatings(c.Root, ratings); err != nil {
		return 0, err
	}
	if err := commitDrafts(plan); err != nil {
		return 0, err
	}
	return nProps, nil
}

func (o Options) persistRatings(root string, ratings []metrics.SessionRating) error {
	if len(ratings) == 0 {
		return nil
	}
	writeRatings := o.WriteRatings
	if writeRatings == nil {
		writeRatings = defaultWriteRatings
	}
	return writeRatings(o.Home, root, ratings)
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
	return proposal{Title: title, Kind: kind, Target: target, File: p.File, Text: text, Evidence: p.Evidence}, true
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

type proposalMeta struct {
	normTitle string
	target    string
	kind      string
	file      string
	sessionID string
	draftKey  string
}

type proposalIndex struct {
	harnessTitles map[string]bool
	projectTitles map[string][]string
	sessions      map[string]bool
	skillTargets  map[string]bool
	drafts        map[string]bool
}

func newProposalIndex() *proposalIndex {
	return &proposalIndex{
		harnessTitles: map[string]bool{},
		projectTitles: map[string][]string{},
		sessions:      map[string]bool{},
		skillTargets:  map[string]bool{},
		drafts:        map[string]bool{},
	}
}

func parseProposalMeta(title, body string) proposalMeta {
	m := proposalMeta{normTitle: strings.ToLower(strings.Join(strings.Fields(title), " ")), target: "project"}
	if strings.HasPrefix(title, "Dream, harness — draft skills for ") {
		m.target = "harness"
		var lines []string
		for _, ln := range strings.Split(body, "\n") {
			if strings.HasPrefix(ln, "Draft skill") {
				lines = append(lines, ln)
			}
		}
		m.draftKey = m.normTitle + "\n" + strings.Join(lines, "\n")
		return m
	}
	if strings.HasPrefix(title, "Dream, harness") {
		m.target = "harness"
	}
	for _, ln := range strings.Split(body, "\n") {
		if strings.HasPrefix(ln, "Target: ") {
			parseTargetLine(ln, &m)
		} else if rest, ok := strings.CutPrefix(ln, "Evidence: conversation "); ok {
			if fs := strings.Fields(rest); len(fs) > 0 {
				m.sessionID = fs[0]
			}
		}
	}
	return m
}

func parseTargetLine(ln string, m *proposalMeta) {
	rest, ok := strings.CutPrefix(ln, "Target: ")
	if !ok {
		return
	}
	if t, after, ok := strings.Cut(rest, "Kind:"); ok {
		if strings.TrimSpace(t) == "harness" {
			m.target = "harness"
		} else {
			m.target = "project"
		}
		if k, f, ok := strings.Cut(after, "File:"); ok {
			m.kind = strings.ToLower(strings.TrimSpace(k))
			m.file = strings.TrimSpace(f)
		}
	}
}

func (idx *proposalIndex) add(project string, m proposalMeta) {
	if m.draftKey != "" {
		idx.drafts[m.draftKey] = true
		return
	}
	if m.sessionID != "" {
		idx.sessions[m.sessionID] = true
	}
	if m.target == "harness" {
		idx.harnessTitles[m.normTitle] = true
		if m.kind == "skill" && m.file != "" {
			idx.skillTargets[m.file] = true
		}
		return
	}
	idx.projectTitles[m.normTitle] = append(idx.projectTitles[m.normTitle], project)
}

func (idx *proposalIndex) seen(project string, m proposalMeta) bool {
	if m.draftKey != "" {
		return idx.drafts[m.draftKey]
	}
	if m.sessionID != "" && idx.sessions[m.sessionID] {
		return true
	}
	if m.target == "harness" {
		if m.kind == "skill" && m.file != "" && idx.skillTargets[m.file] {
			return true
		}
		return idx.harnessTitles[m.normTitle]
	}
	for _, p := range idx.projectTitles[m.normTitle] {
		if p == project || trail.SameDir(p, project) {
			return true
		}
	}
	return false
}

func dedupeProposals(home, project string, ps []brain.Proposal) ([]brain.Proposal, error) {
	if len(ps) == 0 {
		return nil, nil
	}
	idx := newProposalIndex()
	if brainDBExists(home) {
		recs, err := brain.List(brain.Options{Home: home, Global: true}, brain.ListFilter{Kind: "proposal"})
		if err != nil {
			return nil, err
		}
		for _, rec := range recs {
			if rec.Status == "queued" || rec.Status == "applied" || strings.HasPrefix(rec.Status, "applied:") {
				idx.add(rec.Project, parseProposalMeta(rec.Title, rec.Body))
			}
		}
	}
	var out []brain.Proposal
	for _, p := range ps {
		m := parseProposalMeta(p.Title, p.Body)
		if idx.seen(project, m) {
			continue
		}
		idx.add(project, m)
		out = append(out, p)
	}
	return out, nil
}

func targetSessionCounts(ratings []metrics.SessionRating) map[string]int {
	seen := map[string]map[string]bool{}
	for _, rt := range ratings {
		if !needsSkillProposal(rt) {
			continue
		}
		target := resolvedSkillTarget(rt.TargetSkill)
		if seen[target] == nil {
			seen[target] = map[string]bool{}
		}
		seen[target][rt.SessionID] = true
	}
	counts := make(map[string]int, len(seen))
	for target, ids := range seen {
		counts[target] = len(ids)
	}
	return counts
}

func needsSkillProposal(r metrics.SessionRating) bool {
	return r.SkillAction == "rewrite-skill" || r.SkillAction == "new-skill"
}

func collectedTranscriptIDs(inputs []Input) map[string]bool {
	ids := make(map[string]bool)
	for _, in := range inputs {
		if in.Kind != kindTranscript {
			continue
		}
		if id, ok := strings.CutPrefix(in.Label, "antigravity "); ok {
			if id = strings.TrimSpace(id); id != "" {
				ids[id] = true
			}
		}
	}
	return ids
}

func (o Options) validRatings(c Collected, raw []sessionRating, t0 time.Time) []metrics.SessionRating {
	allowed := collectedTranscriptIDs(c.Inputs)
	seen := map[string]bool{}
	var out []metrics.SessionRating
	for _, sr := range raw {
		if r, ok := o.validateRating(c.Root, allowed, sr, t0); ok && !seen[r.SessionID] {
			seen[r.SessionID] = true
			out = append(out, r)
		}
	}
	return out
}

func (o Options) validateRating(root string, allowed map[string]bool, sr sessionRating, t0 time.Time) (metrics.SessionRating, bool) {
	id := strings.TrimSpace(o.line(sr.id()))
	if id == "" || len(id) > 128 || !allowed[id] || strings.ContainsAny(id, `/\ `) || id == "." || id == ".." ||
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
	ts := cleanTargetSkill(o.line(sr.TargetSkill))
	if len(ts) > 200 {
		return metrics.SessionRating{}, false
	}
	out := metrics.SessionRating{
		SessionID:     id,
		ProjectRoot:   root,
		EvaluatedAt:   t0.UnixMilli(),
		OutcomeScore:  sr.OutcomeScore,
		PlanAlignment: pa,
		SkillAction:   sa,
		TargetSkill:   ts,
		Summary:       clipUTF8(strings.TrimSpace(o.line(sr.Summary)), 400),
	}
	setInt(&out.TokenEfficiency, sr.TokenEfficiency)
	return out, true
}

func clipUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes && utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		n := utf8.RuneLen(r)
		if b.Len()+n > maxBytes {
			break
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
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
	if c == "skills" {
		return "skills/"
	}
	if trailing && !strings.HasSuffix(c, "/") {
		c += "/"
	}
	if !strings.HasPrefix(c, "skills/") {
		return ""
	}
	return c
}

func resolvedSkillTarget(raw string) string {
	if c := cleanTargetSkill(raw); c != "" {
		return c
	}
	return "skills/"
}

func (o Options) ratingProposal(head string, r metrics.SessionRating, sessionCount int) brain.Proposal {
	file := resolvedSkillTarget(r.TargetSkill)
	titleTarget := file
	if r.SkillAction == "new-skill" && file == "skills/" {
		titleTarget = "new skill"
	}
	if sessionCount < 1 {
		sessionCount = 1
	}
	title := fmt.Sprintf("Dream, harness — %s (%s)", o.line(titleTarget), o.line(r.SessionID))
	text := strings.TrimSpace(o.clean(r.Summary))
	if text == "" {
		text = fmt.Sprintf("Conversation %s rated outcome %d/5, token efficiency %d/5 (%s).", r.SessionID, r.OutcomeScore, r.TokenEfficiency, r.PlanAlignment)
	}
	ev := fmt.Sprintf("conversation %s (outcome %d/5, token_efficiency %d/5, plan_alignment %s)",
		o.line(r.SessionID), r.OutcomeScore, r.TokenEfficiency, o.line(r.PlanAlignment))
	body := head + fmt.Sprintf("Target: harness   Kind: skill   File: %s\nSessions: %d\n\n%s\n\nEvidence: %s\n",
		o.line(file), sessionCount, indent(text), ev)
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
	if strings.ToLower(o.line(p.Target)) == "harness" {
		target = "harness"
	}
	kind := strings.ToLower(o.line(p.Kind))
	return fmt.Sprintf("Target: %s   Kind: %s   File: %s\n\n%s\n\nEvidence: %s\n",
		target, kind, o.line(p.File), indent(strings.TrimSpace(o.clean(p.Text))), o.line(p.Evidence))
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
