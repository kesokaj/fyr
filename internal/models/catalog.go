package models

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ModelEvidence holds empirical run counts and pass rates for one model ID.
type ModelEvidence struct {
	WorkRuns         int     `json:"work_runs"`
	WorkPass         int     `json:"work_pass"`
	WorkMeanSeconds  float64 `json:"work_mean_seconds"`
	EvalRuns         int     `json:"eval_runs"`
	EvalPass         int     `json:"eval_pass"`
	TrialRuns        int     `json:"trial_runs"`
	TrialPass        int     `json:"trial_pass"`
	JudgeRuns        int     `json:"judge_runs"`
	JudgePass        int     `json:"judge_pass"`
	RatingRuns       int     `json:"rating_runs"`
	RatingScoreSum   float64 `json:"rating_score_sum"`
	DecayedRuns      float64 `json:"decayed_runs"`
	DecayedSuccesses float64 `json:"decayed_successes"`
}

// CatalogEntry describes one model tracked in .config/fyr/models.json.
type CatalogEntry struct {
	ID              string        `json:"id"`
	DisplayName     string        `json:"display_name"`
	Family          string        `json:"family"`
	Subfamily       string        `json:"subfamily,omitempty"`
	Tier            string        `json:"tier"`
	Effort          string        `json:"effort"`
	Mode            string        `json:"mode,omitempty"`
	Speed           string        `json:"speed,omitempty"`
	Summary         string        `json:"summary,omitempty"`
	BestFor         []string      `json:"best_for,omitempty"`
	AvoidFor        []string      `json:"avoid_for,omitempty"`
	InteractiveOnly bool          `json:"interactive_only,omitempty"`
	RetiringOn      string        `json:"retiring_on,omitempty"`
	ChangelogNote   string        `json:"changelog_note,omitempty"`
	Status          string        `json:"status"`
	FirstSeen       string        `json:"first_seen"`
	LastSeen        string        `json:"last_seen"`
	ContextWindow   int64         `json:"context_window,omitempty"`
	Evidence        ModelEvidence `json:"evidence"`

	Score *ScoreBreakdown `json:"score,omitempty"`
}

// RolePick records the selected active model and rationale for a role+tier.
type RolePick struct {
	Role           string `json:"role"`
	Tier           string `json:"tier"`
	Model          string `json:"model"`
	Reason         string `json:"reason"`
	FallbackReason string `json:"fallback_reason,omitempty"`

	PriorMean       float64 `json:"prior_mean"`
	PosteriorMean   float64 `json:"posterior_mean"`
	ExploreBonus    float64 `json:"explore_bonus"`
	HeadroomPenalty float64 `json:"headroom_penalty"`
	RankScore       float64 `json:"rank_score"`
	DecayedRuns     float64 `json:"decayed_runs"`
}

// Catalog is the persisted .config/fyr/models.json document.
type Catalog struct {
	UpdatedAt      string              `json:"updated_at"`
	QuotaUpdatedAt string              `json:"quota_updated_at,omitempty"`
	Entries        []CatalogEntry      `json:"entries"`
	Picks          map[string]RolePick `json:"picks"`
	Pools          []Pool              `json:"pools,omitempty"`
}

// StandardRoleTiers lists the default role+tier pairs persisted in Catalog.Picks.
var StandardRoleTiers = [][2]string{
	{"builder", "pro"},
	{"builder", "flash"},
	{"planner", "pro"},
	{"judge", "pro"},
	{"judge", "flash"},
	{"reviewer", "pro"},
	{"reviewer", "flash"},
	{"verifier", "pro"},
	{"verifier", "flash"},
	{"mechanical", "flash"},
}

// RefreshCatalog runs `agy models`, merges previous status/dates and empirical
// evidence, and atomically writes <home>/.config/fyr/models.json. On command or
// empty-list error, any existing models.json is left untouched.
func RefreshCatalog(ctx context.Context, home, agyPath string, extra map[string]ModelEvidence) (Catalog, error) {
	return refreshCatalogAt(ctx, home, agyPath, extra, time.Now().UTC())
}

