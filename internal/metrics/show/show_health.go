package show

import (
	"path/filepath"
	"strings"
	"time"

	r "github.com/kesokaj/fyr/internal/metrics/report"
	"github.com/kesokaj/fyr/internal/models"
	"github.com/kesokaj/fyr/internal/search"
)

const (
	// AuditSubagentCap is the tool-call budget for audit/unknown subagent roles.
	AuditSubagentCap = 40
	// BuilderSubagentCap is the tool-call budget for builder/coder subagent roles.
	BuilderSubagentCap = 120
)

// CollectStepHealth computes context-knee hits, over-budget subagents, hook denials, and orchestration counts.
func CollectStepHealth(h *r.HealthSignals, sum *Summary, all []Sess, stepsByID map[string][]Stp, home string) map[string]bool {
	h.MainSessions = sum.Sessions["agy"]
	modelByID := make(map[string]string, len(all))
	for _, x := range all {
		modelByID[x.ID] = x.Model
	}
	overrides := models.ContextWindowOverrides(home)
	for _, p := range sum.B.SessionPeaks["agy"] {
		if knee := models.ContextKneeWithOverrides(modelByID[p.Session], overrides); knee > 0 && p.Peak >= knee {
			h.SessionsOverKnee++
		}
	}
	overBudget, seen := map[string]bool{}, map[string]bool{}
	for _, x := range all {
		if x.Vendor == Historical {
			continue
		}
		subTools := countSessionStepHealth(h, x, stepsByID[x.ID], seen)
		if x.Parent != "" && subTools > SubagentCapForRole(x.Agent) {
			overBudget[x.ID] = true
		}
	}
	h.OrchestrationCalls = h.SubagentDispatches + h.WorkflowCalls + h.SwarmCalls
	return overBudget
}

// SubagentCapForRole returns the per-subagent tool-call budget cap aligned with
// internal/search/hook_watchdog.go: 40 for audit/unknown roles, 120 for builders.
func SubagentCapForRole(agent string) int {
	if strings.TrimSpace(agent) == "" || search.IsAuditSubagentRole(agent) {
		return AuditSubagentCap
	}
	return BuilderSubagentCap
}

func countSessionStepHealth(h *r.HealthSignals, x Sess, steps []Stp, seen map[string]bool) int {
	subTools := 0
	for _, st := range steps {
		if st.Kind == "tool" && x.Parent != "" && st.Tool != "" && !IsExemptSubagentStep(st) {
			subTools++
		}
		if isReplayedStep(x, st, seen) {
			continue
		}
		switch st.Kind {
		case "compaction":
			h.StepCompactions++
		case "tool":
			classifyToolHealth(h, st)
		}
	}
	return subTools
}

// IsExemptSubagentStep reports whether a subagent tool call is exempt from budget caps.
func IsExemptSubagentStep(st Stp) bool {
	if st.Denied {
		return true
	}
	lowTool := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(st.Tool)), "agy:")
	switch {
	case lowTool == "finish" || lowTool == "send_message" || lowTool == "sendmessage":
		return true
	case lowTool == "bash" || lowTool == "run_command":
		return isExemptSubagentBashStep(st.Verbs)
	case isWriteOrEditTool(lowTool):
		return isExemptScratchAgentTarget(st.KindOf, st.Hash)
	default:
		return false
	}
}

func isWriteOrEditTool(lowTool string) bool {
	switch lowTool {
	case "write", "edit", "write_file", "edit_file", "write_to_file", "replace_file_content", "multi_replace_file_content":
		return true
	}
	return false
}

func isExemptScratchAgentTarget(kindOf, hash string) bool {
	switch strings.ToLower(strings.TrimSpace(kindOf)) {
	case "scratch_agent", "agent_scratch", "scratch_artifact", "agent_artifact", "scratch":
		return true
	}
	for _, raw := range []string{kindOf, hash} {
		if strings.Contains(strings.ToLower(filepath.ToSlash(strings.TrimSpace(raw))), ".agents/scratch/agents/") {
			return true
		}
	}
	return false
}

func isExemptSubagentBashStep(verbs []string) bool {
	if len(verbs) == 0 {
		return false
	}
	for _, v := range verbs {
		if !isExemptWrapUpVerb(v) {
			return false
		}
	}
	return true
}

func isExemptWrapUpVerb(verb string) bool {
	fields := strings.Fields(strings.TrimSpace(verb))
	if len(fields) < 2 {
		return false
	}
	switch fields[0] {
	case "git":
		switch fields[1] {
		case "status", "diff", "add", "commit":
			return true
		case "checkout":
			for _, f := range fields[2:] {
				if f == "--detach" {
					return true
				}
			}
		}
	case "fyr":
		return fields[1] == "git-check"
	}
	return false
}

func classifyToolHealth(h *r.HealthSignals, st Stp) {
	if st.Denied {
		h.HookDenials++
		switch st.DenialKind {
		case "clamped":
			h.ClampedSaves++
		case "ledger":
			h.HookBlocksLedger++
		default:
			h.HookBlocksGuards++
		}
	}
	switch {
	case st.Tool == "Agent" || st.Tool == "invoke_subagent" || st.Tool == "agy:invoke_subagent":
		h.SubagentDispatches++
	case st.Tool == "agy:run_workflow" || st.Tool == "run_workflow" || strings.HasSuffix(st.Tool, ":run_workflow"):
		h.WorkflowCalls++
	case st.Tool == "agy:swarm" || st.Tool == "swarm" || strings.HasSuffix(st.Tool, ":swarm"):
		h.SwarmCalls++
	}
	for _, verb := range st.Verbs {
		for _, gate := range []string{"fyr verify", "fyr gate", "fyr land", "fyr close"} {
			if verb == gate || strings.HasPrefix(verb, gate+" ") {
				h.DeterministicGates++
				break
			}
		}
	}
}

func (d Deps) populateModelFamilyHealth(h *r.HealthSignals, o Options, since, now time.Time) error {
	where, args := ShowWhere(o, since, now)
	rows, err := d.DB.Query(`SELECT id, COALESCE(parent_id,''), COALESCE(agent_type,''), COALESCE(model,'') FROM sessions WHERE `+where+` AND vendor = 'agy'`, args...)
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
		if fam := ClassifyModelFamily(model); fam != "" {
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
			h.ModelFamilyShare[fam] = r.Round4(float64(c) * 100.0 / float64(total))
		}
	}
	return nil
}

// ClassifyModelFamily buckets a model ID into "gemini", "anthropic", "other", or "".
func ClassifyModelFamily(model string) string {
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

func (d Deps) inspectProjectSearchDBs(sum *Summary, home string, o Options, all []Sess, overBudget map[string]bool) {
	roots := DistinctProjectRoots(o, home, all, d.Roots.IsSandboxProjectRoot)
	knownMain, knownSub := indexSessionsByID(all)
	var acc structuralAcc
	for _, root := range roots {
		db, searchDBPath, cleanup, ok := r.OpenCopySQLiteFileWithPath(home, root, ".agents", "scratch", "search.db")
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
		a.out.HiddenCouplingRate = r.Round4(a.hcRateSum / a.weightSum)
		a.out.GodNodeChurnRate = r.Round4(a.churnSum / a.weightSum)
		a.out.HubConcentration = r.Round4(a.hubSum / a.weightSum)
	}
	return a.out
}
