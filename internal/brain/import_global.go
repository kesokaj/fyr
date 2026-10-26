package brain

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// ImportGlobal migrates ~/.config/fyr's logs into the global scope:
// feedback.jsonl as feedback records, retros.jsonl as retro records,
// proposals/**/*.md as proposal records, each with the project it is about;
// and every indexed project's old memory store into that project's scope. A
// line, file or store it cannot take is a problem, never fatal, and a source
// with a problem is not listed for removal. Records already stored are
// skipped, so it can run again after the logs grew.
func ImportGlobal(o Options, dryRun bool) (*ImportResult, error) {
	if !o.Global {
		return nil, usage("import --global migrates ~/.config/fyr's logs")
	}
	conf := filepath.Join(o.Home, harness.StateDir)
	res := &ImportResult{}
	b := newBudget()
	var recs []Record
	used := map[string]bool{}
	failed := map[string]bool{} // sources with a problem: kept, never listed for removal
	problem := func(source, msg string) {
		failed[source] = true
		res.Problems = append(res.Problems, msg)
	}
	add := func(r Record, source string) {
		r = normalize(r)
		// A -N suffix is tried until it names no ID this import already
		// gave: FOO.md, foo-2.md and foo.md are three records (round 2,
		// finding 1).
		if used[r.ID] {
			base := r.ID
			for n := 2; used[r.ID]; n++ {
				r.ID = fmt.Sprintf("%s-%d", base, n)
			}
		}
		used[r.ID] = true
		if err := checkRecord(r); err != nil {
			problem(source, fmt.Sprintf("%s: %v; not imported", source, err))
			return
		}
		recs = append(recs, r)
		res.Records = append(res.Records, Imported{ID: r.ID, Kind: r.Kind, Status: r.Status, Path: source, Project: r.Project, rec: r})
	}
	var sources []string

	for _, log := range []struct{ file, kind string }{{"feedback.jsonl", "feedback"}, {"retros.jsonl", "retro"}} {
		lines, long, err := readLogLines(filepath.Join(conf, log.file))
		if err != nil {
			problem(log.file, log.file+": "+err.Error()+"; not imported")
			continue
		}
		if lines == nil {
			continue
		}
		sources = append(sources, log.file)
		for _, n := range long {
			problem(log.file, fmt.Sprintf("%s line %d: over %d bytes; not imported", log.file, n, maxLine))
		}
		for i, l := range lines {
			if strings.TrimSpace(l) == "" {
				continue
			}
			src := fmt.Sprintf("%s line %d", log.file, i+1)
			var e map[string]any
			if err := json.Unmarshal([]byte(l), &e); err != nil {
				problem(log.file, src+": not one JSON object; not imported")
				continue
			}
			ts, _ := e["ts"].(string)
			t, err := time.Parse(time.RFC3339, ts)
			if err != nil {
				problem(log.file, fmt.Sprintf("%s: ts %q is not RFC 3339; not imported", src, ts))
				continue
			}
			project, _ := e["project"].(string)
			title := log.kind + " " + filepath.Base(project)
			if log.kind == "feedback" {
				corrected, _ := e["corrected"].(string)
				if corrected == "" {
					corrected, _ = e["user_quote"].(string)
				}
				title = corrected
			}
			// The line as written: indenting it first could multiply a
			// nested line's size before any cap is checked (round 2,
			// finding 2).
			add(Record{ID: fmt.Sprintf("%s-%s-%s", prefixes[log.kind], t.Format("2006-01-02-1504"), slugify(title)), Kind: log.kind,
				Status: "closed", Date: t.Format("2006-01-02"), Title: title, Body: l,
				Project: projectKey(project), Updated: t.UTC().Format(time.RFC3339)}, log.file)
		}
	}

	props := filepath.Join(conf, "proposals")
	if fi, err := os.Lstat(props); err == nil && fi.IsDir() {
		filepath.WalkDir(props, func(p string, e fs.DirEntry, err error) error {
			if err != nil || !e.Type().IsRegular() || !strings.HasSuffix(p, ".md") {
				return nil
			}
			rel, _ := filepath.Rel(conf, p)
			rel = filepath.ToSlash(rel)
			data, err := readRegular(p, maxBody)
			if err != nil {
				problem(rel, rel+": "+err.Error()+"; not imported")
				return nil
			}
			if err := b.take(len(data), rel); err != nil {
				problem(rel, rel+": "+err.Error())
				return filepath.SkipAll
			}
			meta, body := frontmatter(strings.ReplaceAll(string(data), "\r\n", "\n"))
			body = strings.TrimSpace(body)
			if body == "" {
				return nil
			}
			base := strings.TrimSuffix(filepath.Base(p), ".md")
			if base == "SKILL" {
				base = filepath.Base(filepath.Dir(p))
			}
			fallback := time.Now().Format("2006-01-02")
			if info, err := e.Info(); err == nil {
				fallback = info.ModTime().Format("2006-01-02")
			}
			date := docDate(meta, base, fallback)
			title := heading(body)
			if title == "" {
				title = "proposal " + base
			}
			sources = append(sources, rel)
			add(Record{ID: "PROPOSAL-" + slugOf(strings.ReplaceAll(strings.TrimPrefix(strings.TrimSuffix(rel, ".md"), "proposals/"), "/", "-")),
				Kind: "proposal", Status: "queued", Date: date, Title: title, Body: body,
				Project: projectKey(meta["project"]), Updated: date + "T00:00:00Z"}, rel)
			return nil
		})
	}

	projects, err := search.ListProjects(o.Home)
	if err != nil {
		res.Problems = append(res.Problems, "the global index's project list: "+err.Error())
	}
	for _, p := range projects {
		scope, _, err := trail.Roots(p.Root)
		if err != nil {
			continue
		}
		mems, files, problems, err := readMemoryStore(scope, b)
		if err != nil {
			res.Problems = append(res.Problems, scope+": "+err.Error())
			continue
		}
		res.Problems = append(res.Problems, problems...)
		for _, f := range files {
			res.Memory = append(res.Memory, filepath.Join(scope, f))
		}
		for _, m := range mems {
			m.rec.Scope = scope
			m.Path = filepath.Join(scope, m.Path)
			recs = append(recs, m.rec)
			res.Records = append(res.Records, m)
		}
	}
	for _, src := range sources {
		if !failed[src] {
			res.Sources = append(res.Sources, src)
		}
	}
	if dryRun {
		return res, nil
	}
	st, err := openStore(o.Home)
	if err != nil {
		return nil, err
	}
	s := &session{st: st}
	defer s.close()
	stored, err := s.insertNew(recs, false)
	if err != nil {
		return nil, err
	}
	res.Skipped = len(res.Records) - len(stored)
	res.Records = filterStored(res.Records, stored)
	return res, nil
}

