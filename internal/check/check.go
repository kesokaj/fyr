// Package check is `fyr check`: a read-only drift check of a git checkout's
// docs, specs, requirements and R → M → T traces against the running binary and the tree
// (ADR-064, spec SPEC-fyr-check). It reads files, git and the checkout's
// .agents/trail.jsonl, and writes nothing: no index, no brain store, no
// state directory. It opens no SQLite database: internal/brain is imported
// for ReadTrail alone, so the driver's direct users stay the three R4 names.
package check

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/trail"
)

// VerbSpec is one verb of the running binary. Flags are the verb's own and
// the two global ones. Subverbs maps each subverb to the flags it takes, the
// global ones included; nil for a verb without subverbs.
type VerbSpec struct {
	Flags    map[string]bool
	Subverbs map[string]map[string]bool
}

// Vocabulary maps each verb name, "help" included, to its spec.
type Vocabulary map[string]VerbSpec

// Finding is one place where the docs and the tree disagree.
type Finding struct {
	Where string // repo-relative path or record ID
	Line  int    // 1-based; 0 when the rule has none
	Rule  string // verb subverb flag path line spec-stale spec-unlinked requirement trace
	Text  string
}

// Report is what Run found, and the notes on what it could not check.
type Report struct {
	Findings []Finding
	Notes    []string
}

// ErrRefused wraps a reason Run checked nothing for.
var ErrRefused = errors.New("check refused")

// refusal is a reason Run checked nothing for; it is ErrRefused, and its
// text is the reason alone, which the CLI prints after "check: ".
type refusal struct{ reason string }

func (r refusal) Error() string        { return r.reason }
func (r refusal) Is(target error) bool { return target == ErrRefused }

// livingKinds are the record kinds that describe the tree as it is, read
// while their status is current; every other kind is history.
var livingKinds = map[string]bool{"spec": true, "guide": true, "deploy": true}

// notInRuleA are root files that name what is not built yet (plan D6;
// PLAN.md item 38).
var notInRuleA = map[string]bool{"ROADMAP.md": true, "TODO.md": true, "PLAN.md": true}

// vendoredRow is a table row of skills/VENDORED.md naming a vendored skill
// or catalog, read as restatement-grep.sh reads it (plan D5).
var vendoredRow = regexp.MustCompile(`^\| ([a-z0-9/-]+) \|`)

// checker is one run's view of the checkout.
type checker struct {
	top      string
	root     *os.Root
	tracked  []string
	topDirs  map[string]bool
	topFiles map[string]bool
	lines    map[string]int
	links    map[string]bool
	tests    map[string]bool // Go tests declared in tracked *_test.go, read once
	skipped  []string        // tracked docs and tests readFile refused, for one note (IN53)
}

// Run checks the checkout that holds dir against v. It writes nothing.
func Run(dir string, v Vocabulary) (Report, error) {
	_, top, err := trail.Roots(dir)
	if err != nil {
		if errors.Is(err, trail.ErrNotCheckout) {
			return Report{}, refusal{dir + " is not a git checkout"}
		}
		return Report{}, refusal{err.Error()}
	}
	k, err := open(top)
	if err != nil {
		return Report{}, err
	}
	defer k.root.Close()
	var rep Report
	recs, ok, err := brain.ReadTrail(top)
	if err != nil {
		return Report{}, refusal{err.Error()}
	}
	if !ok {
		rep.Notes = append(rep.Notes, "no .agents/trail.jsonl: records, specs not checked")
	}
	var living, specs []brain.Record
	for _, r := range recs {
		if livingKinds[r.Kind] && r.Status == "current" {
			living = append(living, r)
			if r.Kind == "spec" {
				specs = append(specs, r)
			}
		}
	}
	rep.Findings = k.docsRule(k.docsFiles(), living, v)
	rep.Findings = append(rep.Findings, k.verificationFindings(specs)...)
	if note := k.historyNote(); note != "" {
		rep.Notes = append(rep.Notes, note)
	} else {
		fs, notes := k.specsRule(specs)
		rep.Findings = append(rep.Findings, fs...)
		rep.Notes = append(rep.Notes, notes...)
	}
	k.rootDocsRules(&rep, recs)
	// A drift check that read fewer files than it names says so: a clean
	// result is otherwise no sign (IN53 of the 2026-10-27 audit).
	if n := len(k.skipped); n > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("%d tracked files skipped (unreadable, a link or over 16 MiB), first %s", n, k.skipped[0]))
	}
	sortFindings(rep.Findings)
	return rep, nil
}

// rootDocsRules adds rules (c) and (d), which read the root documents, to rep.
func (k *checker) rootDocsRules(rep *Report, recs []brain.Record) {
	reqs, notes := k.requirementsRule(recs)
	rep.Findings = append(rep.Findings, reqs...)
	rep.Notes = append(rep.Notes, notes...)
	traces, notes := k.tracesRule()
	rep.Findings = append(rep.Findings, traces...)
	rep.Notes = append(rep.Notes, notes...)
}

// open lists what git tracks at top and opens the checkout as an os.Root.
func open(top string) (*checker, error) {
	out, err := trail.Command(top, "ls-files", "-z").Output()
	if err != nil {
		return nil, refusal{fmt.Sprintf("git ls-files in %s: %v", top, err)}
	}
	root, err := os.OpenRoot(top)
	if err != nil {
		return nil, refusal{err.Error()}
	}
	k := &checker{top: top, root: root, topDirs: map[string]bool{}, topFiles: map[string]bool{}, lines: map[string]int{}, links: map[string]bool{}}
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		k.tracked = append(k.tracked, p)
		if first, _, nested := strings.Cut(p, "/"); nested {
			k.topDirs[first] = true
		} else {
			k.topFiles[first] = true
		}
	}
	return k, nil
}

