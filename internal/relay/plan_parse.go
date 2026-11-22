package relay

import (
	"fmt"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/gate"
)

// Phase describes one parsed phase section from a PLAN-* record.
type Phase struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Deps      []string `json:"deps,omitempty"`
	Files     []string `json:"files,omitempty"`
	Verify    string   `json:"verify,omitempty"`
	Judge     string   `json:"judge,omitempty"`
	Model     string   `json:"model,omitempty"`
	OpenTasks []string `json:"openTasks,omitempty"`
	DoneTasks []string `json:"doneTasks,omitempty"`
	OpenLines []int    `json:"-"`
}

// ParsedPlan is a plan record fetched from brain.db with parsed phase sections.
type ParsedPlan struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Status string  `json:"status"`
	Body   string  `json:"-"`
	Phases []Phase `json:"phases"`
}

// LoadPlan fetches planID from brain.db, refusing missing, non-plan, or invalid closed plans.
func LoadPlan(home, dir, planID string) (ParsedPlan, error) {
	id := strings.TrimSpace(planID)
	if id == "" {
		return ParsedPlan{}, refusef("missing --plan ID")
	}
	recs, _, err := brain.Get(brain.Options{Home: home, Dir: dir}, []string{id})
	if err != nil {
		if brain.IsRefusal(err) {
			return ParsedPlan{}, refusef("plan %s not found: %v", id, err)
		}
		return ParsedPlan{}, fmt.Errorf("load plan %s: %w", id, err)
	}
	if len(recs) == 0 {
		return ParsedPlan{}, refusef("plan %s not found", id)
	}
	if recs[0].Kind != "plan" {
		return ParsedPlan{}, refusef("record %s is kind %q, want plan", recs[0].ID, recs[0].Kind)
	}
	parsed, err := ParsePlanBody(recs[0].ID, recs[0].Title, recs[0].Status, recs[0].Body)
	if err != nil {
		return ParsedPlan{}, err
	}
	if len(parsed.Phases) == 0 {
		if parsed.Status == "closed" {
			return ParsedPlan{}, refusef("plan %s is closed and has no phases", parsed.ID)
		}
		return ParsedPlan{}, refusef("plan %s contains no parseable Phase sections", parsed.ID)
	}
	for _, ph := range parsed.Phases {
		if len(ph.OpenTasks) > 0 && parsed.Status != "open" {
			return ParsedPlan{}, refusef("plan %s has status %q (must be open)", parsed.ID, parsed.Status)
		}
	}
	return parsed, nil
}

// ParsePlanBody parses `##`/`###`/`#### Phase ...` sections, checkboxes,
// `Files:` leases, and optional `Verify:`/`Judge:`/`Model:` lines outside code fences.
func ParsePlanBody(id, title, status, body string) (ParsedPlan, error) {
	var phases []Phase
	inFence, inFiles, cur := false, false, -1
	for i, raw := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if hdr, ok := parseMarkdownHeading(trimmed); ok {
			inFiles, cur = false, -1
			if strings.HasPrefix(strings.ToLower(hdr), "phase ") {
				phases = append(phases, buildPhaseFromHeading(hdr))
				cur = len(phases) - 1
			}
			continue
		}
		if cur < 0 {
			continue
		}
		var err error
		if inFiles, err = consumePhaseLine(&phases[cur], i, trimmed, inFiles); err != nil {
			return ParsedPlan{}, fmt.Errorf("plan %s phase %q: %w", id, phases[cur].Title, err)
		}
	}
	return ParsedPlan{ID: id, Title: title, Status: status, Body: body, Phases: phases}, nil
}

func parseMarkdownHeading(trimmed string) (string, bool) {
	h := 0
	for h < len(trimmed) && trimmed[h] == '#' {
		h++
	}
	if h < 2 || h > 4 || h >= len(trimmed) || trimmed[h] != ' ' {
		return "", false
	}
	return strings.TrimSpace(trimmed[h+1:]), true
}

func buildPhaseFromHeading(hdr string) Phase {
	title := strings.TrimSpace(hdr)
	var deps []string
	if idx := strings.LastIndex(title, "("); idx > 0 && strings.HasSuffix(title, ")") {
		inner := strings.TrimSpace(title[idx+1 : len(title)-1])
		if strings.HasPrefix(strings.ToLower(inner), "after:") {
			for _, part := range strings.Split(strings.TrimSpace(inner[len("after:"):]), ",") {
				if dep := normalizePhaseID(part); dep != "" {
					deps = append(deps, dep)
				}
			}
		}
	}
	return Phase{ID: normalizePhaseID(title), Title: title, Deps: deps}
}

func normalizePhaseID(s string) string {
	s = strings.TrimSpace(s)
	if low := strings.ToLower(s); low == "" || low == "none" || low == "-" {
		return ""
	}
	fields := strings.Fields(s)
	if len(fields) >= 2 && strings.EqualFold(fields[0], "phase") {
		return "Phase " + strings.TrimRight(fields[1], ":—-")
	}
	if len(fields) == 1 {
		return "Phase " + fields[0]
	}
	return s
}

