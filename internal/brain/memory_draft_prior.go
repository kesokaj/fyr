package brain

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maxPriorEntries     = 2
	maxMergedBodyBytes  = 2000
	maxSinglePriorBytes = 600
)

var priorHeaderRe = regexp.MustCompile(`(?m)^Prior \(([a-z]+)\):\s*\n?`)

type priorEntry struct {
	prov string
	text string
}

func mergeMemoryBody(text, curBody, curProv, normType string) string {
	incomingMain, incomingPriors := splitMemoryMainAndPriors(text)
	curMain, curPriors := splitMemoryMainAndPriors(curBody)
	if curProv == "operator" {
		return mergeOperatorMemoryBody(curMain, curPriors, incomingMain, incomingPriors, normType)
	}
	head := ensureNormalizedMemoryBody(incomingMain, normType)
	priors := selectPriorEntries(curMain, incomingMain, curProv, append(incomingPriors, curPriors...))
	return formatBoundedMemoryBody(head, priors)
}

func mergeOperatorMemoryBody(curMain string, curPriors []priorEntry, incomingMain string, incomingPriors []priorEntry, normType string) string {
	head := ensureNormalizedMemoryBody(curMain, normType)
	var candidates []priorEntry
	if incomingMain != "" {
		candidates = append(candidates, priorEntry{prov: "derived", text: incomingMain})
	}
	candidates = append(candidates, incomingPriors...)
	candidates = append(candidates, curPriors...)
	priors := dedupAndCapPriors(curMain, candidates)
	return formatBoundedOperatorMemoryBody(head, priors)
}

func splitMemoryMainAndPriors(raw string) (string, []priorEntry) {
	norm := strings.ReplaceAll(strings.TrimSpace(raw), "\r\n", "\n")
	if norm == "" {
		return "", nil
	}
	chunks := strings.Split(norm, "\n---\n")
	mainText := stripLeadingTypeLine(chunks[0])
	var priors []priorEntry
	for _, chunk := range chunks[1:] {
		priors = append(priors, parsePriorChunk(chunk)...)
	}
	return mainText, priors
}

func stripLeadingTypeLine(s string) string {
	s = strings.TrimSpace(s)
	if m := memoryType.FindStringSubmatchIndex(s); m != nil {
		return strings.TrimSpace(s[m[1]:])
	}
	return s
}

func parsePriorChunk(chunk string) []priorEntry {
	chunk = strings.TrimSpace(chunk)
	if chunk == "" {
		return nil
	}
	locs := priorHeaderRe.FindAllStringSubmatchIndex(chunk, -1)
	if len(locs) == 0 {
		if body := stripLeadingTypeLine(chunk); body != "" {
			return []priorEntry{{prov: "unknown", text: body}}
		}
		return nil
	}
	var out []priorEntry
	for i, loc := range locs {
		end := len(chunk)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		prov := chunk[loc[2]:loc[3]]
		body := stripLeadingTypeLine(chunk[loc[1]:end])
		if body != "" {
			out = append(out, priorEntry{prov: prov, text: body})
		}
	}
	return out
}

func selectPriorEntries(curMain, newMain, curProv string, existing []priorEntry) []priorEntry {
	prevProv := curProv
	if prevProv == "" {
		prevProv = "unknown"
	}
	if prevProv == "operator" && hasOperatorPrior(existing) {
		prevProv = "derived"
	}
	var candidates []priorEntry
	if curMain != "" {
		candidates = append(candidates, priorEntry{prov: prevProv, text: curMain})
	}
	candidates = append(candidates, existing...)
	return dedupAndCapPriors(newMain, candidates)
}

func dedupAndCapPriors(primaryMain string, candidates []priorEntry) []priorEntry {
	seen := map[string]bool{strings.TrimSpace(primaryMain): true}
	var dedup []priorEntry
	for _, p := range candidates {
		t := truncateUTF8Bytes(strings.TrimSpace(p.text), maxSinglePriorBytes)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		dedup = append(dedup, priorEntry{prov: p.prov, text: t})
	}
	return capPriorsPreservingOperator(dedup)
}

func hasOperatorPrior(priors []priorEntry) bool {
	for _, p := range priors {
		if p.prov == "operator" {
			return true
		}
	}
	return false
}

func capPriorsPreservingOperator(dedup []priorEntry) []priorEntry {
	if len(dedup) <= maxPriorEntries {
		return dedup
	}
	kept := dedup[:maxPriorEntries]
	if hasOperatorPrior(kept) {
		return kept
	}
	for i := len(dedup) - 1; i >= maxPriorEntries; i-- {
		if dedup[i].prov == "operator" {
			return []priorEntry{dedup[0], dedup[i]}
		}
	}
	return kept
}

func formatPriorTail(priors []priorEntry) string {
	var parts []string
	for _, p := range priors {
		parts = append(parts, "Prior ("+p.prov+"):\n"+p.text)
	}
	return "\n\n---\n" + strings.Join(parts, "\n\n")
}

func formatBoundedOperatorMemoryBody(head string, priors []priorEntry) string {
	if len(priors) == 0 {
		return head
	}
	tail := formatPriorTail(priors)
	if len(head)+len(tail) <= maxMergedBodyBytes {
		return head + tail
	}
	rem := maxMergedBodyBytes - len(head)
	if rem <= len("\n\n---\nPrior (x):\n") {
		return head
	}
	return head + truncateUTF8Bytes(tail, rem)
}

func formatBoundedMemoryBody(head string, priors []priorEntry) string {
	if len(priors) == 0 {
		return truncateUTF8Bytes(head, maxMergedBodyBytes)
	}
	tail := formatPriorTail(priors)
	if len(head)+len(tail) <= maxMergedBodyBytes {
		return head + tail
	}
	const minTailBudget = 320
	if len(head) > maxMergedBodyBytes-minTailBudget {
		head = truncateUTF8Bytes(head, maxMergedBodyBytes-minTailBudget)
	}
	rem := maxMergedBodyBytes - len(head)
	if rem <= len("\n\n---\nPrior (x):\n") {
		return truncateUTF8Bytes(head, maxMergedBodyBytes)
	}
	return head + truncateUTF8Bytes(tail, rem)
}

func truncateUTF8Bytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	if maxBytes <= 0 {
		return ""
	}
	s = s[:maxBytes]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return strings.TrimRight(s, " \t\r\n")
}
