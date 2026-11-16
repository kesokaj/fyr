package brain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// Resume assembles the read-only session resume text for o.Dir's checkout in
// the seven-step order AGENTS.md § Resume names: AGENTS.md (plus missing-file
// and file-era trail notices), open records, newest handoff, TODO.md § Now,
// each open plan in full, the three newest change records, and active project
// memory. It never creates brain.db when Path(o.Home) does not exist.
func Resume(o Options) (string, error) {
	sc, top, err := trail.Roots(o.Dir)
	if err != nil {
		if errors.Is(err, trail.ErrNotCheckout) {
			return "", refuse("%s is not a git checkout", o.Dir)
		}
		return "", refuse("%v", err)
	}
	recs, projCount, err := resumeRecords(o.Home, sc, top)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := writeResumeAgents(&b, top, projCount); err != nil {
		return "", err
	}
	writeResumeOpen(&b, recs)
	writeResumeNewestHandoff(&b, recs)
	if err := writeResumeTodoNow(&b, top); err != nil {
		return "", err
	}
	writeResumeOpenPlans(&b, recs)
	writeResumeRecentChanges(&b, recs)
	writeResumeMemory(&b, top, recs, o.now())
	return b.String(), nil
}

// resumeRecords reads sc's records newest-first (date DESC, updated DESC,
// id DESC) from Path(home) when the database exists on disk, falling back to
// <top>/.agents/trail.jsonl without creating brain.db, then appends global
// memories and feedbacks from brain.db when present.
func resumeRecords(home, sc, top string) ([]Record, int, error) {
	var projRecs []Record
	var globals []Record
	if home != "" {
		if _, err := os.Stat(Path(home)); err == nil {
			var err error
			projRecs, globals, err = queryScopeAndGlobalsDesc(home, sc)
			if err != nil {
				return nil, 0, err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, 0, err
		}
	}
	if len(projRecs) == 0 {
		trailRecs, ok, err := ReadTrail(top)
		if err != nil {
			return nil, 0, err
		}
		if ok {
			sort.Slice(trailRecs, func(i, j int) bool {
				if trailRecs[i].Date != trailRecs[j].Date {
					return trailRecs[i].Date > trailRecs[j].Date
				}
				if trailRecs[i].Updated != trailRecs[j].Updated {
					return trailRecs[i].Updated > trailRecs[j].Updated
				}
				return trailRecs[i].ID > trailRecs[j].ID
			})
			projRecs = trailRecs
		}
	}
	projCount := len(projRecs)
	all := append(projRecs, globals...)
	return all, projCount, nil
}

func queryScopeAndGlobalsDesc(home, sc string) (proj, globals []Record, err error) {
	st, ver, err := openStoreReadOnly(home)
	if err != nil {
		return nil, nil, err
	}
	defer st.close()
	if ver < 1 {
		return nil, nil, nil
	}
	cols := selectRecordCols(ver)
	rows, err := st.db.Query(
		`SELECT `+cols+` FROM records WHERE scope = ? OR (scope = '' AND (kind = 'memory' OR (kind = 'feedback' AND (project = '' OR project = ?)))) ORDER BY date DESC, updated DESC, id DESC`,
		sc, sc,
	)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, nil, err
		}
		if r.Scope == "" {
			globals = append(globals, r)
		} else {
			proj = append(proj, r)
		}
	}
	return proj, globals, rows.Err()
}

func writeResumeAgents(b *strings.Builder, top string, count int) error {
	b.WriteString("## AGENTS.md\n")
	data, err := readRegular(filepath.Join(top, "AGENTS.md"), maxBody)
	switch {
	case errors.Is(err, os.ErrNotExist):
		b.WriteString("(missing: AGENTS.md)\n")
	case err != nil:
		return err
	default:
		b.Write(data)
		if len(data) == 0 || data[len(data)-1] != '\n' {
			b.WriteByte('\n')
		}
	}
	if _, err := os.Stat(filepath.Join(top, "PRD.md")); errors.Is(err, os.ErrNotExist) {
		b.WriteString("(missing: PRD.md)\n")
	}
	if count == 0 && hasFileEraTrail(top) {
		b.WriteString("(trail is still files; run 'fyr record import')\n")
	}
	return nil
}

