package auto

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/check"
	"github.com/kesokaj/fyr/internal/trail"
)

const (
	staleAfter     = 14 * 24 * time.Hour // an open plan's untouched boxes, a kept worker branch
	queuedAfter    = 30 * 24 * time.Hour // a proposal nobody took up
	handoffSlack   = time.Hour           // a checkpoint commits the handoff it just wrote
	repeatsToRaise = 3                   // a fixed bug cited this often should have graduated
)

// Finding is one thing a health check found. Text is stable from run to run,
// so an unchanged set of findings is recognised and files no second report.
type Finding struct {
	Check string // check, status, metrics, resume, trail, quota
	Rule  string
	Where string
	Text  string
}

func sortFindings(fs []Finding) {
	slices.SortFunc(fs, func(a, b Finding) int {
		return strings.Compare(a.line(), b.line())
	})
}

func (f Finding) line() string {
	return short(f.Check + "/" + f.Rule + "  " + f.Where + "  " + f.Text)
}

// projectFindings is catalog 1, 4 and 5 for one checkout. Every read is of the
// tree or its trail file; a check that could not run is a finding of its own,
// and failed says so.
func (o Options) projectFindings(root string) (fs []Finding, failed bool) {
	broken := func(name string, err error) {
		failed = true
		fs = append(fs, Finding{name, "failed", root, oneLine(err.Error())})
	}
	rep, err := check.Run(root, o.Vocabulary, o.Home)
	if err != nil {
		broken("check", err)
	}
	for _, f := range rep.Findings {
		where := f.Where
		if f.Line > 0 {
			where += ":" + strconv.Itoa(f.Line)
		}
		fs = append(fs, Finding{"check", f.Rule, where, f.Text})
	}
	recs, _, err := brain.ReadScope(o.Home, root)
	if err != nil {
		broken("trail", err)
	}
	now := o.now()
	resume, err := resumeFindings(root, recs, now)
	if err != nil {
		broken("resume", err)
	}
	branches, err := keptBranches(root, now)
	if err != nil {
		broken("resume", err)
	}
	fs = append(append(append(fs, resume...), branches...), trailFindings(recs)...)
	sortFindings(fs)
	return fs, failed
}

func recordTime(rec brain.Record) time.Time {
	if t, err := time.Parse(time.RFC3339, rec.Updated); err == nil {
		return t
	}
	t, _ := time.Parse("2006-01-02", rec.Date)
	return t
}

// resumeFindings is catalog 4: what a fresh session would trip over. The
// dirty tree and the unpushed commits are G8's, which stops first.
func resumeFindings(root string, recs []brain.Record, now time.Time) ([]Finding, error) {
	var fs []Finding
	var newest brain.Record
	openPlan := false
	for _, rec := range recs {
		switch rec.Kind {
		case "handoff":
			if recordTime(rec).After(recordTime(newest)) {
				newest = rec
			}
		case "plan":
			if rec.Status != "open" {
				continue
			}
			openPlan = true
			if strings.Contains(rec.Body, "- [ ]") && now.Sub(recordTime(rec)) > staleAfter {
				fs = append(fs, Finding{"resume", "plan-stale", rec.ID, "an open plan with unticked boxes, untouched since " + recordTime(rec).Format("2006-01-02")})
			}
		}
	}
	if newest.ID != "" {
		out, err := trail.Log(root, "-1", "--format=%ct %h")
		if err != nil {
			return fs, fmt.Errorf("git log: %w", err)
		}
		sec, sha, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
		if n, err := strconv.ParseInt(sec, 10, 64); err == nil && time.Unix(n, 0).Sub(recordTime(newest)) > handoffSlack {
			fs = append(fs, Finding{"resume", "handoff-behind", newest.ID, "the newest handoff is older than HEAD " + sha})
		}
	}
	data, err := readBounded(filepath.Join(root, "TODO.md"), 1<<20)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return fs, err
	case !openPlan && todoNow(string(data)) != "":
		fs = append(fs, Finding{"resume", "todo-now", "TODO.md", "Now holds work and no plan is open"})
	}
	return fs, nil
}

var comment = regexp.MustCompile(`(?s)<!--.*?-->`)

