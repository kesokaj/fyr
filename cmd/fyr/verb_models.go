package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/sandbox"
	"github.com/kesokaj/fyr/internal/work"
)

func init() {
	verbs["models"] = verb{
		flags: func(c *ctx) {
			c.fs.BoolVar(&c.jsonOut, "json", false, "print the catalog or --pick result as JSON")
			c.fs.StringVar(&c.modelsPick, "pick", "", "pick one model for `ROLE`: builder, planner, judge, reviewer, verifier or mechanical")
			c.fs.StringVar(&c.modelsExplain, "explain", "", "print profile, best-for/avoid-for guidance, and empirical stats for model `ID`")
			c.fs.BoolVar(&c.modelsRefresh, "refresh", false, "re-query agy models and update .config/fyr/models.json")
			c.fs.StringVar(&c.modelsTier, "tier", "", "with --pick: the tier `T`, pro or flash")
		},
		run: runModels,
	}
}

func runModels(c *ctx) error {
	if c.modelsTier != "" && c.modelsPick == "" {
		return usageErr(c.out, "models", "fyr models: --tier requires --pick")
	}
	if c.modelsPick != "" {
		if _, _, err := work.NormalizeRoleTier(c.modelsPick, c.modelsTier); err != nil {
			return usageErr(c.out, "models", "fyr models: "+err.Error())
		}
	}
	if c.modelsExplain != "" && c.modelsPick != "" {
		return usageErr(c.out, "models", "fyr models: --explain and --pick cannot be combined")
	}
	sig, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	agyPath, _ := exec.LookPath("agy")
	cat, err := loadOrRefreshCatalog(sig, c, agyPath)
	if err != nil {
		fmt.Fprintf(c.out, "models: %v\n", err)
		return exitCode(1)
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

func loadOrRefreshCatalog(ctx context.Context, c *ctx, agyPath string) (work.Catalog, error) {
	if c.modelsRefresh || !work.CatalogExists(c.home) {
		cat, err := work.RefreshCatalog(ctx, c.home, agyPath, loadMetricsModelEvidence(c.home))
		if err == nil && c.modelsRefresh {
			cat = work.EnrichCatalogChangelog(ctx, c.home, agyPath, cat)
		}
		return cat, err
	}
	cat, ok, err := work.LoadCatalog(c.home)
	if err != nil {
		return work.Catalog{}, err
	}
	if !ok {
		return work.Catalog{}, fmt.Errorf("catalog missing at %s", work.CatalogPath(c.home))
	}
	return cat, nil
}

func runModelsPick(ctx context.Context, c *ctx, cat work.Catalog, agyPath string) error {
	pick, err := work.PickRoleFromCatalog(ctx, cat, agyPath, c.modelsPick, c.modelsTier)
	if err != nil {
		fmt.Fprintf(c.out, "models: %v\n", err)
		return exitCode(1)
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

func writeIndentedJSON(c *ctx, v any) error {
	enc := json.NewEncoder(c.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func runModelsExplain(c *ctx, cat work.Catalog, target string) error {
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
	fmt.Fprintf(c.out, "models: no entry matching %q in %s\n", target, work.CatalogPath(c.home))
	return exitCode(1)
}

func printCatalogEntryDetail(c *ctx, e work.CatalogEntry) {
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
	if tot := e.Evidence.WorkRuns + e.Evidence.EvalRuns; tot > 0 {
		fmt.Fprintf(c.out, "Evidence: %d/%d total pass (work %d/%d @ %.0fs mean, eval %d/%d)\n",
			e.Evidence.WorkPass+e.Evidence.EvalPass, tot,
			e.Evidence.WorkPass, e.Evidence.WorkRuns, e.Evidence.WorkMeanSeconds,
			e.Evidence.EvalPass, e.Evidence.EvalRuns)
	} else {
		fmt.Fprintln(c.out, "Evidence: 0 runs (unjudged canary eligible)")
	}
	if e.RetiringOn != "" {
		fmt.Fprintf(c.out, "Retiring: %s\n", e.RetiringOn)
	}
	if e.ChangelogNote != "" {
		fmt.Fprintf(c.out, "Changelog: %s\n", e.ChangelogNote)
	}
}

func printCatalogText(c *ctx, cat work.Catalog) {
	fmt.Fprintf(c.out, "%-30s  %-6s  %-9s  %-5s  %-12s  %-7s  %-8s  %s\n",
		"MODEL", "STATUS", "FAMILY", "TIER", "EFFORT", "PASS", "SPEED", "BEST FOR")
	for _, e := range cat.Entries {
		eff := e.Effort
		if eff == "" {
			eff = "-"
		}
		runs := "-"
		if tot := e.Evidence.WorkRuns + e.Evidence.EvalRuns; tot > 0 {
			runs = fmt.Sprintf("%d/%d", e.Evidence.WorkPass+e.Evidence.EvalPass, tot)
		}
		spd := e.Speed
		if spd == "" {
			spd = "-"
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
		fmt.Fprintf(c.out, "%-30s  %-6s  %-9s  %-5s  %-12s  %-7s  %-8s  %s\n",
			e.ID, e.Status, e.Family, e.Tier, eff, runs, spd, best)
	}
	if len(cat.Picks) == 0 {
		return
	}
	fmt.Fprintln(c.out, "\nRole picks:")
	for _, rt := range work.StandardRoleTiers {
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

// refreshModelsBestEffort updates <home>/.config/fyr/models.json when home is
// a valid non-checkout sandbox home and agy is available, keeping any existing
// cache intact on error.
func refreshModelsBestEffort(parent context.Context, home string) {
	if home == "" {
		return
	}
	if sandbox.CheckWorkDir(home) != nil {
		return
	}
	if exec.Command("git", "-C", home, "rev-parse", "--git-dir").Run() == nil {
		return
	}
	agyPath, err := exec.LookPath("agy")
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	_, _ = work.RefreshCatalog(ctx, home, agyPath, loadMetricsModelEvidence(home))
}

func loadMetricsModelEvidence(home string) map[string]work.ModelEvidence {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	dbPath := metrics.Path(home)
	fi, err := os.Lstat(dbPath)
	if err != nil || !fi.Mode().IsRegular() {
		return nil
	}
	s, err := metrics.Open(dbPath)
	if err != nil {
		return nil
	}
	defer s.Close()
	raw, err := s.ModelEvidence(time.Time{}, time.Time{})
	if err != nil || len(raw) == 0 {
		return nil
	}
	out := make(map[string]work.ModelEvidence, len(raw))
	for k, v := range raw {
		out[k] = work.ModelEvidence{
			WorkRuns:        v.WorkRuns,
			WorkPass:        v.WorkPass,
			WorkMeanSeconds: v.WorkMeanSeconds,
			EvalRuns:        v.EvalRuns,
			EvalPass:        v.EvalPass,
		}
	}
	return out
}
