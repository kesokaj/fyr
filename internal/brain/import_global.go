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
// and every indexed project's old memory store into that project's scope
// when the project is registered (ADR-068). A
// line, file or store it cannot take is a problem, never fatal, and a source
// with a problem is not listed for removal. Records already stored are
// skipped, so it can run again after the logs grew.
func ImportGlobal(o Options, dryRun bool) (*ImportResult, error) {
	if !o.Global {
		return nil, usage("import --global migrates ~/.config/fyr's logs")
	}
	g := &globalImport{conf: filepath.Join(o.Home, harness.StateDir), res: &ImportResult{}, b: newBudget(),
		used: map[string]bool{}, failed: map[string]bool{}}
	for _, log := range []struct{ file, kind string }{{"feedback.jsonl", "feedback"}, {"retros.jsonl", "retro"}} {
		g.readLog(log.file, log.kind)
	}
	g.readProposals()
	g.readMemories(o.Home)
	res := g.res
	for _, src := range g.sources {
		if !g.failed[src] {
			res.Sources = append(res.Sources, src)
		}
	}
	if dryRun {
		return res, nil
	}
	if err := storeImported(o.Home, "", "", res, g.recs, false); err != nil {
		return nil, err
	}
	return res, nil
}

// globalImport is one ImportGlobal run: what it read, and what it found.
type globalImport struct {
	conf    string // ~/.config/fyr
	res     *ImportResult
	b       *budget
	recs    []Record
	used    map[string]bool
	failed  map[string]bool // sources with a problem: kept, never listed for removal
	sources []string
}

func (g *globalImport) problem(source, msg string) {
	g.failed[source] = true
	g.res.Problems = append(g.res.Problems, msg)
}

func (g *globalImport) add(r Record, source string) {
	r = normalize(r)
	// A -N suffix is tried until it names no ID this import already
	// gave: FOO.md, foo-2.md and foo.md are three records (round 2,
	// finding 1).
	if g.used[r.ID] {
		base := r.ID
		for n := 2; g.used[r.ID]; n++ {
			r.ID = fmt.Sprintf("%s-%d", base, n)
		}
	}
	g.used[r.ID] = true
	if err := checkRecord(r); err != nil {
		g.problem(source, fmt.Sprintf("%s: %v; not imported", source, err))
		return
	}
	g.recs = append(g.recs, r)
	g.res.Records = append(g.res.Records, Imported{ID: r.ID, Kind: r.Kind, Status: r.Status, Path: source, Project: r.Project, rec: r})
}

// readLog takes one JSONL log, a record of kind per line.
func (g *globalImport) readLog(file, kind string) {
	lines, long, err := readLogLines(filepath.Join(g.conf, file))
	if err != nil {
		g.problem(file, file+": "+err.Error()+"; not imported")
		return
	}
	if lines == nil {
		return
	}
	g.sources = append(g.sources, file)
	for _, n := range long {
		g.problem(file, fmt.Sprintf("%s line %d: over %d bytes; not imported", file, n, maxLine))
	}
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			g.readLogLine(file, kind, i+1, l)
		}
	}
}

func (g *globalImport) readLogLine(file, kind string, n int, l string) {
	src := fmt.Sprintf("%s line %d", file, n)
	var e map[string]any
	if err := json.Unmarshal([]byte(l), &e); err != nil {
		g.problem(file, src+": not one JSON object; not imported")
		return
	}
	ts, _ := e["ts"].(string)
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		g.problem(file, fmt.Sprintf("%s: ts %q is not RFC 3339; not imported", src, ts))
		return
	}
	project, _ := e["project"].(string)
	title := kind + " " + filepath.Base(project)
	if kind == "feedback" {
		corrected, _ := e["corrected"].(string)
		if corrected == "" {
			corrected, _ = e["user_quote"].(string)
		}
		title = corrected
	}
	// The line as written: indenting it first could multiply a
	// nested line's size before any cap is checked (round 2,
	// finding 2).
	g.add(Record{ID: fmt.Sprintf("%s-%s-%s", prefixes[kind], t.Format("2006-01-02-1504"), slugify(title)), Kind: kind,
		Status: "closed", Date: t.Format("2006-01-02"), Title: title, Body: l,
		Project: projectKey(project), Updated: t.UTC().Format(time.RFC3339)}, file)
}

// readProposals takes proposals/**/*.md, a proposal record each.
func (g *globalImport) readProposals() {
	props := filepath.Join(g.conf, "proposals")
	if fi, err := os.Lstat(props); err == nil && fi.IsDir() {
		filepath.WalkDir(props, g.readProposal)
	}
}

func (g *globalImport) readProposal(p string, e fs.DirEntry, err error) error {
	if err != nil || !e.Type().IsRegular() || !strings.HasSuffix(p, ".md") {
		return nil
	}
	rel, _ := filepath.Rel(g.conf, p)
	rel = filepath.ToSlash(rel)
	data, err := readRegular(p, maxBody)
	if err != nil {
		g.problem(rel, rel+": "+err.Error()+"; not imported")
		return nil
	}
	if err := g.b.take(len(data), rel); err != nil {
		g.problem(rel, rel+": "+err.Error())
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
	g.sources = append(g.sources, rel)
	g.add(Record{ID: "PROPOSAL-" + slugOf(strings.ReplaceAll(strings.TrimPrefix(strings.TrimSuffix(rel, ".md"), "proposals/"), "/", "-")),
		Kind: "proposal", Status: "queued", Date: date, Title: title, Body: body,
		Project: projectKey(meta["project"]), Updated: date + "T00:00:00Z"}, rel)
	return nil
}

// readMemories takes the old memory store of every indexed project the
// operator registered under this home into that project's scope; an
// unregistered one is a problem, its store left where it is (ADR-068,
// audit-choices D15).
func (g *globalImport) readMemories(home string) {
	res := g.res
	projects, err := search.ListProjects(home)
	if err != nil {
		res.Problems = append(res.Problems, "the global index's project list: "+err.Error())
	}
	for _, p := range projects {
		scope, _, err := trail.Roots(p.Root)
		if err != nil {
			continue
		}
		if !trail.Registered(g.conf, scope) {
			res.Problems = append(res.Problems, scope+": not registered (fyr index --register); its memory store is not imported")
			continue
		}
		mems, files, problems, err := readMemoryStore(scope, g.b)
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
			g.recs = append(g.recs, m.rec)
			res.Records = append(res.Records, m)
		}
	}
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
