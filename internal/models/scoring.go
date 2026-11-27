package models

import (
	"math"
	"strings"
)

const (
	PriorStrength             = 4.0
	UCBExploreCoeff           = 0.04
	UCBExploreCap             = 0.06
	MaxEfficiencyBonus        = 0.08
	FamilyFailoverHeadroomPct = 5
)

type ScoreBreakdown struct {
	PriorMean        float64 `json:"prior_mean"`
	PosteriorMean    float64 `json:"posterior_mean"`
	ExploreBonus     float64 `json:"explore_bonus"`
	EfficiencyBonus  float64 `json:"efficiency_bonus,omitempty"`
	HeadroomPenalty  float64 `json:"headroom_penalty"`
	RankScore        float64 `json:"rank_score"`
	DecayedRuns      float64 `json:"decayed_runs"`
	DecayedSuccesses float64 `json:"decayed_successes"`
	HeadroomPct      int     `json:"headroom_pct,omitempty"`
}

// ScoreCandidate computes the hierarchical Empirical-Bayes + conservative UCB +
// Pareto efficiency - convex headroom rank score for entry.
func ScoreCandidate(entry CatalogEntry, role, tier string, totalRoleSamples float64, pools []Pool) ScoreBreakdown {
	ApplyModelProfile(&entry)
	nr, nt, err := NormalizeRoleTier(role, tier)
	if err != nil {
		nr, nt = nonEmpty(strings.ToLower(strings.TrimSpace(role)), "builder"), nonEmpty(strings.ToLower(strings.TrimSpace(tier)), "pro")
	}
	prior := RoleBaselineForTier(nr, nt, entry.Tier, entry.Effort)
	nGlobal, sGlobal := EffectiveEvidence(entry.Evidence)
	globalPost := (sGlobal + PriorStrength*prior) / (nGlobal + PriorStrength)
	post, nEff, succEff := rolePosterior(entry.Evidence, nr, globalPost, nGlobal, sGlobal)
	explore := ExploreBonus(totalRoleSamples, nEff)
	effBonus := EfficiencyBonus(entry.Evidence)
	pen, pct := HeadroomPenalty(entry, pools)
	out := ScoreBreakdown{
		PriorMean: roundScore(prior), PosteriorMean: roundScore(post),
		ExploreBonus: roundScore(explore), EfficiencyBonus: roundScore(effBonus),
		HeadroomPenalty: roundScore(pen),
		RankScore:       roundScore(post + explore + effBonus - pen),
		DecayedRuns:     roundScore(nEff), DecayedSuccesses: roundScore(succEff),
	}
	if pct >= 0 {
		out.HeadroomPct = pct
	}
	return out
}

func rolePosterior(ev ModelEvidence, normRole string, globalPost, nGlobal, sGlobal float64) (post, nEff, succEff float64) {
	re := ev.BuilderEvidence
	if IsCrossFamilyRole(normRole) {
		re = ev.JudgeEvidence
	}
	nRole, sRole := effectiveRoleEvidence(re)
	if nRole <= 0 {
		return globalPost, nGlobal, sGlobal
	}
	return (sRole + PriorStrength*globalPost) / (nRole + PriorStrength), nRole, sRole
}

func effectiveRoleEvidence(re RoleEvidence) (float64, float64) {
	if re.DecayedRuns > 0 {
		return re.DecayedRuns, min(max(re.DecayedSuccesses, 0), re.DecayedRuns)
	}
	if re.Runs > 0 {
		okCount := min(max(re.Pass, 0), re.Runs)
		return float64(re.Runs), float64(okCount)
	}
	return 0, 0
}

func RoleBaselineForTier(role, wantTier, candTier, effort string) float64 {
	ct, wt := strings.ToLower(strings.TrimSpace(candTier)), strings.ToLower(strings.TrimSpace(wantTier))
	base := map[string]float64{"pro": 0.65, "flash": 0.55, "light": 0.45}[ct]
	if base == 0 {
		base = 0.65
	}
	if wt == "light" {
		switch ct {
		case "flash":
			base -= 0.12
		case "pro", "":
			base -= 0.25
		}
	} else if (strings.EqualFold(role, "mechanical") || wt == "flash") && (ct == "pro" || ct == "") {
		base -= 0.22
	}
	base += map[string]float64{"xhigh": 0.02, "max": 0.02, "high": 0.01, "low": -0.01}[strings.ToLower(strings.TrimSpace(effort))]
	return min(max(base, 0.05), 0.95)
}

