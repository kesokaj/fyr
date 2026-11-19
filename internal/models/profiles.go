package models

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
)

var changelogDateRe = regexp.MustCompile(`\b(20\d{2}-\d{2}-\d{2})\b`)

type staticModelProfile struct {
	match           string
	subfamily       string
	mode            string
	speed           string
	summary         string
	bestFor         []string
	avoidFor        []string
	interactiveOnly bool
	retiringOn      string
}

var knownModelProfiles = []staticModelProfile{
	{"orchestrator", "orchestrator", "orchestrator", "balanced", "Interactive 2-phase switcher (Carbon/Argon plan -> Hydrogen execution); default for interactive agy", []string{"interactive-sessions"}, []string{"headless-fyr-work", "fyr-eval"}, true, ""},
	{"carbon", "carbon", "", "deep", "Deepest frontier planner and complex multi-file coder; highest thinking latency", []string{"architecture", "hard-debugging", "complex-build"}, []string{"trivial-edits", "tight-timeouts"}, false, ""},
	{"argon", "argon", "", "balanced", "Balanced Gemini 4 Pro coder and planner with strong pass rate and moderate speed", []string{"standard-build", "planning", "eval"}, []string{"mechanical-renames"}, false, ""},
	{"hydrogen", "hydrogen", "", "fast", "Fast distilled Gemini 4 execution coder; supports XHigh effort at low latency", []string{"fast-coding", "plan-execution", "2-4-file-tasks"}, []string{"cold-architecture-planning", "cross-family-judge"}, false, ""},
	{"3.8-flash", "flash", "", "fast", "High-speed low-cost model for mechanical edits, docs, and recon", []string{"mechanical", "docs", "explorer"}, []string{"deep-architecture", "security-review"}, false, ""},
	{"opus", "opus", "", "deep", "Cross-family frontier judge, code/security reviewer, and verifier", []string{"judge", "reviewer", "verifier", "security"}, []string{"high-volume-mechanical"}, false, ""},
	{"sonnet", "sonnet", "", "balanced", "Fast cross-family reviewer and flash-tier judge", []string{"judge:flash", "reviewer:flash"}, []string{"gemini-only-quota-conservation"}, false, ""},
	{"gpt-oss", "gpt-oss", "", "balanced", "Legacy open-weight model sharing Claude/GPT quota pool; retiring 2026-11-18", []string{"legacy-compat"}, []string{"auto-pick"}, false, "2026-11-18"},
}

// ApplyModelProfile populates static role guidance and capability metadata on
// a CatalogEntry from its normalized ID and display name.
func ApplyModelProfile(e *CatalogEntry) {
	if e == nil {
		return
	}
	lowAll := strings.ToLower(strings.TrimSpace(e.ID) + " " + e.DisplayName)
	e.Mode = "standard"
	for _, p := range knownModelProfiles {
		if strings.Contains(lowAll, p.match) || (p.match == "3.8-flash" && (strings.Contains(lowAll, "3-8-flash") || (e.Family == "gemini" && e.Tier == "flash"))) {
			e.Subfamily, e.Speed, e.Summary, e.BestFor, e.AvoidFor, e.InteractiveOnly = p.subfamily, p.speed, p.summary, p.bestFor, p.avoidFor, p.interactiveOnly
			if p.mode != "" {
				e.Mode = p.mode
			}
			if p.retiringOn != "" && e.RetiringOn == "" {
				e.RetiringOn = p.retiringOn
			}
			return
		}
	}
	if e.Subfamily == "" {
		e.Subfamily = nonEmpty(e.Family, "other")
	}
	if e.Speed == "" {
		e.Speed = map[bool]string{true: "fast", false: "balanced"}[e.Tier == "flash"]
	}
	if e.Summary == "" {
		e.Summary = "Newly discovered " + e.Family + " (" + e.Tier + ") model; awaiting empirical benchmark runs"
	}
}

// RunAgyChangelog runs `agy changelog` best-effort in probeWorkDir() with
// process-group cleanup.
func RunAgyChangelog(ctx context.Context, agyPath string) string {
	return runAgyChangelogForHome(ctx, "", agyPath)
}

func runAgyChangelogForHome(ctx context.Context, home, agyPath string) string {
	if strings.TrimSpace(agyPath) == "" {
		return ""
	}
	out, err := runAgyProbe(ctx, home, agyPath, 2*time.Second, 2*time.Second, "changelog")
	if err != nil {
		return ""
	}
	return ansiCSIRe.ReplaceAllString(out, "")
}

// EnrichCatalogChangelog runs `agy changelog` best-effort (never failing the
// caller), enriches ChangelogNote/RetiringOn on cat.Entries, and saves cat.
func EnrichCatalogChangelog(ctx context.Context, home, agyPath string, cat Catalog) Catalog {
	enrichEntriesFromChangelog(ctx, home, agyPath, cat.Entries)
	if home != "" {
		_ = SaveCatalog(home, cat)
	}
	return cat
}

func enrichEntriesFromChangelog(ctx context.Context, home, agyPath string, entries []CatalogEntry) {
	raw := runAgyChangelogForHome(ctx, home, agyPath)
	if strings.TrimSpace(raw) == "" {
		return
	}
	applyChangelogText(raw, entries)
}

