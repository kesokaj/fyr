package metrics

import (
	"strings"
	"time"
)

func collectStepHealth(h *HealthSignals, sum *Summary, all []sess, stepsByID map[string][]stp) map[string]bool {
	h.MainSessions = sum.Sessions["agy"]
	for _, p := range sum.B.SessionPeaks["agy"] {
		if p.Peak >= ContextKneeTokens {
			h.SessionsOverKnee++
		}
	}
	overBudget, seen := map[string]bool{}, map[string]bool{}
	for _, x := range all {
		if x.vendor == historical {
			continue
		}
		subTools := countSessionStepHealth(h, x, stepsByID[x.id], seen)
		if x.parent != "" {
			capLimit := 40
			if strings.EqualFold(x.agent, "self") {
				capLimit = 120
			}
			if subTools > capLimit {
				overBudget[x.id] = true
			}
		}
	}
	h.OrchestrationCalls = h.SubagentDispatches + h.WorkflowCalls + h.SwarmCalls
	return overBudget
}

func countSessionStepHealth(h *HealthSignals, x sess, steps []stp, seen map[string]bool) int {
	subTools := 0
	for _, st := range steps {
		if st.kind == "tool" && x.parent != "" && st.tool != "" {
			subTools++
		}
		if st.kind == "tool" || st.kind == "assistant" || st.kind == "generation" {
			key := x.vendor + "\x00" + x.label + "\x00" + st.ext
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		if st.kind == "compaction" {
			h.StepCompactions++
		} else if st.kind == "tool" {
			classifyToolHealth(h, st)
		}
	}
	return subTools
}

func classifyToolHealth(h *HealthSignals, st stp) {
	if st.denied {
		h.HookDenials++
		switch st.denialKind {
		case "clamped":
			h.ClampedSaves++
		case "ledger":
			h.HookBlocksLedger++
		default:
			h.HookBlocksGuards++
		}
	}
	switch {
	case st.tool == "Agent" || st.tool == "invoke_subagent" || st.tool == "agy:invoke_subagent":
		h.SubagentDispatches++
	case st.tool == "agy:run_workflow" || st.tool == "run_workflow" || strings.HasSuffix(st.tool, ":run_workflow"):
		h.WorkflowCalls++
	case st.tool == "agy:swarm" || st.tool == "swarm" || strings.HasSuffix(st.tool, ":swarm"):
		h.SwarmCalls++
	}
	for _, verb := range st.verbs {
		for _, gate := range []string{"fyr verify", "fyr gate", "fyr land", "fyr close"} {
			if verb == gate || strings.HasPrefix(verb, gate+" ") {
				h.DeterministicGates++
				break
			}
		}
	}
}

func (s *Store) populateModelFamilyHealth(h *HealthSignals, o ShowOptions, since, now time.Time) error {
	where, args := showWhere(o, since, now)
	rows, err := s.db.Query(`SELECT id, COALESCE(parent_id,''), COALESCE(agent_type,''), COALESCE(model,'') FROM sessions WHERE `+where+` AND vendor = 'agy'`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	counts, total := map[string]int{}, 0
	for rows.Next() {
		var id, parent, agentType, model string
		if err := rows.Scan(&id, &parent, &agentType, &model); err != nil {
			return err
		}
		if fam := classifyModelFamily(model); fam != "" {
			counts[fam]++
			total++
		}
		if parent != "" && isGuardedSubagentRole(agentType) && isDowngradedModel(model) {
			h.GuardedDowngrades++
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if total > 0 {
		h.ModelFamilyCounts, h.ModelFamilyShare = counts, map[string]float64{}
		for fam, c := range counts {
			h.ModelFamilyShare[fam] = round4(float64(c) * 100.0 / float64(total))
		}
	}
	return nil
}

func classifyModelFamily(model string) string {
	low := strings.ToLower(strings.TrimSpace(model))
	if low == "" {
		return ""
	}
	switch {
	case strings.Contains(low, "gemini"):
		return "gemini"
	case strings.Contains(low, "claude") || strings.Contains(low, "anthropic") || strings.Contains(low, "opus") || strings.Contains(low, "sonnet") || strings.Contains(low, "haiku"):
		return "anthropic"
	default:
		return "other"
	}
}

func isGuardedSubagentRole(agentType string) bool {
	switch strings.ToLower(strings.TrimSpace(agentType)) {
	case "reviewer", "security", "verifier", "dreamer":
		return true
	default:
		return false
	}
}

func isDowngradedModel(model string) bool {
	low := strings.ToLower(strings.TrimSpace(model))
	return low != "" && (strings.Contains(low, "flash") || strings.Contains(low, "-low"))
}