func EffectiveEvidence(ev ModelEvidence) (float64, float64) {
	if ev.DecayedRuns > 0 {
		return ev.DecayedRuns, min(max(ev.DecayedSuccesses, 0), ev.DecayedRuns)
	}
	wRuns, eRuns := max(ev.WorkRuns, 0), max(max(ev.EvalRuns, ev.TrialRuns), 0)
	wPass, ePass := min(max(ev.WorkPass, 0), wRuns), min(max(max(ev.EvalPass, ev.TrialPass), 0), eRuns)
	jRuns, rRuns := max(ev.JudgeRuns, 0), max(ev.RatingRuns, 0)
	n := float64(wRuns + eRuns + jRuns + rRuns)
	s := float64(wPass+ePass+min(max(ev.JudgePass, 0), jRuns)) + min(max(ev.RatingScoreSum, 0), float64(rRuns))
	return n, min(max(s, 0), n)
}

func TotalRoleSamples(candidates []CatalogEntry) float64 {
	total := 0.0
	for _, c := range candidates {
		n, _ := EffectiveEvidence(c.Evidence)
		total += n
	}
	return total
}

func ExploreBonus(totalRoleSamples, decayedN float64) float64 {
	n := max(decayedN, 0)
	raw := UCBExploreCoeff * math.Sqrt(math.Log(max(totalRoleSamples, 0)+math.E)/(n+1.0))
	if n < 5 {
		raw *= math.Sqrt(n / (n + 3.0))
	}
	return min(UCBExploreCap, raw)
}

// EfficiencyBonus returns a bounded Pareto efficiency bonus (<= MaxEfficiencyBonus)
// derived from MeanTokenEfficiency (1..5 scale) and latency seconds.
func EfficiencyBonus(ev ModelEvidence) float64 {
	tokBonus := 0.0
	if ev.MeanTokenEfficiency > 0 {
		q := min(max((ev.MeanTokenEfficiency-1.0)/4.0, 0), 1)
		tokBonus = 0.05 * q
	}
	latSec := ev.WorkMeanSeconds
	if latSec <= 0 {
		latSec = ev.MeanLatencySeconds
	}
	latBonus := 0.0
	if latSec > 0 {
		latBonus = 0.03 / (1.0 + latSec/60.0)
	}
	return min(MaxEfficiencyBonus, tokBonus+latBonus)
}

func HeadroomPenalty(entry CatalogEntry, pools []Pool) (float64, int) {
	if p, ok := FindPoolForModel(pools, entry.Family, entry.ID); ok {
		if pct, has := PoolHeadroomPct(p); has {
			return HeadroomPenaltyForPct(pct), pct
		}
	}
	return 0.0, DisabledWindow
}

func PoolHeadroomPct(p Pool) (int, bool) {
	if p.Five >= 0 && p.Week >= 0 {
		return min(p.Five, p.Week), true
	}
	if p.Five >= 0 {
		return p.Five, true
	}
	if p.Week >= 0 {
		return p.Week, true
	}
	return DisabledWindow, false
}

// HeadroomPenaltyForPct returns the smooth convex quota penalty ((50-pct)/50)^2
// for pct < 50 (1.0 at pct <= 0, 0.0 at pct >= 50 or DisabledWindow).
func HeadroomPenaltyForPct(pct int) float64 {
	if pct == DisabledWindow || pct >= 50 {
		return 0.0
	}
	if pct <= 0 {
		return 1.0
	}
	x := float64(50-pct) / 50.0
	return x * x
}

func FindPoolForModel(pools []Pool, family, modelID string) (Pool, bool) {
	fam := strings.ToLower(strings.TrimSpace(family))
	if fam == "" {
		fam, _, _ = ClassifyModel(modelID, "")
	}
	wantGem := isGeminiFamily(fam, modelID)
	wantAnth := isAnthropicFamily(fam) || strings.HasPrefix(strings.ToLower(strings.TrimSpace(modelID)), "claude")
	for _, p := range pools {
		low := strings.ToLower(p.Name)
		if (wantGem && (strings.Contains(low, "gemini") || strings.Contains(low, "google"))) ||
			(wantAnth && (strings.Contains(low, "claude") || strings.Contains(low, "anthropic"))) ||
			(!wantGem && !wantAnth && (strings.Contains(low, "gpt") || strings.Contains(low, "openai"))) {
			return p, true
		}
	}
	return Pool{}, false
}

func roundScore(v float64) float64 { return math.Round(v*10000) / 10000 }
