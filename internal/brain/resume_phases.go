package brain

import "strings"

type planPhaseNode struct {
	id        string
	label     string
	deps      []string
	openTasks int
}

// ReadyPhasesLine parses `### Phase ...` headings and `(after: ...)` dependencies
// in a plan body and returns a 1-line "Ready phases: ..." frontier summary.
func ReadyPhasesLine(body string) string {
	phases := parsePlanPhases(body)
	if len(phases) == 0 {
		return ""
	}
	done := map[string]bool{}
	for _, p := range phases {
		isDone := p.openTasks == 0
		done[p.id] = isDone
		done[p.label] = isDone
	}
	var ready []string
	anyOpen := false
	for _, p := range phases {
		if p.openTasks == 0 {
			continue
		}
		anyOpen = true
		unblocked := true
		for _, dep := range p.deps {
			if !done[dep] {
				unblocked = false
				break
			}
		}
		if unblocked {
			ready = append(ready, p.label)
		}
	}
	if len(ready) == 0 {
		if !anyOpen {
			return "Ready phases: (all complete)"
		}
		return "Ready phases: (none)"
	}
	return "Ready phases: " + strings.Join(ready, ", ")
}

func parsePlanPhases(body string) []planPhaseNode {
	var phases []planPhaseNode
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if strings.HasPrefix(trimmed, "### Phase ") {
			prevID := ""
			if len(phases) > 0 {
				prevID = phases[len(phases)-1].id
			}
			phases = append(phases, parsePhaseHeading(strings.TrimPrefix(trimmed, "### "), prevID))
			continue
		}
		if len(phases) > 0 && strings.HasPrefix(trimmed, "- [ ] ") {
			phases[len(phases)-1].openTasks++
		}
	}
	return phases
}

func parsePhaseHeading(hdr, prevID string) planPhaseNode {
	label := strings.TrimSpace(hdr)
	var deps []string
	hasAfter := false
	if idx := strings.LastIndex(label, "("); idx > 0 && strings.HasSuffix(label, ")") {
		inner := strings.TrimSpace(label[idx+1 : len(label)-1])
		label = strings.TrimSpace(label[:idx])
		if strings.HasPrefix(strings.ToLower(inner), "after:") {
			hasAfter = true
			rawDeps := strings.TrimSpace(inner[len("after:"):])
			for _, part := range strings.Split(rawDeps, ",") {
				d := normalizePhaseID(part)
				if d != "" {
					deps = append(deps, d)
				}
			}
		}
	}
	if !hasAfter && prevID != "" {
		deps = []string{prevID}
	}
	return planPhaseNode{id: normalizePhaseID(label), label: label, deps: deps}
}

func normalizePhaseID(s string) string {
	s = strings.TrimSpace(s)
	low := strings.ToLower(s)
	if low == "" || low == "none" || low == "-" {
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