func refreshCatalogAt(ctx context.Context, home, agyPath string, extra map[string]ModelEvidence, now time.Time) (Catalog, error) {
	if agyPath == "" {
		var err error
		agyPath, err = exec.LookPath("agy")
		if err != nil {
			return Catalog{}, errors.New("agy models: agy not found on PATH")
		}
	}
	raw, err := runAgyModelsForHome(ctx, home, agyPath)
	if err != nil {
		return Catalog{}, fmt.Errorf("agy models: %w", err)
	}
	parsed := ParseModelsOutput(raw)
	if len(parsed) == 0 {
		return Catalog{}, errors.New("agy models listed no model")
	}
	var prevPtr *Catalog
	if prev, ok, err := LoadCatalog(home); err == nil && ok {
		prevPtr = &prev
	}
	ev := AggregateWorkEvidence(home, extra)
	cat := BuildCatalog(now, parsed, prevPtr, ev)
	if home != "" {
		if err := SaveCatalog(home, cat); err != nil {
			return Catalog{}, err
		}
	}
	return cat, nil
}

// EnrichCatalogPools runs `agy -p "/usage"` best-effort (never failing the
// caller), updates QuotaUpdatedAt and Pools on cat, and saves cat when changed.
func EnrichCatalogPools(ctx context.Context, home, agyPath string, cat Catalog) Catalog {
	updated, _ := EnrichCatalogPoolsWithStatus(ctx, home, agyPath, cat)
	return updated
}

// EnrichCatalogPoolsWithStatus behaves like EnrichCatalogPools and also reports
// whether the live `/usage` probe succeeded.
func EnrichCatalogPoolsWithStatus(ctx context.Context, home, agyPath string, cat Catalog) (Catalog, bool) {
	if probeCatalogPoolsBestEffort(ctx, home, agyPath, usageNowFunc(), &cat) {
		populateEntryScores(cat.Entries, cat.Pools)
		cat.Picks = computeStandardPicks(cat.Entries, cat.Picks, cat.Pools)
		if home != "" {
			_ = SaveCatalog(home, cat)
		}
		return cat, true
	}
	return cat, false
}

func probeCatalogPoolsBestEffort(ctx context.Context, home, agyPath string, now time.Time, cat *Catalog) bool {
	if cat == nil || strings.TrimSpace(agyPath) == "" {
		return false
	}
	uText, err := probeUsageForHome(ctx, home, agyPath)
	if err != nil {
		return false
	}
	pools, err := ParseUsage(uText)
	if err != nil || len(pools) == 0 {
		return false
	}
	cat.QuotaUpdatedAt = now.UTC().Format(time.RFC3339)
	cat.Pools = pools
	return true
}

// BuildCatalog merges freshly parsed entries with optional previous state and evidence.
func BuildCatalog(now time.Time, parsed []CatalogEntry, prev *Catalog, evidence map[string]ModelEvidence) Catalog {
	today := now.UTC().Format("2006-01-02")
	entries := mergeCatalogEntries(today, parsed, prev, evidence)
	var prevPicks map[string]RolePick
	var quotaUpdatedAt string
	var prevPools []Pool
	if prev != nil {
		prevPicks = prev.Picks
		quotaUpdatedAt = prev.QuotaUpdatedAt
		prevPools = append([]Pool(nil), prev.Pools...)
	}
	populateEntryScores(entries, prevPools)
	picks := computeStandardPicks(entries, prevPicks, prevPools)
	return Catalog{
		UpdatedAt:      now.UTC().Format(time.RFC3339),
		QuotaUpdatedAt: quotaUpdatedAt,
		Entries:        entries,
		Picks:          picks,
		Pools:          prevPools,
	}
}

// RebuildCatalogWithEvidence refreshes evidence, scores, and picks on cat.
func RebuildCatalogWithEvidence(now time.Time, cat Catalog, evidence map[string]ModelEvidence) Catalog {
	entries := append([]CatalogEntry(nil), cat.Entries...)
	for i := range entries {
		ApplyModelProfile(&entries[i])
	}
	applyCatalogEvidence(entries, evidence)
	populateEntryScores(entries, cat.Pools)
	cat.Entries = entries
	cat.Picks = computeStandardPicks(entries, cat.Picks, cat.Pools)
	cat.UpdatedAt = now.UTC().Format(time.RFC3339)
	return cat
}

func computeStandardPicks(entries []CatalogEntry, prevPicks map[string]RolePick, pools []Pool) map[string]RolePick {
	picks := make(map[string]RolePick, len(StandardRoleTiers))
	for _, rt := range StandardRoleTiers {
		if p, err := PickFromEntriesWithPools(entries, prevPicks, pools, rt[0], rt[1]); err == nil {
			picks[rt[0]+":"+rt[1]] = p
		}
	}
	return picks
}

