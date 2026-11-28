package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/models"
	"github.com/kesokaj/fyr/internal/trail"
)

const bareExplainSentinel = "\x00all"

func modelsVerb() verb {
	return verb{flags: registerModelsFlags, run: runModels}
}

func registerModelsFlags(c *ctx) {
	c.fs.Bool("bench", false, "probe up to --max 0-run active models and record kind=bench trials")
	c.fs.String("caller-family", "", "with --pick: caller `FAMILY` (gemini, anthropic or other)")
	c.fs.StringVar(&c.modelsExplain, "explain", "", "print profile, score breakdown, and empirical stats for model `ID` (or all if omitted)")
	c.fs.BoolVar(&c.jsonOut, "json", false, "print the catalog, --pick, --explain or --bench result as JSON")
	c.fs.Int("max", models.DefaultBenchMax, "with --bench: probe at most `N` 0-run active models (default 3)")
	c.fs.String("model", "", "with --bench: probe only the active non-interactive model `ID`")
	c.fs.StringVar(&c.modelsPick, "pick", "", "pick one model for `ROLE` (builder, planner, judge, security, ...)")
	c.fs.BoolVar(&c.modelsRefresh, "refresh", false, "re-query agy models and update .config/fyr/models.json")
	c.fs.StringVar(&c.modelsTier, "tier", "", "with --pick: the tier `T`, pro, flash or light")
}

type modelsBenchFlags struct {
	bench, maxSet, modelSet, callerSet bool
	model, callerFamily                string
	max                                int
}

func parseAndValidateModels(c *ctx) (modelsBenchFlags, error) {
	bf := modelsBenchFlags{max: models.DefaultBenchMax}
	c.fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "bench":
			bf.bench = f.Value.String() == "true"
		case "caller-family":
			bf.callerFamily, bf.callerSet = f.Value.String(), true
		case "model":
			bf.model, bf.modelSet = f.Value.String(), true
		case "max":
			bf.maxSet = true
			bf.max, _ = strconv.Atoi(f.Value.String())
		}
	})
	if err := validateModelsFlags(c, bf); err != nil {
		return bf, err
	}
	return bf, nil
}

func validateModelsFlags(c *ctx, bf modelsBenchFlags) error {
	switch {
	case c.modelsTier != "" && c.modelsPick == "":
		return usageErr(c.out, "models", "fyr models: --tier requires --pick")
	case bf.callerSet && c.modelsPick == "":
		return usageErr(c.out, "models", "fyr models: --caller-family requires --pick")
	case c.modelsExplain != "" && c.modelsPick != "":
		return usageErr(c.out, "models", "fyr models: --explain and --pick cannot be combined")
	case bf.modelSet && !bf.bench:
		return usageErr(c.out, "models", "fyr models: --model requires --bench")
	case bf.maxSet && !bf.bench:
		return usageErr(c.out, "models", "fyr models: --max requires --bench")
	case bf.bench && (c.modelsPick != "" || c.modelsExplain != ""):
		return usageErr(c.out, "models", "fyr models: --bench cannot be combined with --pick or --explain")
	case bf.bench && bf.max < 1:
		return usageErr(c.out, "models", "fyr models: --max must be at least 1")
	}
	if c.modelsPick != "" {
		if _, _, err := models.NormalizeRoleTier(c.modelsPick, c.modelsTier); err != nil {
			return usageErr(c.out, "models", "fyr models: "+err.Error())
		}
		if _, err := models.NormalizeCallerFamily(bf.callerFamily); err != nil {
			return usageErr(c.out, "models", "fyr models: "+err.Error())
		}
	}
	return nil
}

func runModels(c *ctx) error {
	bf, err := parseAndValidateModels(c)
	if err != nil {
		return err
	}
	sig, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	agyPath, _ := dream.ResolveBinary("agy", c.home)
	cat, poolsProbed, err := loadOrRefreshCatalog(sig, c, agyPath)
	if err != nil {
		fmt.Fprintf(stderr, "models: %v\n", err)
		return exitCode(1)
	}
	if bf.bench {
		return runModelsBench(sig, c, cat, agyPath, bf)
	}
	if c.modelsExplain != "" {
		return runModelsExplain(c, cat, c.modelsExplain)
	}
	if c.modelsPick != "" {
		return runModelsPick(sig, c, cat, agyPath, poolsProbed, bf.callerFamily)
	}
	if c.jsonOut {
		return writeIndentedJSON(c, cat)
	}
	printCatalogText(c, cat)
	return nil
}

