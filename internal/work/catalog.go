package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// ModelEvidence holds empirical run counts and pass rates for one model ID.
type ModelEvidence struct {
	WorkRuns        int     `json:"work_runs"`
	WorkPass        int     `json:"work_pass"`
	WorkMeanSeconds float64 `json:"work_mean_seconds"`
	EvalRuns        int     `json:"eval_runs"`
	EvalPass        int     `json:"eval_pass"`
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
	Evidence        ModelEvidence `json:"evidence"`
}

// RolePick records the selected active model and rationale for a role+tier.
type RolePick struct {
	Role           string `json:"role"`
	Tier           string `json:"tier"`
	Model          string `json:"model"`
	Reason         string `json:"reason"`
	FallbackReason string `json:"fallback_reason,omitempty"`
}

// Catalog is the persisted .config/fyr/models.json document.
type Catalog struct {
	UpdatedAt string              `json:"updated_at"`
	Entries   []CatalogEntry      `json:"entries"`
	Picks     map[string]RolePick `json:"picks"`
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

// CatalogPath returns <home>/.config/fyr/models.json.
func CatalogPath(home string) string {
	return filepath.Join(home, harness.StateDir, "models.json")
}

// CatalogExists reports whether a regular models.json file exists under home.
func CatalogExists(home string) bool {
	if home == "" {
		return false
	}
	st, err := os.Lstat(CatalogPath(home))
	return err == nil && st.Mode().IsRegular()
}

// LoadCatalog reads <home>/.config/fyr/models.json if present.
func LoadCatalog(home string) (Catalog, bool, error) {
	if home == "" {
		return Catalog{}, false, nil
	}
	p := CatalogPath(home)
	st, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return Catalog{}, false, nil
	}
	if err != nil {
		return Catalog{}, false, err
	}
	if !st.Mode().IsRegular() {
		return Catalog{}, false, fmt.Errorf("models catalog %s is not a regular file", p)
	}
	if st.Size() > 4<<20 {
		return Catalog{}, false, fmt.Errorf("models catalog %s exceeds 4 MiB", p)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return Catalog{}, false, err
	}
	var cat Catalog
	if err := json.Unmarshal(data, &cat); err != nil {
		return Catalog{}, false, err
	}
	for i := range cat.Entries {
		applyModelProfile(&cat.Entries[i])
	}
	return cat, true, nil
}

// SaveCatalog writes cat atomically (0600) to <home>/.config/fyr/models.json, refusing symlinks.
func SaveCatalog(home string, cat Catalog) error {
	if home == "" {
		return errors.New("save catalog: empty home")
	}
	dir := filepath.Dir(CatalogPath(home))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if st, err := os.Lstat(dir); err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing non-directory state path %s", dir)
	}
	target := CatalogPath(home)
	if st, err := os.Lstat(target); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing symlink %s", target)
	}
	tmp := target + ".tmp"
	if st, err := os.Lstat(tmp); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing symlink %s", tmp)
	}
	data, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, target)
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
	raw, err := runAgyModels(ctx, agyPath)
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

// BuildCatalog merges freshly parsed entries with optional previous state and evidence.
func BuildCatalog(now time.Time, parsed []CatalogEntry, prev *Catalog, evidence map[string]ModelEvidence) Catalog {
	today := now.UTC().Format("2006-01-02")
	entries := mergeCatalogEntries(today, parsed, prev, evidence)
	var prevPicks map[string]RolePick
	if prev != nil {
		prevPicks = prev.Picks
	}
	picks := make(map[string]RolePick, len(StandardRoleTiers))
	for _, rt := range StandardRoleTiers {
		key := rt[0] + ":" + rt[1]
		if p, err := pickFromEntries(entries, prevPicks, rt[0], rt[1]); err == nil {
			picks[key] = p
		}
	}
	return Catalog{
		UpdatedAt: now.UTC().Format(time.RFC3339),
		Entries:   entries,
		Picks:     picks,
	}
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
		applyModelProfile(&e)
		if old, ok := prevByID[e.ID]; ok {
			e.Status = "active"
			e.FirstSeen = nonEmpty(old.FirstSeen, today)
			e.ChangelogNote = nonEmpty(e.ChangelogNote, old.ChangelogNote)
			e.RetiringOn = nonEmpty(e.RetiringOn, old.RetiringOn)
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
			applyModelProfile(&old)
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
	default:
		return "", "", fmt.Errorf("unknown tier %q (want pro or flash)", tier)
	}
	return normRole, normTier, nil
}

func pickFromEntries(entries []CatalogEntry, prevPicks map[string]RolePick, role, tier string) (RolePick, error) {
	active := make([]CatalogEntry, 0, len(entries))
	eligible := make([]CatalogEntry, 0, len(entries))
	goneIDs := map[string]bool{}
	for _, e := range entries {
		applyModelProfile(&e)
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
	key := role + ":" + tier
	prevModel := ""
	if prev, ok := prevPicks[key]; ok {
		prevModel = prev.Model
	}
	if role == "judge" || role == "reviewer" || role == "verifier" {
		return pickCrossFamilyRole(eligible, goneIDs, prevModel, role, tier)
	}
	chosen, reason, fb := pickGeminiOrAny(eligible, goneIDs, prevModel, role, tier)
	return RolePick{Role: role, Tier: tier, Model: chosen.ID, Reason: reason, FallbackReason: fb}, nil
}
