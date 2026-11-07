package brain

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	recs, err := resumeRecords(o.Home, sc, top)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := writeResumeAgents(&b, top, len(recs)); err != nil {
		return "", err
	}
	writeResumeOpen(&b, recs)
	writeResumeNewestHandoff(&b, recs)
	if err := writeResumeTodoNow(&b, top); err != nil {
		return "", err
	}
	writeResumeOpenPlans(&b, recs)
	writeResumeRecentChanges(&b, recs)
	writeResumeMemory(&b, recs)
	return b.String(), nil
}

// resumeRecords reads sc's records newest-first (date DESC, updated DESC,
// id DESC) from Path(home) when the database exists on disk, falling back to
// <top>/.agents/trail.jsonl without creating brain.db.
func resumeRecords(home, sc, top string) ([]Record, error) {
	if home != "" {
		if _, err := os.Stat(Path(home)); err == nil {
			recs, err := queryScopeDesc(home, sc)
			if err != nil || len(recs) > 0 {
				return recs, err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	recs, ok, err := ReadTrail(top)
	if err != nil || !ok {
		return nil, err
	}
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].Date != recs[j].Date {
			return recs[i].Date > recs[j].Date
		}
		if recs[i].Updated != recs[j].Updated {
			return recs[i].Updated > recs[j].Updated
		}
		return recs[i].ID > recs[j].ID
	})
	return recs, nil
}

func queryScopeDesc(home, sc string) ([]Record, error) {
	st, err := openStore(home)
	if err != nil {
		return nil, err
	}
	defer st.close()
	rows, err := st.db.Query(
		`SELECT `+recordCols+` FROM records WHERE scope = ? ORDER BY date DESC, updated DESC, id DESC`,
		sc,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var recs []Record
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		recs = append(recs, r)
	}
	return recs, rows.Err()
}

func writeResumeAgents(b *strings.Builder, top string, count int) error {
	b.WriteString("## AGENTS.md\n")
	data, err := os.ReadFile(filepath.Join(top, "AGENTS.md"))
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
	data, err := os.ReadFile(filepath.Join(top, "TODO.md"))
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
			writeRecordFull(b, r)
			n++
		}
	}
	if n == 0 {
		b.WriteString("(none)\n")
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

func writeResumeMemory(b *strings.Builder, recs []Record) {
	b.WriteString("\n## Memory\n")
	var mems []Record
	for _, r := range recs {
		if r.Kind == "memory" && r.Status == "closed" {
			mems = append(mems, r)
		}
	}
	if len(mems) == 0 {
		b.WriteString("(none)\n")
		return
	}
	sort.Slice(mems, func(i, j int) bool { return mems[i].ID < mems[j].ID })
	base := b.Len()
	for i, r := range mems {
		l := formatMemoryIndexLine(r)
		if i == memoryMaxLines || (b.Len()-base)+len(l)+1 > memoryMaxBytes {
			fmt.Fprintf(b, "(… %d more: fyr record list --kind memory, then fyr record get MEMORY-<name>)\n", len(mems)-i)
			return
		}
		b.WriteString(l + "\n")
	}
}

func formatMemoryIndexLine(r Record) string {
	typ := "?"
	if m := memoryType.FindStringSubmatch(r.Body); m != nil {
		typ = m[1]
	}
	l := search.Printable(fmt.Sprintf("- %s (%s) — %s", strings.TrimPrefix(r.ID, "MEMORY-"), typ, r.Title))
	for len(l) > memoryMaxLine {
		_, size := utf8.DecodeLastRuneInString(l)
		l = l[:len(l)-size]
	}
	return l
}

func writeRecordFull(b *strings.Builder, r Record) {
	b.WriteString(FormatGet(r))
}