func loadOrRefreshCatalog(ctx context.Context, c *ctx, agyPath string) (models.Catalog, bool, error) {
	if c.modelsRefresh || !models.CatalogExists(c.home) {
		cat, err := models.RefreshCatalog(ctx, c.home, agyPath, loadMetricsModelEvidence(c.home))
		if err != nil {
			return cat, false, err
		}
		if c.modelsRefresh {
			cat = models.EnrichCatalogChangelog(ctx, c.home, agyPath, cat)
		}
		if isCrossFamilyPick(c.modelsPick, c.modelsTier) {
			return cat, false, nil
		}
		cat, probed := models.EnrichCatalogPoolsWithStatus(ctx, c.home, agyPath, cat)
		return cat, probed, nil
	}
	cat, ok, err := models.LoadCatalog(c.home)
	if err != nil {
		return models.Catalog{}, false, err
	}
	if !ok {
		return models.Catalog{}, false, fmt.Errorf("catalog missing at %s", models.CatalogPath(c.home))
	}
	if c.modelsPick != "" {
		return cat, false, nil
	}
	return models.RebuildCatalogWithEvidence(time.Now().UTC(), cat, models.AggregateWorkEvidence(c.home, loadMetricsModelEvidence(c.home))), false, nil
}

func isCrossFamilyPick(role, tier string) bool {
	if role == "" {
		return false
	}
	normRole, _, err := models.NormalizeRoleTier(role, tier)
	return err == nil && isCrossFamilyRole(normRole)
}

func runModelsBench(ctx context.Context, c *ctx, cat models.Catalog, agyPath string, bf modelsBenchFlags) error {
	targets, err := models.SelectBenchTargets(cat, bf.model, bf.max)
	if err != nil {
		fmt.Fprintf(stderr, "models: %v\n", err)
		return exitCode(1)
	}
	if len(targets) > 0 && agyPath == "" && models.UsesDefaultBenchRunner() {
		fmt.Fprintln(stderr, "models: agy not found on PATH for --bench")
		return exitCode(1)
	}
	rep, err := models.RunBench(ctx, c.home, agyPath, targets, func(rec models.BenchTrialRecord) error {
		return metrics.RecordTrial(c.home, metrics.TrialInput{
			Run: rec.Run, Label: "bench=sanity", Kind: "bench", Harness: buildVersion(),
			Status: rec.Status, Cause: rec.Cause, StartedAt: rec.StartedAt,
			Seconds: max(rec.DurationMS/1000, 0), DurationMS: rec.DurationMS,
			Model: rec.Model, PolicyModel: rec.Model,
		})
	})
	if err != nil {
		fmt.Fprintf(stderr, "models: %v\n", err)
		return exitCode(1)
	}
	updated := models.RebuildCatalogWithEvidence(time.Now().UTC(), cat, models.AggregateWorkEvidence(c.home, loadMetricsModelEvidence(c.home)))
	if err := models.SaveCatalog(c.home, updated); err != nil {
		fmt.Fprintf(stderr, "models: %v\n", err)
		return exitCode(1)
	}
	models.FinalizeBenchReport(c.out, &rep, updated, !c.jsonOut)
	if c.jsonOut {
		return writeIndentedJSON(c, rep)
	}
	return nil
}

