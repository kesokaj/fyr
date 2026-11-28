package models

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
)

type workEvTally struct {
	runs, pass int
	secSum     float64
	secCount   int
}

// AggregateWorkEvidence scans <home>/.config/fyr/work/*/status.json and merges extra evidence.
func AggregateWorkEvidence(home string, extra map[string]ModelEvidence) map[string]ModelEvidence {
	acc := map[string]*workEvTally{}
	scanWorkDirEvidence(home, acc)
	out := make(map[string]ModelEvidence, len(acc)+len(extra))
	for id, t := range acc {
		mean := 0.0
		if t.secCount > 0 {
			mean = t.secSum / float64(t.secCount)
		}
		out[id] = ModelEvidence{WorkRuns: t.runs, WorkPass: t.pass, WorkMeanSeconds: mean}
	}
	for id, ex := range extra {
		cur := out[id]
		mergeExactEvidence(&cur, ex)
		out[id] = cur
	}
	return out
}

func scanWorkDirEvidence(home string, acc map[string]*workEvTally) {
	if home == "" || CheckHomeDir(home) != nil {
		return
	}
	workDir := filepath.Join(home, harness.StateDir, "work")
	st, err := os.Lstat(workDir)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return
	}
	dirs, err := os.ReadDir(workDir)
	if err != nil {
		return
	}
	for _, d := range dirs {
		if d.IsDir() {
			mergeStatusFileEvidence(filepath.Join(workDir, d.Name(), "status.json"), acc)
		}
	}
}

