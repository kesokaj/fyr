package dream

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
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
	eff := sr.TokenEfficiency
	return metrics.SessionRating{
		SessionID:       id,
		ProjectRoot:     root,
		EvaluatedAt:     t0.UnixMilli(),
		OutcomeScore:    sr.OutcomeScore,
		TokenEfficiency: eff,
		PlanAlignment:   pa,
		SkillAction:     sa,
		TargetSkill:     ts,
		Summary:         strings.TrimSpace(trail.TruncateUTF8(strings.ToValidUTF8(strings.TrimSpace(o.line(sr.Summary)), "\ufffd"), 400)),
	}, true
}

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