func runModelsPick(ctx context.Context, c *ctx, cat models.Catalog, agyPath string, poolsProbed bool, callerFamily string) error {
	normRole, normTier, _ := models.NormalizeRoleTier(c.modelsPick, c.modelsTier)
	pickOpts := models.PickRoleOptions{
		Home:         c.home,
		AgyPath:      agyPath,
		ForceRefresh: c.modelsRefresh && !poolsProbed,
		CallerFamily: callerFamily,
	}
	pick, pools, err := models.PickRoleFromCatalogForHome(ctx, cat, pickOpts, c.modelsPick, c.modelsTier)
	if err != nil {
		fmt.Fprintf(stderr, "models: %v\n", err)
		return exitCode(1)
	}
	ttl := models.AdaptivePoolQuotaTTL(cat.Pools)
	freshCached := !pickOpts.ForceRefresh && models.HasFreshPoolQuotas(cat, time.Now().UTC(), ttl)
	if agyPath == "" && !freshCached && isCrossFamilyRole(normRole) && isAnthropicPick(cat.Entries, pick.Model) {
		fb, fbErr := fallbackPickWithoutAgy(cat, normRole, normTier)
		if fbErr != nil {
			fmt.Fprintf(stderr, "models: %v\n", fbErr)
			return exitCode(1)
		}
		pick = fb
		pools = nil
	}
	if len(pools) > 0 && c.home != "" && models.CheckHomeDir(c.home) == nil {
		models.PersistCatalogPoolsBestEffort(c.home, pools)
	}
	if c.jsonOut {
		return writeIndentedJSON(c, pick)
	}
	if pick.FallbackReason != "" {
		fmt.Fprintf(stderr, "fyr models: %s\n", pick.FallbackReason)
	}
	fmt.Fprintln(c.out, pick.Model)
	return nil
}

func isCrossFamilyRole(role string) bool {
	return models.IsCrossFamilyRole(role)
}

func isAnthropicPick(entries []models.CatalogEntry, modelID string) bool {
	for _, e := range entries {
		if e.ID == modelID {
			return strings.EqualFold(e.Family, "anthropic")
		}
	}
	return strings.HasPrefix(strings.ToLower(modelID), "claude")
}

func nonAnthropicEntries(entries []models.CatalogEntry) []models.CatalogEntry {
	out := make([]models.CatalogEntry, 0, len(entries))
	for _, e := range entries {
		if !strings.EqualFold(e.Family, "anthropic") {
			out = append(out, e)
		}
	}
	return out
}

func fallbackPickWithoutAgy(cat models.Catalog, normRole, normTier string) (models.RolePick, error) {
	fb, err := models.PickFromEntriesWithPools(nonAnthropicEntries(cat.Entries), nil, cat.Pools, normRole, normTier)
	if err != nil {
		return models.RolePick{}, fmt.Errorf("agy not found on PATH; cannot verify Anthropic quota for %s: %w", normRole, err)
	}
	fb.FallbackReason = "agy not found on PATH; cannot verify Anthropic quota — fallback to Gemini"
	return fb, nil
}