func hasFileEraTrail(top string) bool {
	if _, err := os.Lstat(trailPath(top)); err == nil {
		return false
	}
	for _, d := range []string{".agents/docs", "docs"} {
		if fi, err := os.Stat(filepath.Join(top, d)); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}

func isOpenRecord(r Record) bool {
	switch r.Kind {
	case "plan", "bug":
		return r.Status == "open"
	case "spec", "deploy", "guide":
		return r.Status == "current"
	case "proposal":
		return r.Status == "queued"
	default:
		return false
	}
}

func writeResumeOpen(b *strings.Builder, recs []Record) {
	b.WriteString("\n## Open records\n")
	n := 0
	for _, r := range recs {
		if isOpenRecord(r) {
			b.WriteString(FormatLine(r, false) + "\n")
			n++
		}
	}
	if n == 0 {
		b.WriteString("(none)\n")
	}
}

func writeResumeNewestHandoff(b *strings.Builder, recs []Record) {
	b.WriteString("\n## Newest handoff\n")
	for _, r := range recs {
		if r.Kind == "handoff" {
			writeRecordFull(b, r)
			return
		}
	}
	b.WriteString("(none)\n")
}

func writeResumeTodoNow(b *strings.Builder, top string) error {
	b.WriteString("\n## TODO.md Now\n")
	data, err := readRegular(filepath.Join(top, "TODO.md"), maxBody)
	switch {
	case errors.Is(err, os.ErrNotExist):
		b.WriteString("(missing: TODO.md)\n")
		return nil
	case err != nil:
		return err
	}
	now := todoNowSection(string(data))
	if now == "" {
		b.WriteString("(no ## Now section in TODO.md)\n")
		return nil
	}
	b.WriteString(now)
	if !strings.HasSuffix(now, "\n") {
		b.WriteByte('\n')
	}
	return nil
}

func todoNowSection(text string) string {
	var out []string
	inNow, inFence := false, false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
		}
		if !inFence && (strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "# ")) {
			if strings.TrimSpace(strings.TrimPrefix(line, "## ")) == "Now" {
				inNow = true
				out = append(out, line)
				continue
			}
			if inNow {
				break
			}
		}
		if inNow {
			out = append(out, line)
		}
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

func writeResumeOpenPlans(b *strings.Builder, recs []Record) {
	b.WriteString("\n## Open plans\n")
	n := 0
	for _, r := range recs {
		if r.Kind == "plan" && r.Status == "open" {
			if n > 0 {
				b.WriteByte('\n')
			}
			writePlanRecordFull(b, r)
			n++
		}
	}
	if n == 0 {
		b.WriteString("(none)\n")
	}
}

func writePlanRecordFull(b *strings.Builder, r Record) {
	ready := ReadyPhasesLine(r.Body)
	if ready == "" {
		writeRecordFull(b, r)
		return
	}
	b.WriteString(FormatLine(r, false) + "\n")
	b.WriteString(ready + "\n")
	if r.Body != "" {
		b.WriteString(r.Body + "\n")
	}
}

func writeResumeRecentChanges(b *strings.Builder, recs []Record) {
	b.WriteString("\n## Recent changes\n")
	n := 0
	for _, r := range recs {
		if r.Kind == "change" {
			if n > 0 {
				b.WriteByte('\n')
			}
			writeRecordFull(b, r)
			n++
			if n == 3 {
				break
			}
		}
	}
	if n == 0 {
		b.WriteString("(none)\n")
	}
}

