package brain

import (
	"sort"
	"strings"

	"github.com/kesokaj/fyr/internal/search"
)

const (
	maxTurnFTSWords         = 8
	maxTurnPlanBodies       = 4
	maxTurnADRCandidates    = 8
	maxTurnMemoryCandidates = 8
)

var trivialTurnFTSWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true,
	"this": true, "that": true, "what": true, "when": true, "where": true,
	"which": true, "have": true, "has": true, "had": true, "yes": true,
	"okay": true, "all": true, "approved": true, "proceed": true, "continue": true,
	"done": true, "lgtm": true, "sgtm": true, "fix": true, "add": true,
	"update": true, "check": true, "test": true, "tests": true, "run": true,
	"file": true, "files": true, "code": true, "please": true, "thanks": true,
}

func normalizeTurnWord(raw string, minLen int) (string, bool) {
	w := strings.ToLower(strings.Trim(strings.TrimSpace(raw), ".,;:!?`'\"()[]{}"))
	if len(w) < minLen || trivialTurnFTSWords[w] || isAllASCIIDigits(w) {
		return "", false
	}
	return w, true
}

func filterTurnFTSWords(words []string) []string {
	if len(words) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(words))
	var out []string
	for _, raw := range words {
		w, ok := normalizeTurnWord(raw, 3)
		if !ok || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
		if len(out) >= maxTurnFTSWords {
			break
		}
	}
	return out
}

func normalizeTurnTitleWords(words []string) []string {
	if len(words) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(words))
	var out []string
	for _, raw := range words {
		w, ok := normalizeTurnWord(raw, 4)
		if !ok || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

func isAllASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

type turnBodyCounts struct {
	plans int
	adrs  int
	mems  int
}

func (c *turnBodyCounts) take(kind string) bool {
	switch kind {
	case "bug":
		return true
	case "plan":
		if c.plans < maxTurnPlanBodies {
			c.plans++
			return true
		}
	case "adr":
		if c.adrs < maxTurnADRCandidates {
			c.adrs++
			return true
		}
	case "memory":
		if c.mems < maxTurnMemoryCandidates {
			c.mems++
			return true
		}
	}
	return false
}

func selectTurnBodyRIDs(rows []turnMetaRow, hitRIDs map[int64]bool, rawWords []string) map[int64]bool {
	words := normalizeTurnTitleWords(rawWords)
	want := make(map[int64]bool, len(hitRIDs)+maxTurnPlanBodies+maxTurnADRCandidates+maxTurnMemoryCandidates)
	var counts turnBodyCounts
	for _, row := range rows {
		if hitRIDs[row.rid] || titleMatchesTurnWords(row.rec, words) || counts.take(row.rec.Kind) {
			want[row.rid] = true
		}
	}
	return want
}

func titleMatchesTurnWords(r Record, words []string) bool {
	if len(words) == 0 {
		return false
	}
	hay := strings.ToLower(r.ID + " " + r.Title)
	for _, w := range words {
		if len(w) >= 4 && strings.Contains(hay, w) {
			return true
		}
	}
	return false
}

func normalizeTurnRecordProvenance(r *Record) {
	if (r.Kind == "memory" || r.Kind == "feedback") && r.Provenance == "" {
		r.Provenance = "unknown"
	}
}

func sortTurnMetaRows(rows []turnMetaRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].rec.Date != rows[j].rec.Date {
			return rows[i].rec.Date > rows[j].rec.Date
		}
		return rows[i].rid > rows[j].rid
	})
}

func sortedTurnRIDs(wantRIDs map[int64]bool) []int64 {
	rids := make([]int64, 0, len(wantRIDs))
	for rid := range wantRIDs {
		rids = append(rids, rid)
	}
	sort.Slice(rids, func(i, j int) bool { return rids[i] < rids[j] })
	return rids
}

func mergeTurnRecords(metaRows []turnMetaRow, full map[int64]Record) []Record {
	out := make([]Record, len(metaRows))
	for i, m := range metaRows {
		if rec, ok := full[m.rid]; ok {
			out[i] = rec
		} else {
			out[i] = m.rec
		}
	}
	sort.Slice(out, func(i, j int) bool { return recordDescLess(out[i], out[j]) })
	return out
}

func formatTurnHit(h Hit, snip, proj string, global bool) Hit {
	h.Snippet = search.CleanSnippet(snip)
	h.Label = label(h.Scope, proj, global) + h.ID
	return h
}