func consumePhaseLine(ph *Phase, lineIdx int, trimmed string, inFiles bool) (bool, error) {
	if trimmed == "" || trimmed == "---" {
		return false, nil
	}
	if task, open, checked := parseCheckboxLine(trimmed); open || checked {
		if open {
			ph.OpenTasks, ph.OpenLines = append(ph.OpenTasks, task), append(ph.OpenLines, lineIdx)
		} else {
			ph.DoneTasks = append(ph.DoneTasks, task)
		}
		return false, nil
	}
	if key, val, ok := parseLabeledLine(trimmed); ok {
		return applyLabeledPhaseLine(ph, key, val)
	}
	if inFiles {
		if item, ok := stripListMarker(trimmed); ok {
			pats, err := gate.ParseLease(item)
			if err != nil {
				return false, err
			}
			ph.Files = append(ph.Files, pats...)
			return true, nil
		}
	}
	return false, nil
}

func applyLabeledPhaseLine(ph *Phase, key, val string) (bool, error) {
	switch key {
	case "files", "lease":
		if val == "" {
			return true, nil
		}
		pats, err := gate.ParseLease("Files: " + val)
		if err != nil {
			return false, err
		}
		ph.Files = append(ph.Files, pats...)
		return true, nil
	case "verify":
		ph.Verify = strings.Trim(strings.TrimSpace(val), "`")
	case "judge":
		ph.Judge = strings.TrimSpace(val)
	case "model":
		ph.Model = strings.Trim(strings.TrimSpace(val), "`")
	}
	return false, nil
}

func parseCheckboxLine(trimmed string) (text string, open, checked bool) {
	for _, p := range []string{"- [ ] ", "* [ ] "} {
		if rest, ok := strings.CutPrefix(trimmed, p); ok {
			return strings.TrimSpace(rest), true, false
		}
	}
	for _, p := range []string{"- [x] ", "- [X] ", "* [x] ", "* [X] "} {
		if rest, ok := strings.CutPrefix(trimmed, p); ok {
			return strings.TrimSpace(rest), false, true
		}
	}
	return "", false, false
}

func parseLabeledLine(trimmed string) (key, val string, ok bool) {
	s, _ := stripListMarker(trimmed)
	for _, k := range []string{"files", "lease", "verify", "judge", "model"} {
		for _, prefix := range []string{k + ":", "**" + k + ":**", "**" + k + "**:", "`" + k + ":`", "`" + k + "`:"} {
			if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
				return k, strings.TrimSpace(s[len(prefix):]), true
			}
		}
	}
	return "", "", false
}

func stripListMarker(s string) (string, bool) {
	for _, p := range []string{"- ", "* "} {
		if rest, ok := strings.CutPrefix(s, p); ok {
			return strings.TrimSpace(rest), true
		}
	}
	return s, false
}

// NextReadyPhase returns the first phase with >= 1 unchecked box whose explicit
// dependencies are complete, or ok=false if all phases are checked.
func NextReadyPhase(p ParsedPlan) (Phase, bool, error) {
	done, anyOpen := map[string]bool{}, false
	for _, ph := range p.Phases {
		isDone := len(ph.OpenTasks) == 0
		anyOpen = anyOpen || !isDone
		done[ph.ID], done[ph.Title] = isDone, isDone
	}
	if !anyOpen {
		return Phase{}, false, nil
	}
	for _, ph := range p.Phases {
		if len(ph.OpenTasks) > 0 && phaseDepsDone(ph.Deps, done) {
			return ph, true, nil
		}
	}
	return Phase{}, false, haltf("no unblocked ready phase in %s (unsatisfied dependencies)", p.ID)
}

func phaseDepsDone(deps []string, done map[string]bool) bool {
	for _, d := range deps {
		if !done[d] {
			return false
		}
	}
	return true
}

// UpcomingOpenPhases returns up to maxCount open phases in order for dry-run previews.
func UpcomingOpenPhases(p ParsedPlan, maxCount int) []Phase {
	var out []Phase
	for _, ph := range p.Phases {
		if len(ph.OpenTasks) > 0 {
			out = append(out, ph)
			if maxCount > 0 && len(out) >= maxCount {
				break
			}
		}
	}
	return out
}

// TickPhaseBoxes marks phase's recorded unchecked checkbox lines as `- [x]`.
func TickPhaseBoxes(body string, ph Phase) string {
	if len(ph.OpenLines) == 0 {
		return body
	}
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	for _, idx := range ph.OpenLines {
		if idx < 0 || idx >= len(lines) {
			continue
		}
		for _, pair := range [][2]string{{"- [ ] ", "- [x] "}, {"* [ ] ", "* [x] "}} {
			if pos := strings.Index(lines[idx], pair[0]); pos >= 0 && strings.TrimSpace(lines[idx][:pos]) == "" {
				lines[idx] = lines[idx][:pos] + pair[1] + lines[idx][pos+len(pair[0]):]
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}