func applyChangelogText(raw string, entries []CatalogEntry) {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	for i := range entries {
		stem, ok := bareModelStem(entries[i].ID)
		if !ok {
			stem = strings.ToLower(strings.TrimSpace(entries[i].ID))
		}
		sub := strings.ToLower(strings.TrimSpace(entries[i].Subfamily))
		matchSub := sub != "" && sub != "other" && sub != "orchestrator" && sub != "gemini" && sub != "anthropic"
		for _, line := range lines {
			t := strings.TrimSpace(line)
			if t == "" {
				continue
			}
			low := strings.ToLower(t)
			matchedStem := stem != "" && strings.Contains(low, stem)
			matchedSub := matchSub && strings.Contains(low, sub)
			if !matchedStem && !matchedSub {
				continue
			}
			if entries[i].RetiringOn == "" {
				if kwIdx := earliestKeywordIndex(low, "retir", "deprecat"); kwIdx >= 0 {
					if m := changelogDateRe.FindString(low[kwIdx:]); m != "" {
						entries[i].RetiringOn = m
					}
				}
			}
			if entries[i].ChangelogNote == "" {
				entries[i].ChangelogNote = shortQuote(t)
			}
		}
	}
}

func isAutoPickEligible(e CatalogEntry) bool {
	if e.Status == "gone" || e.InteractiveOnly || e.Mode == "orchestrator" {
		return false
	}
	return strings.TrimSpace(e.RetiringOn) == ""
}

// UnjudgedActiveModels returns active, non-interactive, non-retiring models
// that have zero combined work + eval runs so `fyr auto` can canary them.
func UnjudgedActiveModels(cat Catalog) []CatalogEntry {
	var out []CatalogEntry
	for _, e := range cat.Entries {
		if !isAutoPickEligible(e) {
			continue
		}
		if e.Evidence.WorkRuns+e.Evidence.EvalRuns == 0 {
			out = append(out, e)
		}
	}
	return out
}

// ResolveScopeRoleAndTier determines the best (role, tier) for a task based
// on its touched files scope and prompt complexity.
func ResolveScopeRoleAndTier(files []string, text string) (role, tier, rationale string) {
	low := strings.ToLower(text)
	fileCount := len(files)
	isDocOrMechanical := fileCount > 0 && fileCount <= 2
	for _, f := range files {
		lf := strings.ToLower(f)
		isDoc := strings.HasSuffix(lf, ".md") || strings.HasSuffix(lf, ".txt") ||
			strings.HasPrefix(lf, "skills/") || strings.HasPrefix(lf, "rules/") || strings.HasPrefix(lf, "context/")
		if !isDoc {
			isDocOrMechanical = false
		}
		if strings.Contains(lf, "auth") || strings.Contains(lf, "crypto") ||
			strings.Contains(lf, "sandbox") || strings.Contains(lf, "hook") ||
			strings.Contains(lf, "search") || strings.Contains(lf, "security") {
			return "builder", "pro", "critical/security scope -> builder:pro"
		}
	}
	for _, kw := range []string{"architecture", "refactor", "security", "migration", "schema", "design"} {
		if strings.Contains(low, kw) {
			return "builder", "pro", "complex scope (" + kw + ") -> builder:pro"
		}
	}
	if isDocOrMechanical || strings.Contains(low, "typo") || strings.Contains(low, "rename") {
		return "mechanical", "flash", "small docs/mechanical scope -> mechanical:flash"
	}
	if fileCount > 0 && fileCount <= 3 {
		return "builder", "flash", "focused <=3-file coding scope -> builder:flash"
	}
	return "builder", "pro", "multi-file coding scope -> builder:pro"
}

func earliestKeywordIndex(s string, keywords ...string) int {
	best := -1
	for _, kw := range keywords {
		if idx := strings.Index(s, kw); idx >= 0 && (best < 0 || idx < best) {
			best = idx
		}
	}
	return best
}

// IsInteractiveOnlyID reports whether id refers to an interactive-only model
// (such as an orchestrator) that cannot run headless.
func IsInteractiveOnlyID(id string) bool {
	low := strings.ToLower(strings.TrimSpace(id))
	return strings.Contains(low, "orchestrator")
}

// PickAutoModelForScope resolves the best eligible listed model for files+text.
func PickAutoModelForScope(home string, files []string, text string, listed map[string]bool, ids []string) (string, string, error) {
	role, tier, rationale := ResolveScopeRoleAndTier(files, text)
	picked := PickFromListedIDs(home, listed, ids, role, tier)
	if picked == "" {
		return "", "", errors.New("auto-pick model: no eligible non-orchestrator model listed by agy models")
	}
	return picked, rationale, nil
}

// PickFromListedIDs selects the best eligible listed model for role+tier using
// empirical evidence stored under home when present.
func PickFromListedIDs(home string, listed map[string]bool, ids []string, role, tier string) string {
	entries := BuildListedCatalogEntries(home, ids)
	var eligible []CatalogEntry
	for _, e := range entries {
		ApplyModelProfile(&e)
		if listed[e.ID] && isAutoPickEligible(e) {
			eligible = append(eligible, e)
		}
	}
	if len(eligible) == 0 {
		return ""
	}
	if pick, err := PickFromEntries(eligible, nil, role, tier); err == nil {
		return pick.Model
	}
	return eligible[0].ID
}
