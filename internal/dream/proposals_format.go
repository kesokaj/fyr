package dream

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/kesokaj/fyr/internal/trail"
)

const (
	leftOutPrefix          = "Left out: "
	maxDroppedHeaderBytes  = 1024
	maxDroppedSummaryBytes = maxDroppedHeaderBytes - len(leftOutPrefix)
	maxDroppedSampleBytes  = 100
	maxDroppedKindsBytes   = 360
	maxDroppedKindBytes    = 32
)

// FormatLeftOutHeader returns a bounded "Left out: ..." header line for
// dropped items after applying clean (when non-nil), or "" when empty.
func FormatLeftOutHeader(dropped []string, clean func(string) string) string {
	if len(dropped) == 0 {
		return ""
	}
	return leftOutPrefix + formatDroppedHeader(cleanDropped(dropped, clean))
}

func cleanDropped(dropped []string, clean func(string) string) []string {
	if clean == nil {
		return dropped
	}
	out := make([]string, len(dropped))
	for i, d := range dropped {
		out[i] = clean(d)
	}
	return out
}

func formatDroppedHeader(dropped []string) string {
	if len(dropped) == 0 {
		return ""
	}
	prefix := fmt.Sprintf("%d (%s)", len(dropped), formatDroppedKinds(dropped, maxDroppedKindsBytes))
	limit := min(len(dropped), shownDropped)
	samples := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		samples = append(samples, formatDroppedSample(dropped[i]))
	}
	out := fmt.Sprintf("%s; + %d more", prefix, len(dropped))
	for k := 1; k <= len(samples); k++ {
		cand := prefix + ": " + strings.Join(samples[:k], "; ")
		if rest := len(dropped) - k; rest > 0 {
			cand += fmt.Sprintf("; + %d more", rest)
		}
		if len(cand) > maxDroppedSummaryBytes {
			break
		}
		out = cand
	}
	return strings.TrimSpace(trail.TruncateUTF8(out, maxDroppedSummaryBytes))
}

func formatDroppedKinds(dropped []string, maxBytes int) string {
	counts := map[string]int{}
	for _, d := range dropped {
		counts[droppedKind(d)]++
	}
	kinds := slices.Sorted(maps.Keys(counts))
	var b strings.Builder
	for i, k := range kinds {
		part := fmt.Sprintf("%s %d", k, counts[k])
		sep, tail := "", ""
		if b.Len() > 0 {
			sep = ", "
		}
		if rem := len(kinds) - (i + 1); rem > 0 {
			tail = fmt.Sprintf(", +%d kinds", rem)
		}
		if b.Len()+len(sep)+len(part)+len(tail) > maxBytes && b.Len() > 0 {
			fmt.Fprintf(&b, ", +%d kinds", len(kinds)-i)
			break
		}
		b.WriteString(sep + part)
	}
	return b.String()
}

func droppedKind(raw string) string {
	s := strings.TrimSpace(strings.ToValidUTF8(raw, "\ufffd"))
	if s == "" {
		return "unknown"
	}
	if strings.HasPrefix(s, "[redacted by fyr]") {
		return "redacted"
	}
	cut := strings.IndexFunc(s, func(r rune) bool { return r == ':' || unicode.IsSpace(r) })
	if cut == 0 {
		return "unknown"
	}
	if cut > 0 {
		s = s[:cut]
	}
	if s = strings.TrimSpace(trail.TruncateUTF8(s, maxDroppedKindBytes)); s == "" {
		return "unknown"
	}
	return s
}

func formatDroppedSample(raw string) string {
	s := strings.Join(strings.Fields(strings.ToValidUTF8(raw, "\ufffd")), " ")
	if s == "" {
		return "(empty)"
	}
	if len(s) <= maxDroppedSampleBytes {
		return s
	}
	head := strings.TrimSpace(trail.TruncateUTF8(s, maxDroppedSampleBytes-3))
	if head == "" {
		return strings.TrimSpace(trail.TruncateUTF8(s, maxDroppedSampleBytes))
	}
	return head + "..."
}

func cleanReportDate(raw string) string {
	s := strings.TrimSpace(raw)
	if len(s) != 10 {
		return ""
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return ""
	}
	return s
}

func cleanActionScope(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "auto_skill", "operator_code", "operator_rule", "propose_only":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return ""
	}
}

// section is one proposal's text. The model's text is indented four spaces,
// line by line, so it reads as quoted data, never as headings or fields of
// the record (audit 2026-09-30, SD3).
func (o Options) section(p proposal) string {
	target := "project"
	if strings.ToLower(o.line(p.Target)) == "harness" {
		target = "harness"
	}
	kind := strings.ToLower(o.line(p.Kind))
	return fmt.Sprintf("Target: %s   Kind: %s   File: %s\n%s\n%s\n\nEvidence: %s\n",
		target, kind, o.line(p.File), o.proposalMetaLines(p), indent(strings.TrimSpace(o.clean(p.Text))), o.line(p.Evidence))
}

func (o Options) proposalMetaLines(p proposal) string {
	var b strings.Builder
	if sig := cleanSignalID(o.line(p.SignalID)); sig != "" {
		b.WriteString("Signal: " + sig + "\n")
	}
	if day := cleanReportDate(o.line(p.ReportDate)); day != "" {
		b.WriteString("ReportDate: " + day + "\n")
	}
	if scope := cleanActionScope(o.line(p.ActionScope)); scope != "" {
		b.WriteString("ActionScope: " + scope + "\n")
	}
	if act := o.line(p.RecommendedAction); act != "" {
		b.WriteString("RecommendedAction: " + act + "\n")
	}
	return b.String()
}
