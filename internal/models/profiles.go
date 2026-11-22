package models

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	changelogDateRe = regexp.MustCompile(`\b(20\d{2}-\d{2}-\d{2})\b`)
	modelSizeTokRe  = regexp.MustCompile(`^\d+(?:\.\d+)?(?:[bkm])?$`)
)

// ApplyModelProfile populates dynamic capability metadata and role guidance on
// a CatalogEntry from its normalized ID and display name without static codenames.
func ApplyModelProfile(e *CatalogEntry) {
	if e == nil {
		return
	}
	fam, tier, eff := ClassifyModel(e.ID, e.DisplayName)
	e.Family = nonEmpty(e.Family, fam)
	e.Tier = nonEmpty(e.Tier, tier)
	if e.Effort == "" || eff == "orchestrator" {
		e.Effort = eff
	}
	if IsInteractiveOnlyID(e.ID) || strings.Contains(strings.ToLower(e.DisplayName), "orchestrator") || e.Effort == "orchestrator" {
		e.InteractiveOnly, e.Mode = true, "orchestrator"
	} else {
		e.Mode = "standard"
	}
	e.Subfamily = inferModelSubfamily(e.ID, e.Family, e.InteractiveOnly)
	e.Speed = inferModelSpeed(e)
	populateDynamicGuidance(e)
	if e.ContextWindow <= 0 {
		e.ContextWindow = inferEntryContextWindow(*e)
	}
	if e.RetiringOn == "" {
		if kwIdx := earliestKeywordIndex(strings.ToLower(e.DisplayName), "retir", "deprecat"); kwIdx >= 0 {
			e.RetiringOn = changelogDateRe.FindString(strings.ToLower(e.DisplayName)[kwIdx:])
		}
	}
}

func inferEntryContextWindow(e CatalogEntry) int64 {
	if hint := ParseContextWindowHint(e.DisplayName); hint > 0 {
		return hint
	}
	if hint := ParseContextWindowHint(e.ChangelogNote); hint > 0 {
		return hint
	}
	return DefaultContextWindow(e.ID)
}

func inferModelSubfamily(id, family string, interactive bool) string {
	if interactive {
		return "orchestrator"
	}
	stem, _ := bareModelStem(id)
	var kept []string
	for i, tok := range strings.FieldsFunc(stem, func(r rune) bool { return r == '-' || r == '_' }) {
		if (i == 0 && (tok == "gemini" || tok == "claude")) || modelSizeTokRe.MatchString(tok) {
			continue
		}
		kept = append(kept, tok)
	}
	if len(kept) == 0 {
		return nonEmpty(family, "other")
	}
	return strings.Join(kept, "-")
}

func inferModelSpeed(e *CatalogEntry) string {
	if e.InteractiveOnly || e.Mode == "orchestrator" {
		return "balanced"
	}
	if e.Tier == "light" || (e.Tier == "flash" && e.Effort != "xhigh" && e.Effort != "max") || e.Effort == "low" {
		return "fast"
	}
	if e.Tier == "pro" && (e.Effort == "high" || e.Effort == "xhigh" || e.Effort == "max") {
		return "deep"
	}
	return "balanced"
}

