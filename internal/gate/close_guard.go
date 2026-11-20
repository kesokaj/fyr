package gate

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
)

var (
	quotedLiteralRe = regexp.MustCompile(`"(?:\\.|[^"\\\n])*"`)
	tagDirectiveRe  = regexp.MustCompile(`(?i)(?:/fyr-preflight\s+|\bgit\s+tag(?:\s+-[a-zA-Z0-9]+)*\s+|\b(?:cut|ship|release)\s+(?:tag\s+)?)(v\d+\.\d+\.\d+)\b`)
	earliestDateRe  = regexp.MustCompile(`(?i)\bearliest(?::\s*|\s+)(\d{4}-\d{2}-\d{2})\b`)
	boxEarliestRe   = regexp.MustCompile(`(?i)(?:\s*[(\[]\s*earliest(?::\s*|\s+)\d{4}-\d{2}-\d{2}[^)\]]*[)\]]|\s*[;,]?\s*\bearliest(?::\s*|\s+)\d{4}-\d{2}-\d{2}\b)`)
	neverClauseRe   = regexp.MustCompile(`(?:^|[\s.;(])Never:`)
	kickoffHeaderRe = regexp.MustCompile(`(?i)\b(?:Project|Files|Model|Base-check|Judge|Judge-model|Done when|Standing approvals(?:\s*\([^)]*\))?):`)
)

func validateCloseKickoff(ctx context.Context, top string, opts CloseOptions) error {
	kickoff := extractKickoffSection(opts.Body)
	if strings.TrimSpace(kickoff) == "" {
		return nil
	}
	nonNever := stripNeverClauses(kickoff)
	unquoted := quotedLiteralRe.ReplaceAllString(nonNever, " ")
	if err := checkKickoffTags(ctx, top, unquoted); err != nil {
		return err
	}
	today := currentCloseDate(opts.Now)
	if err := checkKickoffEarliestDate(nonNever, today); err != nil {
		return err
	}
	return checkOpenPlansEarliest(opts.Home, top, nonNever, today)
}

func extractKickoffSection(body string) string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	inSection := false
	inFence := false
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inSection {
			if strings.HasPrefix(trimmed, "```") {
				inFence = !inFence
				continue
			}
			if !inFence && (trimmed == "## Kickoff" || strings.HasPrefix(trimmed, "## Kickoff ")) {
				inSection = true
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			out = append(out, line)
			continue
		}
		if !inFence && strings.HasPrefix(trimmed, "## ") {
			break
		}
		out = append(out, line)
	}
	if !inSection {
		return ""
	}
	return strings.Join(out, "\n")
}

func stripNeverClauses(kickoff string) string {
	lines := strings.Split(strings.ReplaceAll(kickoff, "\r\n", "\n"), "\n")
	var out []string
	inNever := false
	neverIndent := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if inNever {
			if isNeverContinuation(line, trimmed, neverIndent) {
				continue
			}
			inNever = false
		}
		kept, started := stripLineNever(line)
		if started {
			inNever = true
			neverIndent = leadingSpaces(line)
		}
		out = append(out, kept)
	}
	return strings.Join(out, "\n")
}

func isNeverContinuation(line, trimmed string, neverIndent int) bool {
	if trimmed == "" || strings.HasPrefix(trimmed, "```") {
		return false
	}
	if loc := kickoffHeaderRe.FindStringIndex(trimmed); loc != nil && loc[0] == 0 {
		return false
	}
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
		return true
	}
	return leadingSpaces(line) > neverIndent
}

func stripLineNever(line string) (string, bool) {
	for {
		loc := neverClauseRe.FindStringIndex(line)
		if loc == nil {
			return line, false
		}
		neverStart := loc[1] - len("Never:")
		after := line[loc[1]:]
		if h := kickoffHeaderRe.FindStringIndex(after); h != nil {
			line = line[:neverStart] + " " + after[h[0]:]
			continue
		}
		standalone := strings.TrimSpace(line[:neverStart]) == ""
		return line[:neverStart], standalone
	}
}

func leadingSpaces(s string) int {
	return len(s) - len(strings.TrimLeft(s, " \t"))
}

func checkKickoffTags(ctx context.Context, top, unquoted string) error {
	matches := tagDirectiveRe.FindAllStringSubmatch(unquoted, -1)
	if len(matches) == 0 {
		return nil
	}
	out, err := gitBytes(ctx, top, "tag", "--list")
	if err != nil {
		return err
	}
	tags := parseTagSet(out)
	for _, m := range matches {
		if existing, ok := matchExistingTag(tags, m[1]); ok {
			return refusef("kickoff schedules already-existing git tag %s (verify with live `git tag`)", existing)
		}
	}
	return nil
}

func parseTagSet(out []byte) map[string]string {
	tags := map[string]string{}
	for _, line := range bytes.Split(out, []byte{'\n'}) {
		t := strings.TrimSpace(string(line))
		if t == "" {
			continue
		}
		tags[t] = t
		low := strings.ToLower(t)
		if _, exists := tags[low]; !exists {
			tags[low] = t
		}
	}
	return tags
}

func matchExistingTag(tags map[string]string, tag string) (string, bool) {
	if existing, ok := tags[tag]; ok {
		return existing, true
	}
	if existing, ok := tags[strings.ToLower(tag)]; ok {
		return existing, true
	}
	return "", false
}

func currentCloseDate(nowFn func() time.Time) string {
	if nowFn == nil {
		nowFn = time.Now
	}
	return nowFn().Format("2006-01-02")
}