// readFile reads a regular file of at most maxLineRead bytes inside the
// checkout; a symlink or anything larger is an error (os.Root has no
// ReadFile before Go 1.25, and the go line is 1.24).
func (k *checker) readFile(name string) ([]byte, error) {
	fi, err := k.root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxLineRead {
		return nil, fmt.Errorf("%s: not a regular file of at most %d bytes", name, maxLineRead)
	}
	f, err := k.openRegular(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxLineRead))
}

// openRegular opens name without blocking and checks the opened file, not
// the name: what was swapped in after readFile's Lstat is refused unless it
// is a regular file of at most maxLineRead bytes.
func (k *checker) openRegular(name string) (*os.File, error) {
	f, err := k.root.OpenFile(name, os.O_RDONLY|nonBlock, 0)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err == nil && (!fi.Mode().IsRegular() || fi.Size() > maxLineRead) {
		err = fmt.Errorf("%s: not a regular file of at most %d bytes", name, maxLineRead)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func (k *checker) git(args ...string) *exec.Cmd { return trail.Command(k.top, args...) }

// docsFiles is the docs set's files (Interfaces § The docs set): every
// tracked *.md, minus a directory holding a noindex marker (tracked or
// not) and the vendored skills and catalogs skills/VENDORED.md names.
func (k *checker) docsFiles() []string {
	skip := k.vendoredSkips()
	isMarked := k.noIndexed()
	var files []string
next:
	for _, p := range k.tracked {
		if !strings.HasSuffix(p, ".md") {
			continue
		}
		for _, s := range skip {
			if strings.HasPrefix(p, s) {
				continue next
			}
		}
		if isMarked(path.Dir(p)) {
			continue
		}
		files = append(files, p)
	}
	return files
}

// vendoredSkips is the skills/<name>/ prefix of every vendored skill and
// catalog skills/VENDORED.md names.
func (k *checker) vendoredSkips() []string {
	var skip []string
	if data, err := k.readFile(filepath.Join("skills", "VENDORED.md")); err == nil {
		for _, l := range strings.Split(string(data), "\n") {
			if m := vendoredRow.FindStringSubmatch(l); m != nil && m[1] != "Skill" {
				skip = append(skip, "skills/"+m[1]+"/")
			}
		}
	}
	return skip
}

// noIndexed returns the check of whether a directory or one above it holds
// a noindex marker, tracked or not; each answer is kept.
func (k *checker) noIndexed() func(dir string) bool {
	marked := map[string]bool{}
	for _, p := range k.tracked {
		if trail.IsNoIndexMarker(p) {
			marked[path.Dir(p)] = true
		}
	}
	return func(dir string) bool {
		for d := dir; ; d = path.Dir(d) {
			if _, seen := marked[d]; !seen {
				m := false
				for _, name := range trail.NoIndexMarkers {
					if _, err := k.root.Lstat(filepath.Join(filepath.FromSlash(d), name)); err == nil {
						m = true
					}
				}
				marked[d] = m
			}
			if marked[d] {
				return true
			}
			if d == "." {
				return false
			}
		}
	}
}

// docsRule is rule (a) over the files and the living records: every `fyr`
// verb, subverb and flag in their code, and every path candidate in their
// inline spans (D26).
func (k *checker) docsRule(files []string, living []brain.Record, v Vocabulary) []Finding {
	type doc struct{ where, text string }
	var docs []doc
	for _, p := range files {
		if notInRuleA[p] {
			continue
		}
		data, err := k.readFile(filepath.FromSlash(p))
		if err != nil {
			k.skipped = append(k.skipped, p)
			continue
		}
		docs = append(docs, doc{p, string(data)})
	}
	for _, r := range living {
		docs = append(docs, doc{r.ID, r.Body})
	}
	var out []Finding
	var cands []candidate
	for _, d := range docs {
		for _, c := range scan(d.text) {
			out = append(out, verbFindings(d.where, c, v)...)
			if !c.fence {
				if cand, ok := k.pathCandidate(d.where, c); ok {
					cands = append(cands, cand)
				}
			}
		}
	}
	var ps []string
	for _, c := range cands {
		ps = append(ps, c.p)
	}
	ign := k.ignored(ps)
	for _, c := range cands {
		if ign[c.p] {
			continue
		}
		if f, ok := k.checkPath(c); ok {
			out = append(out, f)
		}
	}
	return out
}

// ruleOrder puts rule (a)'s findings first, then (b)'s, (c)'s and (d)'s.
var ruleOrder = map[string]int{"verb": 0, "subverb": 0, "flag": 0, "path": 0, "line": 0, "spec-stale": 1, "spec-unlinked": 1, "requirement": 2, "trace": 3}

func sortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		switch {
		case ruleOrder[a.Rule] != ruleOrder[b.Rule]:
			return ruleOrder[a.Rule] < ruleOrder[b.Rule]
		case a.Where != b.Where:
			return a.Where < b.Where
		case a.Line != b.Line:
			return a.Line < b.Line
		case a.Rule != b.Rule:
			return a.Rule < b.Rule
		}
		return a.Text < b.Text
	})
}

// Counts is how many findings each rule group holds: docs, specs,
// requirements and traces, for the summary line.
func Counts(fs []Finding) (docs, specs, reqs, traces int) {
	for _, f := range fs {
		switch ruleOrder[f.Rule] {
		case 0:
			docs++
		case 1:
			specs++
		case 2:
			reqs++
		default:
			traces++
		}
	}
	return docs, specs, reqs, traces
}
