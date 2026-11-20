package models

import (
	"math"
	"strings"
)

const (
	PriorStrength   = 4.0
	UCBExploreCoeff = 0.15
	UCBExploreCap   = 0.25
)

type ScoreBreakdown struct {
	PriorMean        float64 `json:"prior_mean"`
	PosteriorMean    float64 `json:"posterior_mean"`
	ExploreBonus     float64 `json:"explore_bonus"`
	HeadroomPenalty  float64 `json:"headroom_penalty"`
	RankScore        float64 `json:"rank_score"`
	DecayedRuns      float64 `json:"decayed_runs"`
	DecayedSuccesses float64 `json:"decayed_successes"`
	HeadroomPct      int     `json:"headroom_pct,omitempty"`
}

// ScoreCandidate computes the Empirical-Bayes + UCB1 + headroom rank score for entry.
func ScoreCandidate(entry CatalogEntry, role, tier string, totalRoleSamples float64, pools []Pool) ScoreBreakdown {
	ApplyModelProfile(&entry)
	nr, nt, err := NormalizeRoleTier(role, tier)
	if err != nil {
		nr, nt = nonEmpty(strings.ToLower(strings.TrimSpace(role)), "builder"), nonEmpty(strings.ToLower(strings.TrimSpace(tier)), "pro")
	}
	prior := RoleBaselineForTier(nr, nt, entry.Tier, entry.Effort)
	nEff, succEff := EffectiveEvidence(entry.Evidence)
	post := (succEff + PriorStrength*prior) / (nEff + PriorStrength)
	explore := ExploreBonus(totalRoleSamples, nEff)
	pen, pct := HeadroomPenalty(entry, pools)
	out := ScoreBreakdown{
		PriorMean: roundScore(prior), PosteriorMean: roundScore(post),
		ExploreBonus: roundScore(explore), HeadroomPenalty: roundScore(pen),
		RankScore:   roundScore(post + explore - pen),
		DecayedRuns: roundScore(nEff), DecayedSuccesses: roundScore(succEff),
	}
	if pct >= 0 {
		out.HeadroomPct = pct
	}
	return out
}

func RoleBaselineForTier(role, wantTier, candTier, effort string) float64 {
	ct, wt := strings.ToLower(strings.TrimSpace(candTier)), strings.ToLower(strings.TrimSpace(wantTier))
	base := map[string]float64{"pro": 0.65, "flash": 0.55, "light": 0.45}[ct]
	if base == 0 {
		base = 0.65
	}
	if wt == "light" {
		if ct == "flash" {
			base -= 0.12
		} else if ct == "pro" || ct == "" {
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
	return min(UCBExploreCap, UCBExploreCoeff*math.Sqrt(math.Log(max(totalRoleSamples, 0)+math.E)/(max(decayedN, 0)+1.0)))
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

func HeadroomPenaltyForPct(pct int) float64 {
	if pct < 0 || pct >= 50 {
		return 0.0
	}
	if pct < 20 {
		return 0.50
	}
	return 0.15 * float64(50-pct) / 30.0
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