func mergeStatusFileEvidence(path string, acc map[string]*workEvTally) {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 || st.Size() > 4<<20 {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var snap struct {
		Workers []struct {
			Model      string  `json:"model"`
			State      string  `json:"state"`
			ElapsedSec float64 `json:"elapsed_sec"`
			SetupSec   float64 `json:"setup_s"`
			AgySec     float64 `json:"agy_s"`
			CheckSec   float64 `json:"check_s"`
		} `json:"workers"`
	}
	if json.Unmarshal(data, &snap) != nil {
		return
	}
	for _, w := range snap.Workers {
		if w.Model == "" || (w.State != "PASS" && w.State != "FAIL") {
			continue
		}
		t := acc[w.Model]
		if t == nil {
			t = &workEvTally{}
			acc[w.Model] = t
		}
		t.runs++
		if w.State == "PASS" {
			t.pass++
		}
		if sec := max(w.ElapsedSec, w.SetupSec+w.AgySec+w.CheckSec); sec > 0 {
			t.secSum += sec
			t.secCount++
		}
	}
}

// joinCatalogEvidence maps empirical evidence onto catalog entry IDs, joining
// bare metrics.db model keys (without effort suffixes) onto the canonical
// highest-effort catalog entry while counting exact ID matches once.
func joinCatalogEvidence(entries []CatalogEntry, evidence map[string]ModelEvidence) map[string]ModelEvidence {
	if len(entries) == 0 || len(evidence) == 0 {
		return nil
	}
	exactActive, exactGone := indexCatalogEntryIDs(entries)
	keys := sortedEvidenceKeys(evidence)
	joined := make(map[string]ModelEvidence, len(entries))
	for _, k := range keys {
		idx, exact := routeEvidenceKey(entries, exactActive, exactGone, k)
		if idx < 0 {
			continue
		}
		targetID := entries[idx].ID
		cur := joined[targetID]
		ex := evidence[k]
		if exact {
			mergeExactEvidence(&cur, ex)
		} else {
			mergeStemEvidence(&cur, ex)
		}
		if cur != (ModelEvidence{}) {
			joined[targetID] = cur
		}
	}
	return joined
}

func indexCatalogEntryIDs(entries []CatalogEntry) (exactActive, exactGone map[string]int) {
	exactActive = make(map[string]int, len(entries))
	exactGone = make(map[string]int)
	for i, e := range entries {
		low := strings.ToLower(strings.TrimSpace(e.ID))
		if low == "" {
			continue
		}
		if e.Status == "gone" {
			if _, exists := exactGone[low]; !exists {
				exactGone[low] = i
			}
			continue
		}
		if _, exists := exactActive[low]; !exists {
			exactActive[low] = i
		}
	}
	return exactActive, exactGone
}

func sortedEvidenceKeys(evidence map[string]ModelEvidence) []string {
	keys := make([]string, 0, len(evidence))
	for k := range evidence {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func applyCatalogEvidence(entries []CatalogEntry, evidence map[string]ModelEvidence) {
	joined := joinCatalogEvidence(entries, evidence)
	for i := range entries {
		entries[i].Evidence = joined[entries[i].ID]
	}
}

// BuildListedCatalogEntries constructs active CatalogEntries for ids enriched
// with any persisted empirical evidence from <home>/.config/fyr/models.json.
func BuildListedCatalogEntries(home string, ids []string) []CatalogEntry {
	entries, _ := buildListedCatalogEntriesAndPools(home, ids)
	return entries
}

func buildListedCatalogEntriesAndPools(home string, ids []string) ([]CatalogEntry, []Pool) {
	listedSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		listedSet[id] = true
	}
	cachedEv := map[string]ModelEvidence{}
	var pools []Pool
	if home != "" {
		if cat, ok, err := LoadCatalog(home); err == nil && ok {
			pools = append([]Pool(nil), cat.Pools...)
			for _, e := range cat.Entries {
				if e.Status != "gone" || listedSet[e.ID] {
					cachedEv[e.ID] = e.Evidence
				}
			}
		}
	}
	entries := make([]CatalogEntry, 0, len(ids))
	for _, id := range ids {
		fam, tr, eff := ClassifyModel(id, "")
		entry := CatalogEntry{
			ID:     id,
			Family: fam,
			Tier:   tr,
			Effort: eff,
			Status: "active",
		}
		ApplyModelProfile(&entry)
		entries = append(entries, entry)
	}
	applyCatalogEvidence(entries, cachedEv)
	populateEntryScores(entries, pools)
	return entries, pools
}

func mergeStemEvidence(dst *ModelEvidence, ex ModelEvidence) {
	prevRatings := dst.RatingRuns
	dst.EvalRuns += ex.EvalRuns
	dst.EvalPass += ex.EvalPass
	dst.TrialRuns += ex.TrialRuns
	dst.TrialPass += ex.TrialPass
	dst.JudgeRuns += ex.JudgeRuns
	dst.JudgePass += ex.JudgePass
	dst.RatingRuns += ex.RatingRuns
	dst.RatingScoreSum += ex.RatingScoreSum
	dst.DecayedRuns += ex.DecayedRuns
	dst.DecayedSuccesses += ex.DecayedSuccesses
	mergeRoleEvidence(&dst.BuilderEvidence, ex.BuilderEvidence)
	mergeRoleEvidence(&dst.JudgeEvidence, ex.JudgeEvidence)
	if ex.MeanTokenEfficiency > 0 {
		eff := ex.MeanTokenEfficiency
		if dst.MeanTokenEfficiency > 0 && prevRatings > 0 && ex.RatingRuns > 0 {
			tot := float64(prevRatings + ex.RatingRuns)
			eff = (dst.MeanTokenEfficiency*float64(prevRatings) + eff*float64(ex.RatingRuns)) / tot
		}
		dst.MeanTokenEfficiency = eff
	}
	if dst.MeanLatencySeconds <= 0 && ex.MeanLatencySeconds > 0 {
		dst.MeanLatencySeconds = ex.MeanLatencySeconds
	}
}

func mergeRoleEvidence(dst *RoleEvidence, ex RoleEvidence) {
	dst.Runs += ex.Runs
	dst.Pass += ex.Pass
	dst.DecayedRuns += ex.DecayedRuns
	dst.DecayedSuccesses += ex.DecayedSuccesses
}

func mergeExactEvidence(dst *ModelEvidence, ex ModelEvidence) {
	mergeStemEvidence(dst, ex)
	if ex.WorkRuns > dst.WorkRuns {
		dst.WorkRuns = ex.WorkRuns
		dst.WorkPass = ex.WorkPass
		dst.WorkMeanSeconds = ex.WorkMeanSeconds
	} else if dst.WorkMeanSeconds == 0 && ex.WorkMeanSeconds > 0 {
		dst.WorkMeanSeconds = ex.WorkMeanSeconds
	}
}

// HasZeroEvidence reports whether ev carries zero recorded runs across all sources.
func HasZeroEvidence(ev ModelEvidence) bool {
	return TotalEvidenceRuns(ev) == 0 && ev.DecayedRuns <= 0
}

// TotalEvidenceRuns counts observed runs across work, trials/evals, judge verdicts, and ratings.
func TotalEvidenceRuns(ev ModelEvidence) int {
	return max(ev.WorkRuns, 0) + max(max(ev.EvalRuns, ev.TrialRuns), 0) + max(ev.JudgeRuns, 0) + max(ev.RatingRuns, 0)
}

func routeEvidenceKey(entries []CatalogEntry, exactActive, exactGone map[string]int, key string) (int, bool) {
	low := strings.ToLower(strings.TrimSpace(key))
	if low == "" {
		return -1, false
	}
	if idx, ok := exactActive[low]; ok {
		return idx, true
	}
	stem, hasEffort := bareModelStem(low)
	if !hasEffort && stem != "" {
		if idx := bestBareStemMatch(entries, stem, false); idx >= 0 {
			return idx, false
		}
	}
	if idx, ok := exactGone[low]; ok {
		return idx, true
	}
	if !hasEffort && stem != "" {
		if idx := bestBareStemMatch(entries, stem, true); idx >= 0 {
			return idx, false
		}
	}
	return -1, false
}

func bestBareStemMatch(entries []CatalogEntry, stem string, wantGone bool) int {
	if idx := matchBareStemPass(entries, stem, wantGone, false); idx >= 0 {
		return idx
	}
	return matchBareStemPass(entries, stem, wantGone, true)
}

func matchBareStemPass(entries []CatalogEntry, stem string, wantGone, foldDots bool) int {
	want := stem
	if foldDots {
		want = strings.ReplaceAll(want, ".", "-")
	}
	best := -1
	for i, e := range entries {
		if (e.Status == "gone") != wantGone {
			continue
		}
		candStem, _ := bareModelStem(e.ID)
		if foldDots {
			candStem = strings.ReplaceAll(candStem, ".", "-")
		}
		if candStem != want {
			continue
		}
		if best < 0 || betterCanonicalEntry(e, entries[best]) {
			best = i
		}
	}
	return best
}

func bareModelStem(id string) (string, bool) {
	low := strings.ToLower(strings.TrimSpace(id))
	low = strings.TrimPrefix(low, "models/")
	for _, suf := range []string{"-xhigh", "-max", "-high", "-medium", "-low", "-minimal"} {
		if strings.HasSuffix(low, suf) {
			if base := strings.TrimSuffix(low, suf); base != "" {
				return base, true
			}
		}
	}
	return low, false
}

func entryEffort(e CatalogEntry) string {
	if e.Effort != "" {
		return strings.ToLower(strings.TrimSpace(e.Effort))
	}
	_, _, eff := ClassifyModel(e.ID, e.DisplayName)
	return eff
}

func canonicalEffortRank(effort string) int {
	switch effort {
	case "max":
		return 5
	case "xhigh":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func betterCanonicalEntry(a, b CatalogEntry) bool {
	if ra, rb := canonicalEffortRank(entryEffort(a)), canonicalEffortRank(entryEffort(b)); ra != rb {
		return ra > rb
	}
	if cmp := compareModelVersions(a.ID, b.ID); cmp != 0 {
		return cmp > 0
	}
	return a.ID < b.ID
}