// projectKey is a log's project as a global record keys it: the main
// worktree's root when the path is still a checkout, else the path as given.
func projectKey(p string) string {
	if p == "" {
		return ""
	}
	return canonicalProject(p)
}

// readLogLines reads a JSONL log, opened as readRegular opens a file; nil
// when there is none. A line over maxLine is not kept: its number is
// returned in long and its place left empty, so the other lines keep their
// numbers and the import goes on (security review of the import, finding 8).
func readLogLines(p string) (lines []string, long []int, err error) {
	if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	f, err := openNonBlocking(p)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return nil, nil, refuse("%s is not a regular file", p)
	}
	br := bufio.NewReaderSize(&capped{r: f, left: maxTrail}, 64<<10)
	lines = []string{}
	for n := 1; ; n++ {
		var line []byte
		tooLong := false
		for {
			chunk, err := br.ReadSlice('\n')
			if !tooLong {
				if len(line)+len(chunk) > maxLine {
					tooLong, line = true, nil
				} else {
					line = append(line, chunk...)
				}
			}
			if errors.Is(err, bufio.ErrBufferFull) {
				continue
			}
			if err != nil && !errors.Is(err, io.EOF) {
				return nil, nil, err
			}
			if tooLong {
				long = append(long, n)
				lines = append(lines, "")
			} else if len(line) > 0 {
				lines = append(lines, strings.TrimRight(string(line), "\r\n"))
			}
			if errors.Is(err, io.EOF) {
				return lines, long, nil
			}
			break
		}
	}
}