// todoNow is the text of TODO.md's Now, empty when it says there is none.
func todoNow(text string) string {
	_, rest, ok := strings.Cut("\n"+comment.ReplaceAllString(text, ""), "\n## Now\n")
	if !ok {
		return ""
	}
	if i := strings.Index(rest, "\n## "); i >= 0 {
		rest = rest[:i]
	}
	rest = strings.TrimSpace(rest)
	if strings.EqualFold(strings.TrimRight(rest, "."), "none") {
		return ""
	}
	return rest
}

// keptBranches lists the workers' branches left behind past their time.
func keptBranches(root string, now time.Time) ([]Finding, error) {
	out, err := trail.Command(root, "for-each-ref", "--format=%(committerdate:unix) %(refname:short)", "refs/heads/work/auto-*").Output()
	if err != nil {
		return nil, fmt.Errorf("git for-each-ref: %w", err)
	}
	var fs []Finding
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		sec, name, ok := strings.Cut(line, " ")
		n, err := strconv.ParseInt(sec, 10, 64)
		if ok && err == nil && now.Sub(time.Unix(n, 0)) > staleAfter {
			fs = append(fs, Finding{"resume", "branch-kept", name, "a kept worker branch over 14 days old"})
		}
	}
	return fs, nil
}

var bugRef = regexp.MustCompile(`\bBUG-[0-9]+\b`)

// trailFindings is catalog 5 over one project's records. A repeat is another
// bug that cites this one.
func trailFindings(recs []brain.Record) []Finding {
	cited := map[string]int{}
	for _, rec := range recs {
		if rec.Kind != "bug" {
			continue
		}
		seen := map[string]bool{rec.ID: true}
		for _, id := range bugRef.FindAllString(rec.Body, -1) {
			if !seen[id] {
				seen[id] = true
				cited[id]++
			}
		}
	}
	var fs []Finding
	for _, rec := range recs {
		if rec.Kind != "bug" {
			continue
		}
		if !strings.Contains(strings.ToLower(rec.Body), "prevention") {
			fs = append(fs, Finding{"trail", "bug-no-prevention", rec.ID, "a bug with no prevention rule"})
		}
		if rec.Status == "fixed" && cited[rec.ID] >= repeatsToRaise {
			fs = append(fs, Finding{"trail", "bug-repeats", rec.ID, fmt.Sprintf("fixed, not graduated, cited by %d later bugs", cited[rec.ID])})
		}
	}
	return fs
}

// machineFindings is catalog 2, 3 and 7 and the queued proposals, each once a
// run. The opening of the store and the metrics ingest are why a dry run never
// comes here.
func (r *run) machineFindings() (fs []Finding, failed bool) {
	if r.o.Install != nil {
		found, err := limited(r.o.limit(installLimit), func() ([]Finding, error) { return r.o.Install(), nil })
		fs = append(fs, found...)
		if err != nil {
			failed = true
			fs = append(fs, Finding{"status", "failed", "fyr", oneLine(err.Error())})
		}
	}
	if r.o.Metrics != nil {
		found, err := limited(r.o.limit(metricsLimit), r.o.Metrics)
		fs = append(fs, found...)
		if err != nil {
			failed = true
			fs = append(fs, Finding{"metrics", "failed", "metrics.db", oneLine(err.Error())})
		}
	}
	queued, err := brain.OpenRecords(brain.Options{Home: r.o.Home, Global: true, Now: r.o.Now}, "proposal", 1000)
	if err != nil {
		failed = true
		fs = append(fs, Finding{"trail", "failed", "brain.db", oneLine(err.Error())})
	}
	for _, h := range queued {
		t, err := time.Parse("2006-01-02", h.Date)
		if h.Status == "queued" && err == nil && r.o.now().Sub(t) > queuedAfter {
			fs = append(fs, Finding{"trail", "proposal-queued", h.ID, "queued since " + h.Date})
		}
	}
	// The checks above can take minutes: the machine is read again right
	// before `/usage` (which runs only past observe).
	if !r.again("the quota") || r.cfg.Level == LevelObserve {
		sortFindings(fs)
		return fs, failed
	}
	text, err := r.usage()
	if err == nil {
		var pools []Pool
		if pools, err = parseUsage(text); err == nil {
			r.st.Budget = budgetLine(pools)
			r.say(r.st.Budget)
		}
	}
	if err != nil {
		r.st.Budget = "budget: /usage unreadable"
		r.say("budget: /usage unreadable: " + oneLine(err.Error()))
		fs = append(fs, Finding{"quota", "unreadable", "agy /usage", "the quota could not be read"})
	}
	sortFindings(fs)
	return fs, failed
}
