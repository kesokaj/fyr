package auto

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/isolate"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/search"
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
	for _, c := range rotatedClasses(r.isHarnessCheckout(root), p.LastClass) {
		if t := r.triggerProjectClass(root, p, c.Name, findings); t != nil {
			return t
		}
	}
	return nil
}

func (r *run) triggerProjectClass(root string, p *Project, class string, _ []Finding) *Task {
	switch class {
	case ClassMemoryConsolidation:
		if n := r.countMemoriesSince(root, p.LastMemoryConsolidation); n >= memoryConsolidationThreshold {
			return preparedTask(ClassMemoryConsolidation, nil, "", n)
		}
	case ClassRetroLite:
		if convs := r.conversationsSince(root, p.LastRetroLite); len(convs) > 0 {
			return preparedTask(ClassRetroLite, nil, "", len(convs))
		}
	case ClassMemoryCandidates:
		if p.PendingMemoryCandidates > 0 {
			return preparedTask(ClassMemoryCandidates, nil, "", p.PendingMemoryCandidates)
		}
	case ClassDistill:
		if slug, n := r.repeatedVerbSequence(root); n >= distillRepeatThreshold && slug != "" {
			if r.isHarnessCheckout(root) {
				t := preparedTask(ClassDistill, []string{"skills/auto-distilled-" + slug + "/"}, "", n)
				if t != nil {
					t.Outcome = OutcomeEvalGate
				}
				return t
			}
			return preparedTask(ClassDistill, []string{".agents/skills/auto-distilled-" + slug + "/"}, "", n)
		}
	case ClassMissingSkill:
		if id, n := r.missingSkillProposal(root); id != "" {
			return preparedTask(ClassMissingSkill, nil, id, n)
		}
	case ClassHarnessEdit:
		return r.triggerHarnessEdit(root)
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

func (r *run) conversationCutoff() time.Time {
	if r.cfg.ConversationIdle == "" {
		return time.Time{}
	}
	idle, err := span(r.cfg.ConversationIdle)
	if err != nil || idle <= 0 {
		return time.Time{}
	}
	return r.o.now().Add(-idle)
}

func (r *run) conversationsSince(root string, since time.Time) []convSource {
	var out []convSource
	cutoff := r.conversationCutoff()
	homes := []string{r.o.realHome()}
	if r.o.Home != "" && r.o.Home != homes[0] {
		homes = append(homes, r.o.Home)
	}
	seen := map[string]bool{}
	for _, h := range homes {
		if h == "" {
			continue
		}
		if agy, _, err := search.AgyConversationTextInWindow(h, root, since, cutoff); err == nil {
			for _, a := range agy {
				if !seen[a.ID] && strings.TrimSpace(a.Text) != "" {
					seen[a.ID] = true
					out = append(out, convSource{label: "antigravity " + a.ID, text: a.Text})
				}
			}
		}
		out = appendDirConversations(out, seen, h, root, since, cutoff)
	}
	return out
}

func appendDirConversations(out []convSource, seen map[string]bool, home, root string, since, cutoff time.Time) []convSource {
	for _, sub := range []string{
		filepath.Join(home, ".gemini", "antigravity", "conversations"),
		filepath.Join(home, ".gemini", "antigravity-cli", "brain"),
	} {
		entries, err := os.ReadDir(sub)
		if err != nil {
			continue
		}
		for _, e := range entries {
			p := filepath.Join(sub, e.Name())
			info, err := e.Info()
			if err != nil || (!since.IsZero() && !info.ModTime().After(since)) || (!cutoff.IsZero() && info.ModTime().After(cutoff)) {
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
	return out
}

func readFileMentionsRoot(path, root string) (string, bool) {
	f, err := isolate.OpenRegular(path, os.O_RDONLY)
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
	skillPath := filepath.Join(root, ".agents", "skills", "auto-distilled-"+slug, "SKILL.md")
	if r.isHarnessCheckout(root) {
		skillPath = filepath.Join(root, "skills", "auto-distilled-"+slug, "SKILL.md")
	}
	if _, err := os.Stat(skillPath); err == nil {
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
