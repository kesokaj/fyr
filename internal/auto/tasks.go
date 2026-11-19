package auto

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
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
			if convs := r.conversationsSince(root, p.LastMemoryCandidates); len(convs) > 0 {
				return preparedTask(ClassMemoryCandidates, nil, "", len(convs))
			}
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
	return r.conversationsSinceCutoff(root, since, r.sweepConversationCutoff())
}

func (r *run) conversationsSinceCutoff(root string, since, cutoff time.Time) []convSource {
	if r.sweepCache != nil && r.sweepCache.root == root {
		ck := makeConvCacheKey(since, cutoff)
		if cached, ok := r.sweepCache.convs[ck]; ok {
			return slices.Clone(cached)
		}
	}
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
	if r.sweepCache != nil && r.sweepCache.root == root {
		r.sweepCache.convs[makeConvCacheKey(since, cutoff)] = slices.Clone(out)
	}
	return out
}

func appendDirConversations(out []convSource, seen map[string]bool, home, root string, since, cutoff time.Time) []convSource {
	brainDir := filepath.Join(home, ".gemini", "antigravity-cli", "brain")
	entries, err := os.ReadDir(brainDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		convID := e.Name()
		if seen[convID] || convID == "" || convID == "." || convID == ".." {
			continue
		}
		p, info, ok := openBrainTranscript(brainDir, convID)
		if !ok || (!since.IsZero() && !info.ModTime().After(since)) || (!cutoff.IsZero() && info.ModTime().After(cutoff)) {
			continue
		}
		if raw, ok := readFileMentionsRoot(p, root); ok {
			seen[convID] = true
			out = append(out, convSource{label: convID, text: redact.RedactJSONL(raw)})
		}
	}
	return out
}

func openBrainTranscript(brainDir, convID string) (string, os.FileInfo, bool) {
	cur := brainDir
	for _, seg := range []string{convID, ".system_generated", "logs"} {
		cur = filepath.Join(cur, seg)
		info, err := os.Lstat(cur)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", nil, false
		}
	}
	p := filepath.Join(cur, "transcript.jsonl")
	info, err := os.Lstat(p)
	if err != nil || !info.Mode().IsRegular() {
		return "", nil, false
	}
	return p, info, true
}

func readFileMentionsRoot(path, root string) (string, bool) {
	if root == "" {
		return "", false
	}
	f, err := trail.OpenRegular(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 256<<10))
	if err != nil {
		return "", false
	}
	s := string(b)
	return s, textMentionsPath(s, root)
}

func textMentionsPath(text, root string) bool {
	clean := filepath.Clean(root)
	if clean != "/" {
		clean = strings.TrimRight(clean, "/")
	}
	if clean == "" {
		return false
	}
	for start := 0; start < len(text); {
		idx := strings.Index(text[start:], clean)
		if idx < 0 {
			return false
		}
		pos := start + idx
		end := pos + len(clean)
		if pathBoundaryBefore(text, pos) && pathBoundaryAfter(text, end, clean) {
			return true
		}
		start = pos + 1
	}
	return false
}

func pathBoundaryBefore(s string, pos int) bool {
	if pos == 0 {
		return true
	}
	b := s[pos-1]
	if b == '/' {
		return pos >= 3 && s[pos-3:pos] == "://"
	}
	return !isPathWordByte(b)
}

func pathBoundaryAfter(s string, end int, clean string) bool {
	if end == len(s) {
		return true
	}
	b := s[end]
	return b == '/' || clean == "/" || !isPathWordByte(b)
}

func isPathWordByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '.' || b == '-'
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
	s, closeSnap := r.sweepMetricsSnapshot()
	if s == nil {
		return nil, nil
	}
	defer closeSnap()
	bySess, order, err := s.ProjectVerbs(root, 100)
	if err != nil {
		return nil, nil
	}
	return bySess, order
}

func (r *run) missingSkillProposal(root string) (string, int) {
	recs, err := r.sweepOpenProposals()
	if err != nil {
		return "", 0
	}
	var ids []string
	for _, rec := range recs {
		if rec.Project == "" || !trail.SameDir(rec.Project, root) {
			continue
		}
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
