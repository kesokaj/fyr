package serve

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/search"
)

var (
	overviewHead = []string{"project", "sessions (30d)", "tokens (30d)", "open plans", "open bugs", "memories", "last active"}
	metricsHead  = []string{"project", "sessions", "tool calls", "turns", "tokens", "peak context", "DoD closed", "tool error rate"}
	memTypeRe    = regexp.MustCompile(`^type: ([a-z]+)\n`)
)

// listAll reads every scope's records without creating brain.db when absent.
func (r *Run) listAll(f brain.ListFilter) ([]brain.Record, error) {
	var recs []brain.Record
	if _, err := os.Lstat(brain.Path(r.o.Home)); err == nil {
		var lerr error
		recs, lerr = brain.List(brain.Options{Home: r.o.Home, Dir: r.o.Dir}, brain.ListFilter{All: true, Kind: f.Kind, Open: f.Open})
		if lerr != nil {
			return nil, lerr
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if r.root != "" {
		hasRoot := false
		for _, rec := range recs {
			if rec.Scope == r.root {
				hasRoot = true
				break
			}
		}
		if !hasRoot {
			if trailRecs, ok, _ := brain.ReadTrail(r.root); ok {
				for _, rec := range trailRecs {
					if (f.Kind == "" || rec.Kind == f.Kind) && (!f.Open || rec.Status == "open") {
						recs = append(recs, rec)
					}
				}
			}
		}
	}
	return recs, nil
}

// knownProjects returns the sorted canonical union of projects in brain.db,
// search.db and metrics.db, rolling subdirectories up to enclosing roots (D7).
func (r *Run) knownProjects(mStore *metrics.Store) ([]string, error) {
	scopes, err := brain.Scopes(r.o.Home)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, s := range scopes {
		seen[filepath.Clean(s)] = true
	}
	if _, err := os.Lstat(filepath.Join(r.o.Home, harness.StateDir, "search.db")); err == nil {
		if projs, err := search.ListProjects(r.o.Home); err == nil {
			for _, p := range projs {
				seen[filepath.Clean(p.Root)] = true
			}
		}
	}
	if mStore != nil {
		roots, err := mStore.ProjectRoots()
		if err != nil {
			return nil, err
		}
		for _, p := range roots {
			seen[filepath.Clean(p)] = true
		}
	}
	all := make([]string, 0, len(seen))
	for p := range seen {
		if p != "" && p != "." {
			all = append(all, p)
		}
	}
	sort.Strings(all)
	var out []string
	for _, p := range all {
		if !hasEnclosingRoot(all, p) {
			out = append(out, p)
		}
	}
	return out, nil
}

func hasEnclosingRoot(all []string, sub string) bool {
	for _, p := range all {
		if p != sub && strings.HasPrefix(sub, p+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// overviewBlock builds the project or all-projects summary table for GET /.
func (r *Run) overviewBlock() block {
	recs, err := r.listAll(brain.ListFilter{})
	if err != nil {
		return block{Line: storeErr("brain store", err).Error()}
	}
	var mStore *metrics.Store
	if _, err := os.Lstat(metrics.Path(r.o.Home)); err == nil {
		mStore, err = metrics.Open(metrics.Path(r.o.Home))
		if err != nil {
			return block{Line: storeErr("metrics store", err).Error()}
		}
		defer mStore.Close()
	}
	heading := "projects"
	projects := []string{r.root}
	if r.root != "" {
		heading = "project"
	} else {
		projects, err = r.knownProjects(mStore)
		if err != nil {
			return block{Line: storeErr("brain store", err).Error()}
		}
		if len(projects) == 0 {
			return block{Heading: heading, Line: "no projects"}
		}
	}
	rows := make([][]cell, 0, len(projects))
	for _, p := range projects {
		rows = append(rows, r.overviewRow(p, recs, mStore))
	}
	return block{Heading: heading, Table: &table{Head: overviewHead, Rows: rows}}
}

func (r *Run) overviewRow(p string, recs []brain.Record, mStore *metrics.Store) []cell {
	var plans, bugs, mems int
	last := ""
	for _, rec := range recs {
		if rec.Scope != p {
			continue
		}
		switch {
		case rec.Kind == "plan" && rec.Status == "open":
			plans++
		case rec.Kind == "bug" && rec.Status == "open":
			bugs++
		case rec.Kind == "memory" && rec.Status == "closed":
			mems++
		}
		last = maxDate(last, rec.Date, rec.Updated)
	}
	sessStr, tokStr := "-", "-"
	if mStore != nil {
		if sum, err := mStore.Show(metrics.ShowOptions{Project: p}); err == nil {
			sessStr = strconv.Itoa(sum.Sessions["agy"])
			if t := sum.B.Tokens["agy"]; t != nil {
				tokStr = strconv.FormatInt(t.In+t.Out, 10)
			} else if sum.Sessions["agy"] == 0 {
				tokStr = "0"
			}
			for _, d := range sum.Daily {
				if d.Sessions["agy"] > 0 || d.ToolCalls["agy"] > 0 {
					last = maxDate(last, d.Date, "")
				}
			}
		}
	}
	if last == "" {
		last = "-"
	}
	proj := cell{Text: search.Printable(p)}
	if r.root == "" {
		proj.Href = r.base + "/records?" + url.Values{"scope": {p}}.Encode()
	}
	return []cell{proj, {Text: sessStr}, {Text: tokStr}, {Text: strconv.Itoa(plans)}, {Text: strconv.Itoa(bugs)}, {Text: strconv.Itoa(mems)}, {Text: last}}
}

func maxDate(cur, date, updated string) string {
	if len(date) == 10 && date > cur {
		cur = date
	}
	if len(updated) >= 10 && updated[:10] > cur {
		cur = updated[:10]
	}
	return cur
}

// metricsBlocks builds the blocks of GET /metrics when metrics.db exists.
func (r *Run) metricsBlocks() []block {
	out := []block{{Link: &link{Href: r.base + "/metrics/report", Text: "the metrics report (Antigravity sessions, the last 30 days)"}}}
	s, err := metrics.Open(metrics.Path(r.o.Home))
	if err != nil {
		return append(out, block{Line: storeErr("metrics store", err).Error()})
	}
	defer s.Close()
	if r.root == "" {
		out = append(out, r.metricsProjectsBlock(s))
	}
	sum, err := s.Show(metrics.ShowOptions{Project: r.root})
	if err != nil {
		return append(out, block{Line: storeErr("metrics store", err).Error()})
	}
	var text bytes.Buffer
	sum.WriteText(&text)
	return append(out, block{Pre: printableText(text.String())})
}

func (r *Run) metricsProjectsBlock(s *metrics.Store) block {
	projects, err := r.knownProjects(s)
	if err != nil {
		return block{Line: storeErr("metrics store", err).Error()}
	}
	if len(projects) == 0 {
		return block{Heading: "projects (the last 30 days)", Line: "no projects"}
	}
	rows := make([][]cell, 0, len(projects))
	for _, p := range projects {
		sum, err := s.Show(metrics.ShowOptions{Project: p})
		if err != nil {
			return block{Line: storeErr("metrics store", err).Error()}
		}
		rows = append(rows, metricsProjectRow(p, sum))
	}
	return block{Heading: "projects (the last 30 days)", Table: &table{Head: metricsHead, Rows: rows}}
}

func metricsProjectRow(p string, sum *metrics.Summary) []cell {
	tok := "0"
	if t := sum.B.Tokens["agy"]; t != nil {
		tok = strconv.FormatInt(t.In+t.Out, 10)
	} else if sum.Sessions["agy"] > 0 {
		tok = "n/a"
	}
	c := sum.A.DoDClosed["agy"]
	dod := fmt.Sprintf("%d (%d) / %d", c.Closed, c.Ambiguous, c.Of)
	errRate := "n/a"
	if er := sum.D.ToolErrorRate["agy"]; er != nil {
		errRate = fmt.Sprintf("%.3f", *er)
	}
	return []cell{
		{Text: search.Printable(p)},
		{Text: strconv.Itoa(sum.Sessions["agy"])},
		{Text: strconv.Itoa(sum.B.ToolCalls["agy"])},
		{Text: strconv.Itoa(sum.B.Turns["agy"])},
		{Text: tok},
		{Text: strconv.FormatInt(sum.B.PeakContext["agy"], 10)},
		{Text: dod},
		{Text: errRate},
	}
}

// allMemoriesText formats active memories grouped by project scope (D5).
func (r *Run) allMemoriesText() (string, error) {
	recs, err := r.listAll(brain.ListFilter{Kind: "memory"})
	if err != nil {
		return "", err
	}
	var active []brain.Record
	for _, rec := range recs {
		if rec.Status == "closed" && rec.Scope != "" {
			active = append(active, rec)
		}
	}
	slices.SortFunc(active, func(a, b brain.Record) int {
		if a.Scope != b.Scope {
			return strings.Compare(a.Scope, b.Scope)
		}
		return strings.Compare(a.ID, b.ID)
	})
	var b strings.Builder
	curScope := ""
	for _, rec := range active {
		if rec.Scope != curScope {
			if curScope != "" {
				b.WriteString("\n")
			}
			curScope = rec.Scope
			b.WriteString(search.Printable(curScope) + "\n")
		}
		b.WriteString(formatMemoryLine(rec) + "\n")
	}
	return b.String(), nil
}

func formatMemoryLine(rec brain.Record) string {
	typ := "?"
	if m := memTypeRe.FindStringSubmatch(rec.Body); m != nil {
		typ = m[1]
	}
	l := search.Printable(fmt.Sprintf("- %s (%s) — %s", strings.TrimPrefix(rec.ID, "MEMORY-"), typ, rec.Title))
	for len(l) > 640 {
		_, size := utf8.DecodeLastRuneInString(l)
		l = l[:len(l)-size]
	}
	return l
}
