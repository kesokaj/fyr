package metrics

import (
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/models"
	"github.com/kesokaj/fyr/internal/search"
)

func collectStepHealth(h *HealthSignals, sum *Summary, all []sess, stepsByID map[string][]stp, home string) map[string]bool {
	h.MainSessions = sum.Sessions["agy"]
	modelByID := make(map[string]string, len(all))
	for _, x := range all {
		modelByID[x.id] = x.model
	}
	overrides := models.ContextWindowOverrides(home)
	for _, p := range sum.B.SessionPeaks["agy"] {
		if knee := models.ContextKneeWithOverrides(modelByID[p.Session], overrides); knee > 0 && p.Peak >= knee {
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

func inspectProjectSearchDBs(sum *Summary, home string, o ShowOptions, all []sess, overBudget map[string]bool) {
	roots := distinctProjectRoots(o, home, all)
	knownMain, knownSub := indexSessionsByID(all)
	var acc structuralAcc
	for _, root := range roots {
		db, searchDBPath, cleanup, ok := openCopySQLiteFileWithPath(home, root, ".agents", "scratch", "search.db")
		if !ok {
			continue
		}
		sum.Health.SearchDBCompactions += querySearchDBSessionMeta(db, knownMain, knownSub, overBudget)
		if sig, err := search.ReadStructuralSignals(searchDBPath, root); err == nil {
			acc.add(sig)
		}
		cleanup()
	}
	sum.Health.Structural = acc.finish()
}

type structuralAcc struct {
	out                         search.StructuralSignals
	weightSum                   float64
	hcRateSum, churnSum, hubSum float64
	count                       int
	first                       search.StructuralSignals
}

func (a *structuralAcc) add(s search.StructuralSignals) {
	if s.TotalCodeFiles == 0 && s.TotalProdPkgs == 0 && s.HiddenCouplingPairs == 0 &&
		s.HiddenCouplingEditPairs == 0 && s.HiddenCouplingReadPairs == 0 &&
		s.GodNodeClampHits == 0 && s.GodNodeRepeatReads == 0 && s.ProdPkgCycles == 0 {
		return
	}
	if a.count == 0 {
		a.first = s
	}
	a.count++
	a.out.HiddenCouplingPairs += s.HiddenCouplingPairs
	a.out.HiddenCouplingEditPairs += s.HiddenCouplingEditPairs
	a.out.HiddenCouplingReadPairs += s.HiddenCouplingReadPairs
	a.out.GodNodeClampHits += s.GodNodeClampHits
	a.out.GodNodeRepeatReads += s.GodNodeRepeatReads
	a.out.ProdPkgCycles += s.ProdPkgCycles
	a.out.LargeFilesOver350 += s.LargeFilesOver350
	a.out.TotalCodeFiles += s.TotalCodeFiles
	a.out.TotalProdPkgs += s.TotalProdPkgs
	w := float64(max(1, s.TotalCodeFiles))
	a.weightSum += w
	a.hcRateSum += s.HiddenCouplingRate * w
	a.churnSum += s.GodNodeChurnRate * w
	a.hubSum += s.HubConcentration * w
}

func (a *structuralAcc) finish() search.StructuralSignals {
	if a.count == 0 {
		return search.StructuralSignals{}
	}
	if a.count == 1 {
		return a.first
	}
	if a.weightSum > 0 {
		a.out.HiddenCouplingRate = round4(a.hcRateSum / a.weightSum)
		a.out.GodNodeChurnRate = round4(a.churnSum / a.weightSum)
		a.out.HubConcentration = round4(a.hubSum / a.weightSum)
	}
	return a.out
}