func writeResumeMemory(b *strings.Builder, top string, recs []Record, now time.Time) {
	b.WriteString("\n## Memory\n")
	type scoredMem struct {
		rec Record
		val float64
	}
	var feedbacks []Record
	for _, r := range recs {
		if r.Kind == "feedback" {
			feedbacks = append(feedbacks, r)
		}
	}
	var mems []scoredMem
	for _, r := range recs {
		if r.Kind != "memory" || r.Status != "closed" || resolveMemoryKind(r) != "semantic" {
			continue
		}
		val := ComputeMemoryValidity(top, r, CountMemoryContradictions(r, feedbacks), now)
		if val >= MinActiveValidity {
			mems = append(mems, scoredMem{rec: r, val: val})
		}
	}
	if len(mems) == 0 {
		b.WriteString("(none)\n")
		return
	}
	sort.Slice(mems, func(i, j int) bool {
		if mems[i].rec.ID != mems[j].rec.ID {
			return mems[i].rec.ID < mems[j].rec.ID
		}
		return mems[i].rec.Scope > mems[j].rec.Scope
	})
	base := b.Len()
	for i, item := range mems {
		l := formatMemoryIndexLine(top, item.rec, item.val)
		if i == memoryMaxLines || (b.Len()-base)+len(l)+1 > memoryMaxBytes {
			fmt.Fprintf(b, "(… %d more: fyr record list --kind memory, then fyr record get MEMORY-<name>)\n", len(mems)-i)
			return
		}
		b.WriteString(l + "\n")
	}
}

func formatMemoryIndexLine(top string, r Record, val float64) string {
	stale := MemoryStalenessTag(top, r)
	var scopeTag string
	if r.Scope == "" {
		scopeTag = " [global]"
	}
	l := search.Printable(fmt.Sprintf("- %s (%s, %.2f)%s — %s",
		strings.TrimPrefix(r.ID, "MEMORY-"), resolveMemoryKind(r), val, scopeTag, r.Title))
	limit := memoryMaxLine
	if stale != "" && limit > len(stale)+1 {
		limit -= len(stale) + 1
	}
	for len(l) > limit {
		_, size := utf8.DecodeLastRuneInString(l)
		l = l[:len(l)-size]
	}
	if stale != "" {
		l += " " + stale
	}
	return l
}

func writeRecordFull(b *strings.Builder, r Record) {
	b.WriteString(FormatGet(r))
}

var sha256PinRe = regexp.MustCompile(`(?i)\bsha256[:=\s]+([0-9a-f]{7,64})\b`)

type repoFileRef struct {
	rel    string
	pinHex string
}

// MemoryStalenessTag returns "[STALE: <path> modified since <ID>]" when a memory
// record references a repository file modified after the record's timestamp or
// whose SHA-256 no longer matches a hash pin in the record.
func MemoryStalenessTag(top string, r Record) string {
	if top == "" || (r.Kind != "" && r.Kind != "memory") {
		return ""
	}
	text := r.Title + "\n" + r.Body
	recTime := parseRecordTimestamp(r)
	for _, ref := range extractRepoFileRefs(top, text) {
		abs := filepath.Join(top, filepath.FromSlash(ref.rel))
		fi, err := os.Lstat(abs)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if ref.pinHex != "" {
			if data, err := os.ReadFile(abs); err == nil {
				sum := sha256.Sum256(data)
				if !strings.HasPrefix(hex.EncodeToString(sum[:]), ref.pinHex) {
					return fmt.Sprintf("[STALE: %s modified since %s]", ref.rel, r.ID)
				}
				continue
			}
		}
		if !recTime.IsZero() && fi.ModTime().After(recTime) {
			return fmt.Sprintf("[STALE: %s modified since %s]", ref.rel, r.ID)
		}
	}
	return ""
}

func parseRecordTimestamp(r Record) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, strings.TrimSpace(r.Updated)); err == nil {
			return t
		}
	}
	if t, err := time.Parse("2006-01-02", strings.TrimSpace(r.Date)); err == nil {
		return t.Add(24*time.Hour - time.Nanosecond)
	}
	return time.Time{}
}

func extractRepoFileRefs(top, text string) []repoFileRef {
	idxByPath := map[string]int{}
	var out []repoFileRef
	for _, line := range strings.Split(text, "\n") {
		linePin := ""
		if m := sha256PinRe.FindStringSubmatch(line); len(m) == 2 {
			linePin = strings.ToLower(m[1])
		}
		var linePaths []string
		for _, raw := range strings.Fields(line) {
			cand := cleanCandidatePath(raw)
			if cand == "" {
				continue
			}
			abs := filepath.Join(top, filepath.FromSlash(cand))
			if fi, err := os.Lstat(abs); err == nil && fi.Mode().IsRegular() {
				linePaths = append(linePaths, cand)
				if _, seen := idxByPath[cand]; !seen {
					idxByPath[cand] = len(out)
					out = append(out, repoFileRef{rel: cand})
				}
			}
		}
		if linePin != "" && len(linePaths) > 0 {
			last := linePaths[len(linePaths)-1]
			out[idxByPath[last]].pinHex = linePin
		}
	}
	if len(out) == 1 && out[0].pinHex == "" {
		if m := sha256PinRe.FindStringSubmatch(text); len(m) == 2 {
			out[0].pinHex = strings.ToLower(m[1])
		}
	}
	return out
}

