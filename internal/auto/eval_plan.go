package auto

import (
	"hash/fnv"
	"slices"
	"strings"
)

const maxGateRows = maxEvalGateSessions / (2 * gateRuns)

func normalizeTargetSlug(name string) string {
	s := strings.Trim(strings.TrimSpace(strings.ReplaceAll(name, "\\", "/")), "/")
	if rest, ok := strings.CutPrefix(s, "skills/"); ok {
		slug, _, _ := strings.Cut(rest, "/")
		return slug
	}
	return s
}

func targetMatchAliases(name string) []string {
	slug := normalizeTargetSlug(name)
	if slug == "" {
		return nil
	}
	out := []string{slug}
	if base, ok := strings.CutPrefix(slug, "fyr-"); ok && base != "" {
		out = append(out, base)
		if base == "dream" {
			out = append(out, "dreamer")
		}
	} else if slug == "dream" {
		out = append(out, "dreamer")
	}
	return out
}

func rowNamesTarget(r EvalRow, name string) bool {
	for _, alias := range targetMatchAliases(name) {
		if r.Agent == alias || r.ID == alias || strings.HasPrefix(r.ID, alias+"-") {
			return true
		}
		if mentionsWord(r.Prompt, alias) || mentionsWord(r.Prompt2, alias) {
			return true
		}
	}
	return false
}

func rowExactTarget(r EvalRow, changed []string) bool {
	for _, name := range changed {
		for _, alias := range targetMatchAliases(name) {
			if r.ID == alias || r.Agent == alias {
				return true
			}
		}
	}
	return false
}

// hasMatchingEvalRows reports whether any row in suite names at least one
// target skill entry (used pre-dispatch so unmapped 0-row skills do not waste
// a worker slot or cooldown, ADR-534 F-046).
func hasMatchingEvalRows(suite []EvalRow, targets []string) bool {
	for _, r := range suite {
		if slices.ContainsFunc(targets, func(name string) bool { return rowNamesTarget(r, name) }) {
			return true
		}
	}
	return false
}

// selectGateRows builds a deterministic eval-gate row plan strictly bounded by
// maxRows (<= maxEvalGateSessions/(2*gateRuns)), prioritizing target rows and
// rotating holdout and regression subsets deterministically by changed targets.
func selectGateRows(suite, holdout []EvalRow, changed []string, maxRows int) []gateRow {
	if maxRows <= 0 {
		return nil
	}
	targets, regs := partitionSuiteRows(suite, changed)
	holdouts := make([]gateRow, 0, len(holdout))
	for _, r := range holdout {
		holdouts = append(holdouts, gateRow{ID: r.ID, IsHoldout: true, IsRegression: r.Kind == evalKindRegression})
	}
	rot := deterministicRotation(changed)
	pickedTargets := pickTargetSubset(targets, len(holdouts), maxRows, rot)
	rem := maxRows - len(pickedTargets)
	hCount, rCount := splitHoldoutAndRegSlots(len(holdouts), len(regs), rem)
	pickedHoldouts := takeRotated(holdouts, rot, hCount)
	pickedRegs := takeRotated(regs, rot, rCount)
	out := make([]gateRow, 0, len(pickedTargets)+len(pickedHoldouts)+len(pickedRegs))
	out = append(out, pickedTargets...)
	out = append(out, pickedHoldouts...)
	return append(out, pickedRegs...)
}

func partitionSuiteRows(suite []EvalRow, changed []string) (targets, regs []gateRow) {
	var exact, other []gateRow
	seen := map[string]bool{}
	for _, r := range suite {
		if !slices.ContainsFunc(changed, func(name string) bool { return rowNamesTarget(r, name) }) {
			continue
		}
		gr := gateRow{ID: r.ID, IsRegression: r.Kind == evalKindRegression}
		if rowExactTarget(r, changed) {
			exact = append(exact, gr)
		} else {
			other = append(other, gr)
		}
		seen[r.ID] = true
	}
	targets = append(exact, other...)
	for _, r := range suite {
		if r.Kind == evalKindRegression && !seen[r.ID] {
			regs = append(regs, gateRow{ID: r.ID, IsRegression: true})
			seen[r.ID] = true
		}
	}
	return targets, regs
}

func pickTargetSubset(targets []gateRow, holdoutCount, maxRows int, rot uint32) []gateRow {
	minHoldout := 0
	if holdoutCount > 0 && maxRows >= 2 {
		minHoldout = 1
	}
	targetCap := maxRows - minHoldout
	if len(targets) <= targetCap {
		return targets
	}
	if targetCap <= 0 {
		return nil
	}
	// Always keep the highest-priority target (index 0) and deterministically
	// rotate any additional targets beyond targetCap.
	out := []gateRow{targets[0]}
	if targetCap > 1 {
		out = append(out, takeRotated(targets[1:], rot, targetCap-1)...)
	}
	return out
}

func splitHoldoutAndRegSlots(holdouts, regs, rem int) (hCount, rCount int) {
	if rem <= 0 {
		return 0, 0
	}
	if holdouts > 0 && regs > 0 && rem >= 2 {
		hCount = min(holdouts, rem-1)
		rCount = min(regs, rem-hCount)
		if left := rem - hCount - rCount; left > 0 {
			hCount = min(holdouts, hCount+left)
		}
		return hCount, rCount
	}
	hCount = min(holdouts, rem)
	rCount = min(regs, rem-hCount)
	return hCount, rCount
}

func deterministicRotation(changed []string) uint32 {
	norm := make([]string, 0, len(changed))
	for _, c := range changed {
		if s := normalizeTargetSlug(c); s != "" {
			norm = append(norm, s)
		}
	}
	slices.Sort(norm)
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.Join(norm, ",")))
	return h.Sum32()
}

func takeRotated(rows []gateRow, rot uint32, count int) []gateRow {
	if count <= 0 || len(rows) == 0 {
		return nil
	}
	if count >= len(rows) {
		return slices.Clone(rows)
	}
	start := int(rot % uint32(len(rows)))
	out := make([]gateRow, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, rows[(start+i)%len(rows)])
	}
	return out
}
