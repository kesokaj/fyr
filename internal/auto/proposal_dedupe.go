package auto

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/redact"
)

var (
	proposalDateRe  = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}(?:-\d{4})?\b`)
	proposalSHARe   = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	proposalCountRe = regexp.MustCompile(`\b(?:x\d+|\d+x)\b`)
)

type proposalSpec struct {
	Project, Title, Body, UpdateBody, ReportDate, CustomKey string
	Derived                                                 bool
}

func (r *run) invalidateProposalCache() {
	if r.sweepCache != nil {
		r.sweepCache.propsLoaded, r.sweepCache.props, r.sweepCache.propsErr = false, nil, nil
	}
}

func (r *run) upsertProposal(spec proposalSpec) (id string, created bool, err error) {
	g := brain.Options{Home: r.o.Home, Global: true, Now: r.o.Now}
	key := spec.CustomKey
	if key == "" {
		key = proposalDedupeKey(brain.Record{Title: spec.Title, Body: spec.Body, Project: spec.Project})
	}
	if match, ok := r.findOpenProposalByKey(g, key, spec.Derived); ok {
		merged := mergeRecurringProposalBody(match.Body, spec, r.o.now())
		if err := brain.Edit(g, brain.EditInput{ID: match.ID, Body: strings.NewReader(redact.Redact(merged))}); err != nil {
			return "", false, err
		}
		r.invalidateProposalCache()
		return match.ID, false, nil
	}
	id, err = brain.Add(g, brain.AddInput{
		Kind: "proposal", Status: "queued", Title: spec.Title, Project: spec.Project,
		Body: strings.NewReader(redact.Redact(spec.Body)),
	})
	if err != nil {
		return "", false, err
	}
	r.invalidateProposalCache()
	return id, true, nil
}

func (r *run) findOpenProposalByKey(g brain.Options, key string, derivedOnly bool) (brain.Record, bool) {
	if key == "" || !brainDBExists(r.o.Home) {
		return brain.Record{}, false
	}
	open, err := brain.List(g, brain.ListFilter{Kind: "proposal", Open: true})
	if err != nil {
		return brain.Record{}, false
	}
	for _, rec := range open {
		if derivedOnly && !isDerivedProposal(rec) {
			continue
		}
		if proposalDedupeKey(rec) == key {
			return rec, true
		}
	}
	return brain.Record{}, false
}

func brainDBExists(home string) bool {
	if strings.TrimSpace(home) == "" {
		return false
	}
	_, err := os.Lstat(filepath.Join(home, harness.StateDir, "brain.db"))
	return err == nil
}

func noTestAskKey(root, class string) string {
	return "ask:no-test|" + proposalProjectKey(root) + "|" + strings.ToLower(strings.TrimSpace(class))
}

func rubricAskKey(root, class, denied string) string {
	return "ask:denied|" + proposalProjectKey(root) + "|" + strings.ToLower(strings.TrimSpace(class)) + "|" + strings.ToLower(strings.TrimSpace(denied))
}

func evalGateProposalKey(class string) string {
	return "eval-gate|harness|" + strings.ToLower(strings.TrimSpace(class))
}

func proposalProjectKey(root string) string {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		return ""
	}
	clean := filepath.Clean(trimmed)
	if real, err := filepath.EvalSymlinks(clean); err == nil {
		return filepath.Clean(real)
	}
	return clean
}

func proposalScopeKey(rec brain.Record) string {
	for _, line := range proposalHeaderLines(rec.Body) {
		if tgt := proposalHeaderTarget(line); tgt == "harness" {
			return "harness"
		} else if tgt == "project" {
			return "project:" + proposalProjectKey(rec.Project)
		}
	}
	low := strings.ToLower(strings.TrimSpace(rec.Title))
	if strings.HasPrefix(low, "auto, harness") || strings.HasPrefix(low, "dream, harness") {
		return "harness"
	}
	return "project:" + proposalProjectKey(rec.Project)
}

func proposalActionScopeHeader(rec brain.Record) string {
	for _, line := range proposalHeaderLines(rec.Body) {
		if v, ok := recordFieldVal(line, "actionscope:", "action_scope:"); ok && v != "" {
			return strings.ToLower(strings.TrimSpace(v))
		}
	}
	return ""
}

func proposalDedupeKey(rec brain.Record) string {
	if k := systemAskDedupeKey(rec); k != "" {
		return k
	}
	scopeKey := proposalScopeKey(rec)
	if k := evalGateDedupeKey(rec, scopeKey); k != "" {
		return k
	}
	actionScope := proposalActionScopeHeader(rec)
	sigID, _ := extractProposalAttribution(rec)
	if sigID == "" {
		sigID = firstSignalID(rec.Title)
	}
	if sigID != "" {
		return "signal|" + scopeKey + "|" + sigID + "|" + actionScope
	}
	norm := normalizeProposalTitle(rec.Title)
	if isStillRegressingProposal(rec) {
		return "still-regressing|" + scopeKey + "|" + actionScope + "|" + norm
	}
	if norm == "" {
		return ""
	}
	return "title|" + scopeKey + "|" + actionScope + "|" + norm
}

func systemAskDedupeKey(rec brain.Record) string {
	low := strings.ToLower(strings.TrimSpace(rec.Title))
	rest, ok := strings.CutPrefix(low, "auto ask:")
	if !ok {
		return ""
	}
	rest = strings.TrimSpace(rest)
	if strings.Contains(rest, "no # auto: test line") {
		class, _, _ := strings.Cut(rest, " in ")
		return noTestAskKey(rec.Project, class)
	}
	if class, after, ok := strings.Cut(rest, " in "); ok {
		if idx := strings.LastIndex(after, "("); idx >= 0 && strings.HasSuffix(after, ")") {
			return rubricAskKey(rec.Project, class, after[idx+1:len(after)-1])
		}
	}
	return "ask|" + proposalProjectKey(rec.Project) + "|" + normalizeProposalTitle(rec.Title)
}

func evalGateDedupeKey(rec brain.Record, scopeKey string) string {
	low := strings.ToLower(strings.TrimSpace(rec.Title))
	idx := strings.Index(low, " not kept (")
	if idx < 0 {
		return ""
	}
	head := strings.TrimSpace(low[:idx])
	for _, pfx := range []string{"auto, harness — ", "auto, harness - ", "auto, project — ", "auto, project - "} {
		head = strings.TrimPrefix(head, pfx)
	}
	if head == "" {
		return ""
	}
	return "eval-gate|" + scopeKey + "|" + head
}

func isStillRegressingProposal(rec brain.Record) bool {
	low := strings.ToLower(rec.Title + "\n" + rec.Body)
	return strings.Contains(low, "still_regressing") || strings.Contains(low, "still regressing")
}

func normalizeProposalTitle(title string) string {
	low := strings.ToLower(strings.TrimSpace(title))
	for _, pfx := range []string{
		"auto, harness — ", "auto, harness - ", "auto, project — ", "auto, project - ",
		"dream, harness — ", "dream, harness - ", "dream, project — ", "dream, project - ",
	} {
		low = strings.TrimPrefix(low, pfx)
	}
	low = proposalCountRe.ReplaceAllString(proposalSHARe.ReplaceAllString(proposalDateRe.ReplaceAllString(low, " "), " "), " ")
	low = strings.NewReplacer("(", " ", ")", " ", "[", " ", "]", " ", ":", " ", ",", " ", ";", " ").Replace(low)
	return strings.Join(strings.Fields(low), " ")
}

func parseProposalOccurrences(body string) int {
	for _, line := range proposalHeaderLines(body) {
		if v, ok := recordFieldVal(line, "occurrences:"); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
				return n
			}
		}
	}
	return 1
}

func proposalLastSeen(rec brain.Record) time.Time {
	for _, line := range proposalHeaderLines(rec.Body) {
		if v, ok := recordFieldVal(line, "lastseen:", "last_seen:"); ok {
			if t, err := time.Parse(time.RFC3339, strings.TrimSpace(v)); err == nil {
				return t
			}
		}
	}
	return recordTime(rec)
}

func splitProposalHeaderAndBody(body string) (headers []string, rest string) {
	lines := strings.Split(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(body), "\n")
	i := 0
	for i < len(lines) {
		raw := lines[i]
		if strings.TrimSpace(raw) == "" {
			i++
			break
		}
		if raw != strings.TrimLeft(raw, " \t") || !looksLikeHeaderKV(raw) {
			break
		}
		headers = append(headers, strings.TrimSpace(raw))
		i++
	}
	return headers, strings.TrimRight(strings.Join(lines[i:], "\n"), "\n")
}

func looksLikeHeaderKV(line string) bool {
	k, _, ok := strings.Cut(line, ":")
	if !ok || k == "" || len(k) > 32 {
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func upsertHeaderLine(headers []string, key, value string) []string {
	want, line := strings.ToLower(key)+":", key+": "+value
	for i, h := range headers {
		if strings.HasPrefix(strings.ToLower(h), want) {
			headers[i] = line
			return headers
		}
	}
	return append(headers, line)
}

func mergeRecurringProposalBody(existingBody string, spec proposalSpec, now time.Time) string {
	count, stamp := parseProposalOccurrences(existingBody)+1, now.UTC().Format(time.RFC3339)
	headers, rest := splitProposalHeaderAndBody(existingBody)
	headers = upsertHeaderLine(upsertHeaderLine(headers, "Occurrences", strconv.Itoa(count)), "LastSeen", stamp)
	if spec.ReportDate != "" {
		headers = updateNewerReportDate(headers, spec.ReportDate)
	}
	rest = stripPriorLatestSection(rest, spec.Derived)
	return assembleProposalBody(headers, rest, formatLatestOccurrence(spec, stamp, count))
}

func updateNewerReportDate(headers []string, incoming string) []string {
	for _, h := range headers {
		if v, ok := recordFieldVal(h, "reportdate:", "report_date:"); ok && v >= incoming {
			return headers
		}
	}
	return upsertHeaderLine(headers, "ReportDate", incoming)
}

func stripPriorLatestSection(rest string, derived bool) string {
	marker := "\n\n## Latest occurrence\n"
	if derived {
		marker = "\n\n    [latest occurrence "
	}
	if idx := strings.Index(rest, marker); idx >= 0 {
		return strings.TrimRight(rest[:idx], "\n")
	}
	if derived && strings.HasPrefix(rest, "    [latest occurrence ") {
		return ""
	}
	return strings.TrimRight(rest, "\n")
}

func formatLatestOccurrence(spec proposalSpec, stamp string, count int) string {
	text := strings.TrimSpace(spec.UpdateBody)
	if spec.Derived {
		head := fmt.Sprintf("    [latest occurrence %s, count %d]", stamp, count)
		if text == "" {
			return head
		}
		return head + "\n" + indentProposalBody(text)
	}
	out := fmt.Sprintf("## Latest occurrence\n\n- occurrences: %d\n- last_seen: %s", count, stamp)
	if text != "" {
		out += "\n\n" + text
	}
	return out
}

func assembleProposalBody(headers []string, rest, footer string) string {
	var parts []string
	if len(headers) > 0 {
		parts = append(parts, strings.Join(headers, "\n"))
	}
	if strings.TrimSpace(rest) != "" {
		parts = append(parts, rest)
	}
	if strings.TrimSpace(footer) != "" {
		parts = append(parts, footer)
	}
	return strings.Join(parts, "\n\n") + "\n"
}