func cleanCandidatePath(raw string) string {
	s := strings.Trim(raw, "\"'`()[]{}<>,;!")
	s = strings.TrimPrefix(s, "./")
	if idx := strings.IndexAny(s, ":#"); idx > 0 {
		s = s[:idx]
	}
	s = strings.TrimRight(s, ".:")
	if s == "" || strings.Contains(s, "://") || strings.Contains(s, "..") || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "-") {
		return ""
	}
	if !strings.Contains(s, "/") && filepath.Ext(s) == "" {
		return ""
	}
	clean := filepath.ToSlash(filepath.Clean(s))
	if clean == "." || clean == "" || strings.HasPrefix(clean, "..") {
		return ""
	}
	return clean
}

type planPhaseNode struct {
	id        string
	label     string
	deps      []string
	openTasks int
}

// ReadyPhasesLine parses `### Phase ...` headings and `(after: ...)` dependencies
// in a plan body and returns a 1-line "Ready phases: ..." frontier summary.
func ReadyPhasesLine(body string) string {
	phases := parsePlanPhases(body)
	if len(phases) == 0 {
		return ""
	}
	done := map[string]bool{}
	for _, p := range phases {
		isDone := p.openTasks == 0
		done[p.id] = isDone
		done[p.label] = isDone
	}
	var ready []string
	anyOpen := false
	for _, p := range phases {
		if p.openTasks == 0 {
			continue
		}
		anyOpen = true
		unblocked := true
		for _, dep := range p.deps {
			if !done[dep] {
				unblocked = false
				break
			}
		}
		if unblocked {
			ready = append(ready, p.label)
		}
	}
	if len(ready) == 0 {
		if !anyOpen {
			return "Ready phases: (all complete)"
		}
		return "Ready phases: (none)"
	}
	return "Ready phases: " + strings.Join(ready, ", ")
}

func parsePlanPhases(body string) []planPhaseNode {
	var phases []planPhaseNode
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if strings.HasPrefix(trimmed, "### Phase ") {
			prevID := ""
			if len(phases) > 0 {
				prevID = phases[len(phases)-1].id
			}
			phases = append(phases, parsePhaseHeading(strings.TrimPrefix(trimmed, "### "), prevID))
			continue
		}
		if len(phases) > 0 && strings.HasPrefix(trimmed, "- [ ] ") {
			phases[len(phases)-1].openTasks++
		}
	}
	return phases
}

func parsePhaseHeading(hdr, prevID string) planPhaseNode {
	label := strings.TrimSpace(hdr)
	var deps []string
	hasAfter := false
	if idx := strings.LastIndex(label, "("); idx > 0 && strings.HasSuffix(label, ")") {
		inner := strings.TrimSpace(label[idx+1 : len(label)-1])
		label = strings.TrimSpace(label[:idx])
		if strings.HasPrefix(strings.ToLower(inner), "after:") {
			hasAfter = true
			rawDeps := strings.TrimSpace(inner[len("after:"):])
			for _, part := range strings.Split(rawDeps, ",") {
				d := normalizePhaseID(part)
				if d != "" {
					deps = append(deps, d)
				}
			}
		}
	}
	if !hasAfter && prevID != "" {
		deps = []string{prevID}
	}
	return planPhaseNode{id: normalizePhaseID(label), label: label, deps: deps}
}

func normalizePhaseID(s string) string {
	s = strings.TrimSpace(s)
	low := strings.ToLower(s)
	if low == "" || low == "none" || low == "-" {
		return ""
	}
	fields := strings.Fields(s)
	if len(fields) >= 2 && strings.EqualFold(fields[0], "phase") {
		return "Phase " + strings.TrimRight(fields[1], ":—-")
	}
	if len(fields) == 1 {
		return "Phase " + fields[0]
	}
	return s
}