func populateDynamicGuidance(e *CatalogEntry) {
	effLabel := nonEmpty(e.Effort, "default")
	switch {
	case e.InteractiveOnly || e.Mode == "orchestrator":
		e.BestFor, e.AvoidFor = []string{"interactive-sessions"}, []string{"headless-work", "eval", "bench"}
		e.Summary = "Interactive multi-phase orchestrator; excluded from headless routing"
	case isAnthropicFamily(e.Family):
		if e.Tier == "pro" {
			e.BestFor, e.AvoidFor = []string{"judge:pro", "reviewer:pro", "verifier:pro", "architecture"}, []string{"mechanical:flash"}
		} else {
			e.BestFor, e.AvoidFor = []string{"judge:flash", "reviewer:flash", "verifier:flash"}, []string{"quota-constrained-bulk"}
		}
		e.Summary = fmt.Sprintf("Dynamic %s %s (%s) cross-family model", e.Family, e.Tier, effLabel)
	case isGeminiFamily(e.Family, e.ID):
		if e.Tier == "pro" {
			e.BestFor, e.AvoidFor = []string{"builder:pro", "planner:pro", "architecture"}, []string{"mechanical:flash"}
		} else {
			e.BestFor, e.AvoidFor = []string{"mechanical:flash", "builder:flash"}, []string{"architecture", "planner:pro"}
		}
		e.Summary = fmt.Sprintf("Dynamic %s %s (%s) model", e.Family, e.Tier, effLabel)
	default:
		e.BestFor, e.AvoidFor = []string{"fallback:" + nonEmpty(e.Tier, "pro")}, []string{"cross-family-judge"}
		e.Summary = fmt.Sprintf("Dynamic %s (%s) model", nonEmpty(e.Family, "other"), nonEmpty(e.Tier, "pro"))
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
	out, err := runAgyProbeCapped(ctx, home, agyPath, ChangelogTimeout, 2*time.Second, MaxChangelogOut, "changelog")
	if err != nil {
		return ""
	}
	return ansiCSIRe.ReplaceAllString(out, "")
}

// EnrichCatalogChangelog runs `agy changelog` best-effort (never failing the
// caller), enriches ChangelogNote/RetiringOn on cat.Entries, and saves cat.
func EnrichCatalogChangelog(ctx context.Context, home, agyPath string, cat Catalog) Catalog {
	enrichEntriesFromChangelog(ctx, home, agyPath, cat.Entries)
	populateEntryScores(cat.Entries, cat.Pools)
	cat.Picks = computeStandardPicks(cat.Entries, cat.Picks, cat.Pools)
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
		for _, line := range lines {
			for _, clause := range splitChangelogClauses(line) {
				applyChangelogClause(&entries[i], clause)
			}
		}
	}
}

func applyChangelogClause(e *CatalogEntry, clause string) {
	low := strings.ToLower(clause)
	if clause == "" || !matchesChangelogLine(*e, low) {
		return
	}
	if e.RetiringOn == "" {
		if kwIdx := earliestKeywordIndex(low, "retir", "deprecat"); kwIdx >= 0 {
			if m := changelogDateRe.FindString(low[kwIdx:]); m != "" {
				e.RetiringOn = m
			}
		}
	}
	if hint := ParseContextWindowHint(clause); hint > 0 {
		e.ContextWindow = hint
	}
	if e.ChangelogNote == "" {
		e.ChangelogNote = shortQuote(clause)
	}
}

func splitChangelogClauses(line string) []string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return nil
	}
	var clauses []string
	for _, semi := range strings.Split(trimmed, ";") {
		for _, sent := range strings.Split(semi, ". ") {
			if c := strings.TrimSpace(sent); c != "" {
				clauses = append(clauses, c)
			}
		}
	}
	return clauses
}

func matchesChangelogLine(e CatalogEntry, lowLine string) bool {
	stem, ok := bareModelStem(e.ID)
	if !ok {
		stem = strings.ToLower(strings.TrimSpace(e.ID))
	}
	if stem != "" && strings.Contains(lowLine, stem) {
		return true
	}
	sub := strings.ToLower(strings.TrimSpace(e.Subfamily))
	if sub == "" || sub == "other" || sub == "orchestrator" || sub == "gemini" || sub == "anthropic" {
		return false
	}
	hasSub, hasFam := false, false
	for _, w := range strings.FieldsFunc(lowLine, func(r rune) bool { return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') }) {
		hasSub = hasSub || w == sub
		hasFam = hasFam || w == e.Family || (e.Family == "anthropic" && w == "claude")
	}
	return hasSub && hasFam
}

func isAutoPickEligible(e CatalogEntry) bool {
	if e.Status == "gone" || e.InteractiveOnly || e.Mode == "orchestrator" {
		return false
	}
	return strings.TrimSpace(e.RetiringOn) == ""
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
	entries, pools := buildListedCatalogEntriesAndPools(home, ids)
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
	if pick, err := PickFromEntriesWithPools(eligible, nil, pools, role, tier); err == nil {
		return pick.Model
	}
	return eligible[0].ID
}
