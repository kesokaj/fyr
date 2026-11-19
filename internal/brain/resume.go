package brain

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
	all, err := scanRecordsRows(rows)
	if err != nil {
		return nil, nil, err
	}
	for _, r := range all {
		if r.Scope == "" {
			globals = append(globals, r)
		} else {
			proj = append(proj, r)
		}
	}
	return proj, globals, nil
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

func writeRecordFull(b *strings.Builder, r Record) {
	b.WriteString(FormatGet(r))
}