func checkKickoffEarliestDate(nonNever, today string) error {
	for _, m := range earliestDateRe.FindAllStringSubmatch(nonNever, -1) {
		date := m[1]
		if !isValidISODate(date) {
			continue
		}
		if date > today {
			return refusef("kickoff schedules work before earliest date %s (today is %s)", date, today)
		}
	}
	return nil
}

func isValidISODate(date string) bool {
	_, err := time.Parse("2006-01-02", date)
	return err == nil
}

func checkOpenPlansEarliest(home, top, nonNever, today string) error {
	recs, ok, err := brain.ReadScope(home, top)
	if err != nil || !ok {
		return nil
	}
	for _, r := range recs {
		if r.Kind != "plan" || r.Status != "open" || !containsPlanID(nonNever, r.ID) {
			continue
		}
		if err := matchOpenPlanPhaseBoxes(r, nonNever, today); err != nil {
			return err
		}
	}
	return nil
}

func matchOpenPlanPhaseBoxes(r brain.Record, nonNever, today string) error {
	inFence := false
	phaseID := ""
	for _, line := range strings.Split(strings.ReplaceAll(r.Body, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if strings.HasPrefix(trimmed, "### Phase ") {
			phaseID = parsePhaseLabel(strings.TrimPrefix(trimmed, "### "))
			continue
		}
		if strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "### ") {
			phaseID = ""
			continue
		}
		if !strings.HasPrefix(trimmed, "- [ ] ") && !strings.HasPrefix(trimmed, "* [ ] ") {
			continue
		}
		boxText := strings.TrimSpace(trimmed[len("- [ ] "):])
		if date := futurePlanEarliestDate(boxText, today); date != "" && kickoffTargetsPhaseOrBox(nonNever, phaseID, boxText) {
			phaseName := phaseID
			if phaseName == "" {
				phaseName = "open task"
			}
			return refusef("kickoff schedules %s (%s) before earliest date %s (today is %s)", r.ID, phaseName, date, today)
		}
	}
	return nil
}

func futurePlanEarliestDate(boxText, today string) string {
	for _, m := range earliestDateRe.FindAllStringSubmatch(boxText, -1) {
		if isValidISODate(m[1]) && m[1] > today {
			return m[1]
		}
	}
	return ""
}

func parsePhaseLabel(hdr string) string {
	title := strings.TrimSpace(hdr)
	if idx := strings.LastIndex(title, "("); idx > 0 && strings.HasSuffix(title, ")") {
		inner := strings.TrimSpace(title[idx+1 : len(title)-1])
		if strings.HasPrefix(strings.ToLower(inner), "after:") {
			title = strings.TrimSpace(title[:idx])
		}
	}
	fields := strings.Fields(title)
	if len(fields) >= 2 && strings.EqualFold(fields[0], "phase") {
		if num := strings.TrimRight(fields[1], ":—-"); num != "" {
			return "Phase " + num
		}
	}
	return title
}

func containsPlanID(text, id string) bool {
	if id == "" {
		return false
	}
	for start := 0; start < len(text); {
		idx := strings.Index(text[start:], id)
		if idx < 0 {
			return false
		}
		pos := start + idx
		end := pos + len(id)
		leftOK := pos == 0 || (!isPlanIDChar(text[pos-1]) && text[pos-1] != '.')
		rightOK := end == len(text) || (!isPlanIDChar(text[end]) && !(text[end] == '.' && end+1 < len(text) && isPlanIDAlnum(text[end+1])))
		if leftOK && rightOK {
			return true
		}
		start = pos + 1
	}
	return false
}

func isPlanIDAlnum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func isPlanIDChar(b byte) bool { return isPlanIDAlnum(b) || b == '-' || b == '_' }

func isWordByte(b byte) bool { return isPlanIDAlnum(b) || b == '_' }

func kickoffTargetsPhaseOrBox(nonNever, phaseID, boxText string) bool {
	if phaseID != "" && containsTokenBoundary(nonNever, phaseID) {
		return true
	}
	lowKickoff := strings.ToLower(nonNever)
	if boxText != "" && strings.Contains(lowKickoff, strings.ToLower(boxText)) {
		return true
	}
	core := stripBoxMetadata(boxText)
	if core == "" {
		return false
	}
	if len(core) >= 6 && strings.Contains(lowKickoff, strings.ToLower(core)) {
		return true
	}
	return containsTokenBoundary(nonNever, core)
}

func containsTokenBoundary(text, tok string) bool {
	lowText, lowTok := strings.ToLower(text), strings.ToLower(tok)
	if lowTok == "" {
		return false
	}
	for start := 0; start < len(lowText); {
		idx := strings.Index(lowText[start:], lowTok)
		if idx < 0 {
			return false
		}
		pos, end := start+idx, start+idx+len(lowTok)
		if (pos == 0 || !isWordByte(lowText[pos-1])) && (end == len(lowText) || !isWordByte(lowText[end])) {
			return true
		}
		start = pos + 1
	}
	return false
}

func stripBoxMetadata(boxText string) string {
	s := boxText
	for _, sep := range []string{" — verify:", " — expect:", " — docs:", " -- verify:", " -- expect:", " -- docs:"} {
		if idx := strings.Index(strings.ToLower(s), sep); idx >= 0 {
			s = s[:idx]
		}
	}
	s = boxEarliestRe.ReplaceAllString(s, "")
	return strings.TrimSpace(strings.TrimRight(s, "—-;:,"))
}
