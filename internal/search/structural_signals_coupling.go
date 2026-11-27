package search

import (
	"database/sql"
	"path"
	"sort"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/redact"
)

type hiddenCouplingSummary struct {
	hints        []HiddenCouplingHint
	totalPairs   int
	editPairs    int
	readPairs    int
	totalCoPairs int
}

type pairKindKey struct {
	a, b, kind string
}

func summarizeHiddenCoupling(db *sql.DB, repoRoot string, minCount int, astLinked map[[2]string]bool) hiddenCouplingSummary {
	byKind, allPairs := scanCoActivityByPairKind(db, repoRoot, minCount)
	if len(byKind) == 0 {
		return hiddenCouplingSummary{}
	}
	var hidden []HiddenCouplingHint
	totalMissing := map[[2]string]bool{}
	editMissing := map[[2]string]bool{}
	readMissing := map[[2]string]bool{}
	for k, h := range byKind {
		pair := [2]string{k.a, k.b}
		if astLinked[pair] {
			continue
		}
		hidden = append(hidden, h)
		totalMissing[pair] = true
		switch k.kind {
		case "co_modified", "co_edit":
			editMissing[pair] = true
		case "co_read":
			readMissing[pair] = true
		}
	}
	sortHiddenCouplingHints(hidden)
	return hiddenCouplingSummary{
		hints:        hidden,
		totalPairs:   len(totalMissing),
		editPairs:    len(editMissing),
		readPairs:    len(readMissing),
		totalCoPairs: len(allPairs),
	}
}

func scanCoActivityByPairKind(db *sql.DB, repoRoot string, minCount int) (map[pairKindKey]HiddenCouplingHint, map[[2]string]bool) {
	rows, err := db.Query(`SELECT file_a, file_b, kind, count FROM co_activity WHERE count >= ? ORDER BY count DESC, file_a, file_b`, minCount)
	if err != nil {
		return nil, nil
	}
	defer rows.Close()
	canonRoot := CanonicalizePath("", repoRoot)
	best := map[pairKindKey]HiddenCouplingHint{}
	allPairs := map[[2]string]bool{}
	for rows.Next() {
		var a, b, kind string
		var cnt int
		if rows.Scan(&a, &b, &kind, &cnt) != nil {
			continue
		}
		ra, rb := normalizeRepoRel(repoRoot, canonRoot, a), normalizeRepoRel(repoRoot, canonRoot, b)
		if ra == "" || rb == "" || ra == rb || isExcludedASTPath(ra) || isExcludedASTPath(rb) {
			continue
		}
		if harness.SecretShaped(path.Base(ra)) || harness.SecretShaped(path.Base(rb)) {
			continue
		}
		if ra > rb {
			ra, rb = rb, ra
		}
		allPairs[[2]string{ra, rb}] = true
		pk := pairKindKey{a: ra, b: rb, kind: kind}
		if cur, exists := best[pk]; !exists || cnt > cur.Count {
			best[pk] = HiddenCouplingHint{
				FileA: printable(redact.Redact(ra)),
				FileB: printable(redact.Redact(rb)),
				Kind:  printable(kind),
				Count: cnt,
			}
		}
	}
	return best, allPairs
}

func queryHiddenCouplingPairs(db *sql.DB, repoRoot string, minCount int, astLinked map[[2]string]bool) []HiddenCouplingHint {
	return summarizeHiddenCoupling(db, repoRoot, minCount, astLinked).hints
}

func sortHiddenCouplingHints(hidden []HiddenCouplingHint) {
	sort.Slice(hidden, func(i, j int) bool {
		if hidden[i].Count != hidden[j].Count {
			return hidden[i].Count > hidden[j].Count
		}
		if hidden[i].Kind != hidden[j].Kind {
			if hidden[i].Kind == "co_modified" || hidden[j].Kind == "co_modified" {
				return hidden[i].Kind == "co_modified"
			}
			return hidden[i].Kind < hidden[j].Kind
		}
		if hidden[i].FileA != hidden[j].FileA {
			return hidden[i].FileA < hidden[j].FileA
		}
		return hidden[i].FileB < hidden[j].FileB
	})
}
