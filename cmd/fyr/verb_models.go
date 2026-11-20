package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/models"
	"github.com/kesokaj/fyr/internal/trail"
)

func init() {
	verbs["models"] = verb{
		flags: func(c *ctx) {
			c.fs.Bool("bench", false, "probe up to --max 0-run active models and record kind=bench trials")
			c.fs.StringVar(&c.modelsExplain, "explain", "", "print profile, score breakdown, and empirical stats for model `ID`")
			c.fs.BoolVar(&c.jsonOut, "json", false, "print the catalog, --pick, --explain or --bench result as JSON")
			c.fs.Int("max", models.DefaultBenchMax, "with --bench: probe at most `N` 0-run active models (default 3)")
			c.fs.String("model", "", "with --bench: probe only the active non-interactive model `ID`")
			c.fs.StringVar(&c.modelsPick, "pick", "", "pick one model for `ROLE`: builder, planner, judge, reviewer, verifier or mechanical")
			c.fs.BoolVar(&c.modelsRefresh, "refresh", false, "re-query agy models and update .config/fyr/models.json")
			c.fs.StringVar(&c.modelsTier, "tier", "", "with --pick: the tier `T`, pro, flash or light")
		},
		run: runModels,
	}
}

type modelsBenchFlags struct {
	bench, maxSet, modelSet bool
	model                   string
	max                     int
}

func parseAndValidateModels(c *ctx) (modelsBenchFlags, error) {
	bf := modelsBenchFlags{max: models.DefaultBenchMax}
	c.fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "bench":
			bf.bench = f.Value.String() == "true"
		case "model":
			bf.model, bf.modelSet = f.Value.String(), true
		case "max":
			bf.maxSet = true
			bf.max, _ = strconv.Atoi(f.Value.String())
		}
	})
	switch {
	case c.modelsTier != "" && c.modelsPick == "":
		return bf, usageErr(c.out, "models", "fyr models: --tier requires --pick")
	case c.modelsExplain != "" && c.modelsPick != "":
		return bf, usageErr(c.out, "models", "fyr models: --explain and --pick cannot be combined")
	case bf.modelSet && !bf.bench:
		return bf, usageErr(c.out, "models", "fyr models: --model requires --bench")
	case bf.maxSet && !bf.bench:
		return bf, usageErr(c.out, "models", "fyr models: --max requires --bench")
	case bf.bench && (c.modelsPick != "" || c.modelsExplain != ""):
		return bf, usageErr(c.out, "models", "fyr models: --bench cannot be combined with --pick or --explain")
	case bf.bench && bf.max < 1:
		return bf, usageErr(c.out, "models", "fyr models: --max must be at least 1")
	}
	if c.modelsPick != "" {
		if _, _, err := models.NormalizeRoleTier(c.modelsPick, c.modelsTier); err != nil {
			return bf, usageErr(c.out, "models", "fyr models: "+err.Error())
		}
	}
	return bf, nil
}

func runModels(c *ctx) error {
	bf, err := parseAndValidateModels(c)
	if err != nil {
		return err
	}
	sig, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	agyPath, _ := exec.LookPath("agy")
	cat, err := loadOrRefreshCatalog(sig, c, agyPath)
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
		return runModelsPick(sig, c, cat, agyPath)
	}
	if c.jsonOut {
		return writeIndentedJSON(c, cat)
	}
	printCatalogText(c, cat)
	return nil
}

func loadOrRefreshCatalog(ctx context.Context, c *ctx, agyPath string) (models.Catalog, error) {
	if c.modelsRefresh || !models.CatalogExists(c.home) {
		cat, err := models.RefreshCatalog(ctx, c.home, agyPath, loadMetricsModelEvidence(c.home))
		if err == nil {
			if c.modelsRefresh {
				cat = models.EnrichCatalogChangelog(ctx, c.home, agyPath, cat)
			}
			cat = models.EnrichCatalogPools(ctx, c.home, agyPath, cat)
		}
		return cat, err
	}
	cat, ok, err := models.LoadCatalog(c.home)
	if err != nil {
		return models.Catalog{}, err
	}
	if !ok {
		return models.Catalog{}, fmt.Errorf("catalog missing at %s", models.CatalogPath(c.home))
	}
	return models.RebuildCatalogWithEvidence(time.Now().UTC(), cat, models.AggregateWorkEvidence(c.home, loadMetricsModelEvidence(c.home))), nil
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
			Seconds: max(rec.DurationMS/1000, 0), DurationMS: rec.DurationMS, Model: rec.Model,
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

func runModelsPick(ctx context.Context, c *ctx, cat models.Catalog, agyPath string) error {
	normRole, normTier, _ := models.NormalizeRoleTier(c.modelsPick, c.modelsTier)
	pick, pools, err := models.PickRoleFromCatalogWithPools(ctx, cat, agyPath, c.modelsPick, c.modelsTier)
	if err != nil {
		fmt.Fprintf(stderr, "models: %v\n", err)
		return exitCode(1)
	}
	if agyPath == "" && isCrossFamilyRole(normRole) && isAnthropicPick(cat.Entries, pick.Model) {
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
	return role == "judge" || role == "reviewer" || role == "verifier"
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
	if agyPath, err := exec.LookPath("agy"); err == nil {
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		defer cancel()
		_, _ = models.RefreshCatalog(ctx, home, agyPath, loadMetricsModelEvidence(home))
	}
}

func loadMetricsModelEvidence(home string) map[string]models.ModelEvidence {
	if strings.TrimSpace(home) == "" {
		return nil
	}
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
	out := make(map[string]models.ModelEvidence, len(raw))
	for k, v := range raw {
		var e models.ModelEvidence
		e.WorkPass, e.WorkRuns, e.WorkMeanSeconds = v.WorkPass, v.WorkRuns, v.WorkMeanSeconds
		e.EvalPass, e.EvalRuns = v.EvalPass, v.EvalRuns
		e.TrialPass, e.TrialRuns = v.TrialPass, v.TrialRuns
		e.JudgePass, e.JudgeRuns = v.JudgePass, v.JudgeRuns
		e.RatingRuns, e.RatingScoreSum = v.RatingRuns, v.RatingScoreSum
		e.DecayedRuns, e.DecayedSuccesses = v.DecayedRuns, v.DecayedSuccesses
		out[k] = e
	}
	return out
}
