package auto

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/sandbox"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
	"github.com/kesokaj/fyr/internal/work"
)

const (
	memoryConsolidationThreshold = 10
	distillRepeatThreshold       = 3
	learningInputCap             = 48 << 10
)

var distillSlugRe = regexp.MustCompile(`[^a-z0-9-]+`)

// pickProjectTask selects the highest-priority due class for root after
// rotating p.LastClass to the end (§ Task classes).
func (r *run) pickProjectTask(root string, p *Project, findings []Finding) *Task {
	for _, c := range rotatedClasses(r.cfg.Level, p.LastClass) {
		if t := r.triggerProjectClass(root, p, c.Name, findings); t != nil {
			return t
		}
	}
	return nil
}

func (r *run) triggerProjectClass(root string, p *Project, class string, findings []Finding) *Task {
	level := r.cfg.Level
	switch class {
	case ClassSpecDrift:
		return triggerSpecDrift(level, findings)
	case ClassDocDrift:
		return triggerDocDrift(level, findings)
	case ClassMemoryConsolidation:
		if n := r.countMemoriesSince(root, p.LastMemoryConsolidation); n >= memoryConsolidationThreshold {
			return preparedTask(ClassMemoryConsolidation, level, nil, "", n)
		}
	case ClassRetroLite:
		if convs := r.conversationsSince(root, p.LastRetroLite); len(convs) > 0 {
			return preparedTask(ClassRetroLite, level, nil, "", len(convs))
		}
	case ClassMemoryCandidates:
		if p.PendingMemoryCandidates > 0 {
			return preparedTask(ClassMemoryCandidates, level, nil, "", p.PendingMemoryCandidates)
		}
	case ClassOnboarding:
		if n := onboardingAge(r.o.Home, root); n > onboardingCommitLimit {
			return preparedTask(ClassOnboarding, level, []string{"GUIDE-onboarding"}, "", n)
		}
	case ClassDistill:
		if slug, n := r.repeatedVerbSequence(root); n >= distillRepeatThreshold && slug != "" {
			return preparedTask(ClassDistill, level, []string{".agents/skills/auto-distilled-" + slug + "/"}, "", n)
		}
	case ClassMissingSkill:
		if id, n := r.missingSkillProposal(root); id != "" {
			return preparedTask(ClassMissingSkill, level, nil, id, n)
		}
	case ClassContextAdequacy:
		if n := r.sessionsAboveMedianFriction(root); n > 0 {
			return preparedTask(ClassContextAdequacy, level, nil, "", n)
		}
	case ClassScaffoldDrift:
		if n := r.uneditedScaffoldCount(root); n > 0 {
			return preparedTask(ClassScaffoldDrift, level, nil, "", n)
		}
	case ClassEvalRerun:
		return r.triggerEvalRerun(root, p)
	case ClassHarnessEdit:
		return r.triggerHarnessEdit(root)
	case ClassMiniAudit:
		return r.triggerMiniAudit(root, p)
	}
	return nil
}

func (r *run) countMemoriesSince(root string, since time.Time) int {
	recs, err := brain.List(brain.Options{Home: r.o.Home, Dir: root, Now: r.o.Now}, brain.ListFilter{Kind: "memory"})
	if err != nil {
		return 0
	}
	n := 0
	for _, rec := range recs {
		if rec.Status != "closed" {
			continue
		}
		if since.IsZero() {
			n++
			continue
		}
		if t, err := time.Parse(time.RFC3339, rec.Updated); err == nil && t.After(since) {
			n++
		}
	}
	return n
}

type convSource struct {
	label, text string
}

func (r *run) conversationsSince(root string, since time.Time) []convSource {
	var out []convSource
	homes := []string{r.o.realHome()}
	if r.o.Home != "" && r.o.Home != homes[0] {
		homes = append(homes, r.o.Home)
	}
	seen := map[string]bool{}
	for _, h := range homes {
		if h == "" {
			continue
		}
		if agy, _, err := search.AgyConversationText(h, root, since); err == nil {
			for _, a := range agy {
				if !seen[a.ID] && strings.TrimSpace(a.Text) != "" {
					seen[a.ID] = true
					out = append(out, convSource{label: "antigravity " + a.ID, text: a.Text})
				}
			}
		}
		for _, sub := range []string{
			filepath.Join(h, ".gemini", "antigravity", "conversations"),
			filepath.Join(h, ".gemini", "antigravity-cli", "brain"),
		} {
			entries, err := os.ReadDir(sub)
			if err != nil {
				continue
			}
			for _, e := range entries {
				p := filepath.Join(sub, e.Name())
				info, err := e.Info()
				if err != nil || (!since.IsZero() && !info.ModTime().After(since)) {
					continue
				}
				if info.Mode().IsRegular() && !seen[p] {
					if raw, ok := readFileMentionsRoot(p, root); ok {
						seen[p] = true
						out = append(out, convSource{label: filepath.Base(p), text: redact.RedactJSONL(raw)})
					}
				}
			}
		}
	}
	return out
}