func writeIndentedJSON(c *ctx, v any) error {
	enc := json.NewEncoder(c.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func runModelsExplain(c *ctx, cat models.Catalog, target string) error {
	if target == bareExplainSentinel {
		if c.jsonOut {
			entries := cat.Entries
			if entries == nil {
				entries = []models.CatalogEntry{}
			}
			return writeIndentedJSON(c, entries)
		}
		for i, e := range cat.Entries {
			if i > 0 {
				fmt.Fprintln(c.out)
			}
			printCatalogEntryDetail(c, e)
		}
		return nil
	}
	lowTarget := strings.ToLower(strings.TrimSpace(target))
	for _, e := range cat.Entries {
		if strings.EqualFold(e.ID, lowTarget) || strings.ToLower(e.Subfamily) == lowTarget {
			if c.jsonOut {
				return writeIndentedJSON(c, e)
			}
			printCatalogEntryDetail(c, e)
			return nil
		}
	}
	fmt.Fprintf(stderr, "models: no entry matching %q in %s\n", target, models.CatalogPath(c.home))
	return exitCode(1)
}

func printCatalogEntryDetail(c *ctx, e models.CatalogEntry) {
	fmt.Fprintf(c.out, "ID: %s (%s)\n", e.ID, e.Status)
	fmt.Fprintf(c.out, "Family/Subfamily: %s / %s · Tier: %s · Effort: %s · Speed: %s · Mode: %s\n",
		e.Family, e.Subfamily, e.Tier, e.Effort, e.Speed, e.Mode)
	if e.Summary != "" {
		fmt.Fprintf(c.out, "Summary: %s\n", e.Summary)
	}
	if len(e.BestFor) > 0 {
		fmt.Fprintf(c.out, "Best-for: %s\n", strings.Join(e.BestFor, ", "))
	}
	if len(e.AvoidFor) > 0 {
		fmt.Fprintf(c.out, "Avoid-for: %s\n", strings.Join(e.AvoidFor, ", "))
	}
	if tot := models.TotalEvidenceRuns(e.Evidence); tot > 0 || e.Evidence.DecayedRuns > 0 {
		fmt.Fprintf(c.out, "Evidence: %d runs (decayed %.2f/%.2f, work %d/%d @ %.0fs, trials %d/%d, judge %d/%d, ratings %d)\n",
			tot, e.Evidence.DecayedSuccesses, e.Evidence.DecayedRuns, e.Evidence.WorkPass, e.Evidence.WorkRuns, e.Evidence.WorkMeanSeconds,
			max(e.Evidence.EvalPass, e.Evidence.TrialPass), max(e.Evidence.EvalRuns, e.Evidence.TrialRuns), e.Evidence.JudgePass, e.Evidence.JudgeRuns, e.Evidence.RatingRuns)
	} else {
		fmt.Fprintln(c.out, "Evidence: 0 runs (unprobed --bench candidate)")
	}
	if e.Score != nil {
		fmt.Fprintf(c.out, "Score: %.3f (posterior=%.3f, ucb1=%.3f, headroom_penalty=%.3f, decayed_runs=%.2f)\n",
			e.Score.RankScore, e.Score.PosteriorMean, e.Score.ExploreBonus, e.Score.HeadroomPenalty, e.Score.DecayedRuns)
	}
	if e.RetiringOn != "" {
		fmt.Fprintf(c.out, "Retiring: %s\n", e.RetiringOn)
	}
	if e.ChangelogNote != "" {
		fmt.Fprintf(c.out, "Changelog: %s\n", e.ChangelogNote)
	}
}

func printCatalogText(c *ctx, cat models.Catalog) {
	fmt.Fprintf(c.out, "%-30s  %-6s  %-9s  %-5s  %-10s  %-7s  %-22s  %s\n",
		"MODEL", "STATUS", "FAMILY", "TIER", "EFFORT", "DECAYED", "SCORE (POST/UCB/PEN)", "BEST FOR")
	for _, e := range cat.Entries {
		eff := e.Effort
		if eff == "" {
			eff = "-"
		}
		runs := "-"
		if n, s := models.EffectiveEvidence(e.Evidence); n > 0 {
			runs = fmt.Sprintf("%.1f/%.1f", s, n)
		}
		spd := "-"
		if e.Score != nil {
			spd = fmt.Sprintf("%.2f (%.2f+%.2f-%.2f)", e.Score.RankScore, e.Score.PosteriorMean, e.Score.ExploreBonus, e.Score.HeadroomPenalty)
		}
		best := e.DisplayName
		if len(e.BestFor) > 0 {
			best = strings.Join(e.BestFor, ",")
		} else if best == "" {
			best = "-"
		}
		if e.RetiringOn != "" {
			best += " [retiring " + e.RetiringOn + "]"
		}
		fmt.Fprintf(c.out, "%-30s  %-6s  %-9s  %-5s  %-10s  %-7s  %-22s  %s\n",
			e.ID, e.Status, e.Family, e.Tier, eff, runs, spd, best)
	}
	if len(cat.Picks) == 0 {
		return
	}
	fmt.Fprintln(c.out, "\nRole picks:")
	for _, rt := range models.StandardRoleTiers {
		p, ok := cat.Picks[rt[0]+":"+rt[1]]
		if !ok {
			continue
		}
		line := fmt.Sprintf("  %s (%s): %s — %s", p.Role, p.Tier, p.Model, p.Reason)
		if p.FallbackReason != "" && p.FallbackReason != p.Reason {
			line += fmt.Sprintf(" [fallback: %s]", p.FallbackReason)
		}
		fmt.Fprintln(c.out, line)
	}
}

func refreshModelsBestEffort(parent context.Context, home string) {
	if home == "" || models.CheckHomeDir(home) != nil || trail.Command(home, "rev-parse", "--git-dir").Run() == nil {
		return
	}
	if agyPath, ok := dream.ResolveBinary("agy", home); ok {
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		defer cancel()
		_, _ = models.RefreshCatalog(ctx, home, agyPath, loadMetricsModelEvidence(home))
	}
}

func loadMetricsModelEvidence(home string) map[string]models.ModelEvidence {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	out := map[string]models.ModelEvidence{}
	mergeMetricsEvidenceInto(out, loadPrimaryMetricsEvidence(home))
	if devStore, cleanup, ok := metrics.OpenDevEvalFallbackRO(home); ok {
		defer cleanup()
		if raw, err := devStore.ModelEvidence(time.Time{}, time.Time{}); err == nil {
			mergeMetricsEvidenceInto(out, raw)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func loadPrimaryMetricsEvidence(home string) map[string]metrics.ModelEvidence {
	dbPath := metrics.Path(home)
	if fi, err := os.Lstat(dbPath); err != nil || !fi.Mode().IsRegular() {
		return nil
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dbPath), "models-metrics-")
	if err != nil {
		return nil
	}
	defer os.RemoveAll(tmp)
	s, err := metrics.Snapshot(dbPath, tmp)
	if err != nil {
		return nil
	}
	defer s.Close()
	raw, err := s.ModelEvidence(time.Time{}, time.Time{})
	if err != nil || len(raw) == 0 {
		return nil
	}
	return raw
}

func mergeMetricsEvidenceInto(out map[string]models.ModelEvidence, raw map[string]metrics.ModelEvidence) {
	for k, v := range raw {
		cur := out[k]
		prevRatings := cur.RatingRuns
		prevWork := cur.WorkRuns
		prevLat := cur.WorkRuns + cur.EvalRuns
		addLat := v.WorkRuns + v.EvalRuns
		cur.WorkMeanSeconds = mergeWeightedMean(cur.WorkMeanSeconds, prevWork, v.WorkMeanSeconds, v.WorkRuns)
		cur.MeanLatencySeconds = mergeWeightedMean(cur.MeanLatencySeconds, prevLat, v.MeanLatencySeconds, addLat)
		cur.WorkPass, cur.WorkRuns = cur.WorkPass+v.WorkPass, cur.WorkRuns+v.WorkRuns
		cur.EvalPass, cur.EvalRuns = cur.EvalPass+v.EvalPass, cur.EvalRuns+v.EvalRuns
		cur.TrialPass, cur.TrialRuns = cur.TrialPass+v.TrialPass, cur.TrialRuns+v.TrialRuns
		cur.JudgePass, cur.JudgeRuns = cur.JudgePass+v.JudgePass, cur.JudgeRuns+v.JudgeRuns
		cur.RatingRuns, cur.RatingScoreSum = cur.RatingRuns+v.RatingRuns, cur.RatingScoreSum+v.RatingScoreSum
		cur.DecayedRuns, cur.DecayedSuccesses = cur.DecayedRuns+v.DecayedRuns, cur.DecayedSuccesses+v.DecayedSuccesses
		mergeMetricsRoleEvidence(&cur.BuilderEvidence, v.BuilderEvidence)
		mergeMetricsRoleEvidence(&cur.JudgeEvidence, v.JudgeEvidence)
		if v.MeanTokenEfficiency > 0 {
			eff := v.MeanTokenEfficiency
			if cur.MeanTokenEfficiency > 0 && prevRatings > 0 && v.RatingRuns > 0 {
				tot := float64(prevRatings + v.RatingRuns)
				eff = (cur.MeanTokenEfficiency*float64(prevRatings) + eff*float64(v.RatingRuns)) / tot
			}
			cur.MeanTokenEfficiency = eff
		}
		out[k] = cur
	}
}

func mergeWeightedMean(curMean float64, curN int, addMean float64, addN int) float64 {
	switch {
	case addMean <= 0:
		return curMean
	case curMean <= 0:
		return addMean
	case curN > 0 && addN > 0:
		return (curMean*float64(curN) + addMean*float64(addN)) / float64(curN+addN)
	default:
		return curMean
	}
}

func mergeMetricsRoleEvidence(dst *models.RoleEvidence, src metrics.RoleEvidence) {
	dst.Runs += src.Runs
	dst.Pass += src.Pass
	dst.DecayedRuns += src.DecayedRuns
	dst.DecayedSuccesses += src.DecayedSuccesses
}
