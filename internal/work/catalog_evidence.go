package work

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
	if home != "" {
		workDir := filepath.Join(home, harness.StateDir, "work")
		if dirs, err := os.ReadDir(workDir); err == nil {
			for _, d := range dirs {
				if d.IsDir() {
					mergeStatusFileEvidence(filepath.Join(workDir, d.Name(), "status.json"), acc)
				}
			}
		}
	}
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
// bare metrics.db eval trial model keys (without effort suffixes) onto the
// canonical highest-effort catalog entry while counting exact ID matches once.
func joinCatalogEvidence(entries []CatalogEntry, evidence map[string]ModelEvidence) map[string]ModelEvidence {
	if len(entries) == 0 || len(evidence) == 0 {
		return nil
	}
	exactActive := make(map[string]int, len(entries))
	exactGone := make(map[string]int)
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

	keys := make([]string, 0, len(evidence))
	for k := range evidence {
		keys = append(keys, k)
	}
	sort.Strings(keys)

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
			cur.EvalRuns += ex.EvalRuns
			cur.EvalPass += ex.EvalPass
		}
		if cur != (ModelEvidence{}) {
			joined[targetID] = cur
		}
	}
	return joined
}

func applyCatalogEvidence(entries []CatalogEntry, evidence map[string]ModelEvidence) {
	joined := joinCatalogEvidence(entries, evidence)
	for i := range entries {
		entries[i].Evidence = joined[entries[i].ID]
	}
}

func buildListedCatalogEntries(home string, ids []string) []CatalogEntry {
	listedSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		listedSet[id] = true
	}
	cachedEv := map[string]ModelEvidence{}
	if home != "" {
		if cat, ok, err := LoadCatalog(home); err == nil && ok {
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
		entries = append(entries, CatalogEntry{
			ID:     id,
			Family: fam,
			Tier:   tr,
			Effort: eff,
			Status: "active",
		})
	}
	applyCatalogEvidence(entries, cachedEv)
	return entries
}

func mergeExactEvidence(dst *ModelEvidence, ex ModelEvidence) {
	dst.EvalRuns += ex.EvalRuns
	dst.EvalPass += ex.EvalPass
	if ex.WorkRuns > dst.WorkRuns {
		dst.WorkRuns = ex.WorkRuns
		dst.WorkPass = ex.WorkPass
		dst.WorkMeanSeconds = ex.WorkMeanSeconds
	} else if dst.WorkMeanSeconds == 0 && ex.WorkMeanSeconds > 0 {
		dst.WorkMeanSeconds = ex.WorkMeanSeconds
	}
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
	for _, suf := range []string{"-high", "-medium", "-low"} {
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