func mergeCatalogEntries(today string, parsed []CatalogEntry, prev *Catalog, evidence map[string]ModelEvidence) []CatalogEntry {
	prevByID := map[string]CatalogEntry{}
	hasPrev := prev != nil && len(prev.Entries) > 0
	if hasPrev {
		for _, e := range prev.Entries {
			prevByID[e.ID] = e
		}
	}
	seen := map[string]bool{}
	entries := make([]CatalogEntry, 0, len(parsed)+len(prevByID))
	for _, e := range parsed {
		seen[e.ID] = true
		ApplyModelProfile(&e)
		if old, ok := prevByID[e.ID]; ok {
			e.Status = "active"
			e.FirstSeen = nonEmpty(old.FirstSeen, today)
			e.ChangelogNote = nonEmpty(e.ChangelogNote, old.ChangelogNote)
			e.RetiringOn = nonEmpty(e.RetiringOn, old.RetiringOn)
			if e.ContextWindow <= 0 {
				e.ContextWindow = old.ContextWindow
			}
		} else if hasPrev {
			e.Status = "new"
			e.FirstSeen = today
		} else {
			e.Status = "active"
			e.FirstSeen = today
		}
		e.LastSeen = today
		entries = append(entries, e)
	}
	if hasPrev {
		for _, old := range prev.Entries {
			if seen[old.ID] {
				continue
			}
			ApplyModelProfile(&old)
			old.Status = "gone"
			entries = append(entries, old)
		}
	}
	applyCatalogEvidence(entries, evidence)
	return entries
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// NormalizeRoleTier normalizes role and tier names for catalog lookups.
func NormalizeRoleTier(role, tier string) (normRole, normTier string, err error) {
	normRole = strings.ToLower(strings.TrimSpace(role))
	if normRole == "" || normRole == "worker" {
		normRole = "builder"
	}
	switch normRole {
	case "builder", "planner", "judge", "reviewer", "verifier", "mechanical":
	default:
		return "", "", fmt.Errorf("unknown role %q (want builder, planner, judge, reviewer, verifier or mechanical)", role)
	}
	rawTier := strings.ToLower(strings.Trim(strings.TrimSpace(tier), "[]"))
	switch rawTier {
	case "":
		if normRole == "mechanical" {
			normTier = "flash"
		} else {
			normTier = "pro"
		}
	case "pro", "opus":
		normTier = "pro"
	case "flash", "sonnet":
		normTier = "flash"
	case "light", "lite", "haiku":
		normTier = "light"
	default:
		return "", "", fmt.Errorf("unknown tier %q (want pro, flash or light)", tier)
	}
	return normRole, normTier, nil
}

// PickFromCatalog selects the highest-ranked active model in cat for role+tier.
func PickFromCatalog(cat Catalog, role, tier string) (RolePick, error) {
	return PickFromEntriesWithPools(cat.Entries, cat.Picks, cat.Pools, role, tier)
}

// PickFromEntries chooses the highest-ranked active model in entries for role+tier.
func PickFromEntries(entries []CatalogEntry, prevPicks map[string]RolePick, role, tier string) (RolePick, error) {
	return PickFromEntriesWithPools(entries, prevPicks, nil, role, tier)
}

// PickFromEntriesWithPools chooses the highest-ranked active model in entries for role+tier.
func PickFromEntriesWithPools(entries []CatalogEntry, prevPicks map[string]RolePick, pools []Pool, role, tier string) (RolePick, error) {
	normRole, normTier, err := NormalizeRoleTier(role, tier)
	if err != nil {
		return RolePick{}, err
	}
	active := make([]CatalogEntry, 0, len(entries))
	eligible := make([]CatalogEntry, 0, len(entries))
	goneIDs := map[string]bool{}
	for _, e := range entries {
		ApplyModelProfile(&e)
		if e.Status == "gone" {
			goneIDs[e.ID] = true
			continue
		}
		active = append(active, e)
		if isAutoPickEligible(e) {
			eligible = append(eligible, e)
		}
	}
	if len(active) == 0 {
		return RolePick{}, errors.New("no active models in catalog")
	}
	if len(eligible) == 0 {
		eligible = active
	}
	prevModel := prevPicks[normRole+":"+normTier].Model
	if normRole == "judge" || normRole == "reviewer" || normRole == "verifier" {
		return pickCrossFamilyRole(eligible, goneIDs, pools, prevModel, normRole, normTier), nil
	}
	chosen, sc, reason, fb := pickGeminiOrAny(eligible, goneIDs, pools, prevModel, normRole, normTier)
	return buildRolePick(normRole, normTier, chosen.ID, reason, fb, sc), nil
}
