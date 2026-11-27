package dream

import (
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/trail"
)

type proposalMeta struct {
	normTitle string
	target    string
	kind      string
	file      string
	sessionID string
	signalID  string
	draftKey  string
}

type proposalIndex struct {
	harnessTitles map[string]bool
	projectTitles map[string][]string
	sessions      map[string]bool
	signals       map[string]bool
	skillTargets  map[string]bool
	drafts        map[string]bool
}

func newProposalIndex() *proposalIndex {
	return &proposalIndex{
		harnessTitles: map[string]bool{},
		projectTitles: map[string][]string{},
		sessions:      map[string]bool{},
		signals:       map[string]bool{},
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
		} else if rest, ok := strings.CutPrefix(ln, "Signal: "); ok {
			m.signalID = cleanSignalID(rest)
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
	if m.signalID != "" {
		idx.signals[m.signalID] = true
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

func (idx *proposalIndex) addSignalsFromText(text string) {
	for _, tok := range strings.FieldsFunc(text, isNotSignalRune) {
		if sig := cleanSignalID(tok); sig != "" {
			idx.signals[sig] = true
		}
	}
}

func (idx *proposalIndex) seen(project string, m proposalMeta) bool {
	if m.draftKey != "" {
		return idx.drafts[m.draftKey]
	}
	if m.sessionID != "" && idx.sessions[m.sessionID] {
		return true
	}
	if m.signalID != "" && idx.signals[m.signalID] {
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
	if err := loadExistingProposals(home, project, idx); err != nil {
		return nil, err
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

func loadExistingProposals(home, project string, idx *proposalIndex) error {
	if !brainDBExists(home) {
		return nil
	}
	recs, err := brain.List(brain.Options{Home: home, Global: true}, brain.ListFilter{Kind: "proposal"})
	if err != nil {
		return err
	}
	ctx := newAppliedDedupeContext(home, dedupeNow())
	for _, rec := range recs {
		m := parseProposalMeta(rec.Title, rec.Body)
		if rec.Status == "queued" || rec.Status == "open" {
			idx.addSignalsFromText(rec.Body)
			idx.add(rec.Project, m)
			continue
		}
		if isAppliedStatus(rec.Status) {
			idx.addApplied(rec, m, ctx)
		}
	}
	if project != "" && project != home {
		if openRecs, err := brain.List(brain.Options{Home: home, Dir: project}, brain.ListFilter{Open: true}); err == nil {
			for _, rec := range openRecs {
				idx.addSignalsFromText(rec.Body)
			}
		}
	}
	return nil
}