func readFileMentionsRoot(path, root string) (string, bool) {
	f, err := sandbox.OpenRegular(path, os.O_RDONLY)
	if err != nil {
		return "", false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 256<<10))
	if err != nil {
		return "", false
	}
	s := string(b)
	return s, strings.Contains(s, root)
}

func (r *run) repeatedVerbSequence(root string) (string, int) {
	bySess, order := r.readSessionVerbs(root)
	counts := map[string]int{}
	bestSeq, bestN := "", 0
	for _, sid := range order {
		vs := bySess[sid]
		if len(vs) < 2 {
			continue
		}
		seq := strings.Join(vs, ",")
		counts[seq]++
		if counts[seq] > bestN {
			bestSeq, bestN = seq, counts[seq]
		}
	}
	if bestN < distillRepeatThreshold {
		return "", 0
	}
	slug := strings.Trim(distillSlugRe.ReplaceAllString(strings.ToLower(bestSeq), "-"), "-")
	if len(slug) > 24 {
		slug = strings.TrimRight(slug[:24], "-")
	}
	if slug == "" {
		slug = "workflow"
	}
	if _, err := os.Stat(filepath.Join(root, ".agents", "skills", "auto-distilled-"+slug, "SKILL.md")); err == nil {
		return "", 0
	}
	return slug, bestN
}

func (r *run) readSessionVerbs(root string) (map[string][]string, []string) {
	mp := metrics.Path(r.o.Home)
	if _, err := os.Lstat(mp); err != nil {
		return nil, nil
	}
	tmp, err := os.MkdirTemp(filepath.Dir(mp), "auto-metrics-")
	if err != nil {
		return nil, nil
	}
	defer os.RemoveAll(tmp)
	s, err := metrics.Snapshot(mp, tmp)
	if err != nil {
		return nil, nil
	}
	defer s.Close()
	bySess, order, err := s.ProjectVerbs(root, 100)
	if err != nil {
		return nil, nil
	}
	return bySess, order
}

func (r *run) missingSkillProposal(root string) (string, int) {
	recs, err := brain.List(brain.Options{Home: r.o.Home, Global: true, Now: r.o.Now}, brain.ListFilter{Kind: "proposal", Project: root, Open: true})
	if err != nil {
		return "", 0
	}
	var ids []string
	for _, rec := range recs {
		lowTitle := strings.ToLower(rec.Title)
		lowBody := strings.ToLower(rec.Body)
		if strings.Contains(lowTitle, "missing skill") || strings.Contains(lowBody, "missing-skill:") {
			ids = append(ids, rec.ID)
		}
	}
	if len(ids) == 0 {
		return "", 0
	}
	return ids[0], len(ids)
}

func (r *run) sessionsAboveMedianFriction(root string) int {
	vals := r.readSessionFrictions(root)
	if len(vals) < 3 {
		return 0
	}
	sorted := slices.Clone(vals)
	slices.Sort(sorted)
	median := sorted[len(sorted)/2]
	above := 0
	for _, v := range vals {
		if v > median && v > 0 {
			above++
		}
	}
	return above
}

func (r *run) readSessionFrictions(root string) []int {
	mp := metrics.Path(r.o.Home)
	if _, err := os.Lstat(mp); err != nil {
		return nil
	}
	tmp, err := os.MkdirTemp(filepath.Dir(mp), "auto-metrics-")
	if err != nil {
		return nil
	}
	defer os.RemoveAll(tmp)
	s, err := metrics.Snapshot(mp, tmp)
	if err != nil {
		return nil
	}
	defer s.Close()
	vals, err := s.ProjectFrictions(root, r.o.now().AddDate(0, 0, -30))
	if err != nil {
		return nil
	}
	return vals
}

func (r *run) uneditedScaffoldCount(root string) int {
	n := 0
	assetsDir := filepath.Join(r.o.Home, harness.AgyRoot, "skills", "fyr-init-project", "assets")
	for _, name := range []string{"SDD.md", "TDD.md", "TODO.md"} {
		f, err := sandbox.OpenRegular(filepath.Join(root, name), os.O_RDONLY)
		if err != nil {
			continue
		}
		b, rerr := io.ReadAll(io.LimitReader(f, 64<<10))
		f.Close()
		if rerr != nil {
			continue
		}
		s := string(b)
		if strings.Contains(s, "<!-- TODO:") || strings.Contains(s, "_[Replace with") || strings.Contains(s, "<one line:") {
			n++
			continue
		}
		if tmpl, err := os.ReadFile(filepath.Join(assetsDir, name)); err == nil && !bytes.Equal(b, tmpl) {
			if out, err := trail.Command(root, "log", "-n", "2", "--format=%H", "--", name).Output(); err == nil {
				if commits := strings.Fields(string(out)); len(commits) == 1 {
					n++
				}
			}
		}
	}
	return n
}

// unjudgedCanaryModel returns the first active, non-interactive, non-retiring
// model in <home>/.config/fyr/models.json with 0 combined empirical runs, if any.
func (r *run) unjudgedCanaryModel() string {
	cat, ok, err := work.LoadCatalog(r.o.Home)
	if err != nil || !ok {
		return ""
	}
	cands := work.UnjudgedActiveModels(cat)
	if len(cands) == 0 {
		return ""
	}
	return cands[0].ID
}
