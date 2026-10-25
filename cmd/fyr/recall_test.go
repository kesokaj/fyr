package main

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The recall test measures how well a retrieval arm finds the records and
// files a question is about, over this repository's own history (plan
// 2026-09-29-search-index.md, Phase 0 and Phase 4; moved here from
// internal/search by the brain plan, Phase 2, because the history is records
// now and only the CLI prints both). It is a measurement, not a pass/fail
// gate, so it runs only when FYR_RECALL=1 and prints its numbers; a testing
// record holds them.
//
// testdata/recall.tsv holds one question per line: `question<TAB>gold,gold…`,
// each gold a record ID (BUG-006) or a file path.
// A question starting `refs X:` or `open K:` is structured: its arm returns a
// complete list, so recall is counted over the whole list. Every other
// question is scored as recall@10 over a ranked list.

type recallQuestion struct {
	text string
	gold []string
	mode string // "words", "refs", "open"
	arg  string // the refs target or the open kind
}

var structuredPrefix = regexp.MustCompile(`^(refs|open) (\S+): `)

func loadRecallQuestions(t *testing.T) []recallQuestion {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "recall.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	var qs []recallQuestion
	for n, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 2 {
			t.Fatalf("recall.tsv line %d: want 2 tab-separated fields, got %d", n+1, len(fields))
		}
		q := recallQuestion{text: fields[0], gold: strings.Split(fields[1], ","), mode: "words"}
		if m := structuredPrefix.FindStringSubmatch(q.text); m != nil {
			q.mode, q.arg = m[1], m[2]
		}
		qs = append(qs, q)
	}
	return qs
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("recall test needs a git checkout: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// stopWords are dropped from a question before either arm sees it.
var stopWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a an and are as at be because but by did does each every
		file files for from had has how in instead into is it its living not of on one only or
		says so still than that the their them then there these they this to until was were what
		when where which while who why with without drift refs open records mention name`) {
		stopWords[w] = true
	}
}

var wordPattern = regexp.MustCompile(`[A-Za-z0-9_][A-Za-z0-9_.\-/]*[A-Za-z0-9_]`)

func questionWords(text string) []string {
	if m := structuredPrefix.FindStringIndex(text); m != nil {
		text = text[m[1]:]
	} else {
		text = strings.TrimPrefix(text, "drift: ")
	}
	var words []string
	seen := map[string]bool{}
	for _, w := range wordPattern.FindAllString(strings.ToLower(text), -1) {
		if len(w) < 3 || stopWords[w] || seen[w] {
			continue
		}
		seen[w] = true
		words = append(words, w)
	}
	return words
}

// grepArm is the baseline: what one pass of plain grep gives an agent. Each
// question word is searched with `git grep -c -i -F`; files are ranked by the
// number of distinct words they contain, then by total hits, then by path.
// For a refs question it lists every file containing the target literally.
func grepArm(t *testing.T, root string, q recallQuestion) []string {
	t.Helper()
	if q.mode == "refs" {
		return gitGrepFiles(t, root, q.arg)
	}
	distinct := map[string]int{}
	hits := map[string]int{}
	for _, w := range questionWords(q.text) {
		for path, n := range gitGrepCounts(t, root, w) {
			distinct[path]++
			hits[path] += n
		}
	}
	paths := make([]string, 0, len(distinct))
	for p := range distinct {
		paths = append(paths, p)
	}
	sort.Slice(paths, func(i, j int) bool {
		a, b := paths[i], paths[j]
		if distinct[a] != distinct[b] {
			return distinct[a] > distinct[b]
		}
		if hits[a] != hits[b] {
			return hits[a] > hits[b]
		}
		return a < b
	})
	return paths
}

// gitGrepCounts is `git grep -c` per file, except the brain store's export,
// which grep sees as one file: its matching lines count per record ID, as an
// agent grepping the trail would read them.
func gitGrepCounts(t *testing.T, root, word string) map[string]int {
	t.Helper()
	counts := fileGrepCounts(t, root, word)
	if _, ok := counts[".agents/trail.jsonl"]; !ok {
		return counts
	}
	delete(counts, ".agents/trail.jsonl")
	cmd := exec.Command("git", "grep", "-h", "-i", "-F", "--", word, ".agents/trail.jsonl")
	cmd.Dir = root
	out, _ := cmd.Output()
	for _, l := range strings.Split(string(out), "\n") {
		if m := trailID.FindStringSubmatch(l); m != nil {
			counts[m[1]] += strings.Count(strings.ToLower(l), strings.ToLower(word))
		}
	}
	return counts
}

var trailID = regexp.MustCompile(`"id":"([^"]+)"`)

func fileGrepCounts(t *testing.T, root, word string) map[string]int {
	t.Helper()
	cmd := exec.Command("git", "grep", "-c", "-i", "-F", "--", word)
	cmd.Dir = root
	out, err := cmd.Output()
	counts := map[string]int{}
	if err != nil {
		// git grep exits 1 when nothing matches; anything else is a failure.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return counts
		}
		t.Fatalf("git grep %q: %v", word, err)
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		i := strings.LastIndexByte(line, ':')
		if i < 0 {
			continue
		}
		n, err := strconv.Atoi(line[i+1:])
		if err != nil {
			continue
		}
		counts[line[:i]] = n
	}
	return counts
}

func gitGrepFiles(t *testing.T, root, needle string) []string {
	t.Helper()
	counts := gitGrepCounts(t, root, needle)
	paths := make([]string, 0, len(counts))
	for p := range counts {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// recall is the share of gold paths found in got; a words question looks at
// the first ten only.
func recall(q recallQuestion, got []string) float64 {
	// The questions themselves live in testdata/: a hit there answers
	// nothing, for either arm.
	kept := got[:0:0]
	for _, p := range got {
		if !strings.HasPrefix(p, "cmd/fyr/testdata/") {
			kept = append(kept, p)
		}
	}
	got = kept
	if q.mode == "words" && len(got) > 10 {
		got = got[:10]
	}
	in := map[string]bool{}
	for _, p := range got {
		in[p] = true
	}
	found := 0
	for _, g := range q.gold {
		if in[g] {
			found++
		}
	}
	return float64(found) / float64(len(q.gold))
}

type recallArm struct {
	name string
	run  func(t *testing.T, root string, q recallQuestion) []string
}

// indexArm asks `fyr search` the same question with the same words: ranked
// words for an ordinary or drift question, --refs or --open for a structured
// one, against a scratch home whose brain store loads the checkout's
// trail.jsonl. It returns the printed record IDs and paths in order.
func indexArm(t *testing.T, root string, q recallQuestion) []string {
	t.Helper()
	args := []string{"search", "--limit", "11", "--home", recallHome} // one slot for the testdata file recall drops
	switch q.mode {
	case "refs":
		args = append(args, "--refs", q.arg)
	case "open":
		args = append(args, "--open", "--kind", q.arg)
	default:
		args = append(args, questionWords(q.text)...)
	}
	t.Chdir(root)
	var out bytes.Buffer
	if err := run(args, &out); err != nil {
		t.Fatalf("search %q: %v\n%s", q.text, err, out.String())
	}
	return printedPaths(out.String())
}

// recallHome is the scratch home every indexArm call shares, so the store
// loads the trail once.
var recallHome string

var (
	hitLine    = regexp.MustCompile(`^(?:\S+  )?([^\s:][^\s]*?):\d+  `)
	recordLine = regexp.MustCompile(`^(?:\S+:)?([A-Z]+-[A-Za-z0-9._-]+)  [a-z]+`)
)

// printedPaths lists, in order and once each, the record IDs and the file
// paths of hit lines in search output.
func printedPaths(out string) []string {
	var paths []string
	seen := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, " ") {
			continue
		}
		if m := recordLine.FindStringSubmatch(l); m != nil && !strings.Contains(m[1], "/") {
			if !seen[m[1]] {
				seen[m[1]] = true
				paths = append(paths, m[1])
			}
			continue
		}
		if m := hitLine.FindStringSubmatch(l); m != nil && !seen[m[1]] {
			seen[m[1]] = true
			paths = append(paths, m[1])
		}
	}
	return paths
}

// recallArms lists the arms the test compares.
var recallArms = []recallArm{{"grep", grepArm}, {"index", indexArm}}

func TestRecall(t *testing.T) {
	if os.Getenv("FYR_RECALL") != "1" {
		t.Skip("measurement, not a gate: set FYR_RECALL=1 to run it")
	}
	root := repoRoot(t)
	recallHome = t.TempDir()
	qs := loadRecallQuestions(t)
	for _, arm := range recallArms {
		sums := map[string]float64{}
		counts := map[string]int{}
		for i, q := range qs {
			r := recall(q, arm.run(t, root, q))
			class := q.mode
			if strings.HasPrefix(q.text, "drift: ") {
				class = "drift"
			}
			sums[class] += r
			counts[class]++
			sums["all"] += r
			counts["all"]++
			t.Logf("%s q%02d %-5s %.2f  %s", arm.name, i+1, class, r, q.text)
		}
		for _, class := range []string{"words", "drift", "refs", "open", "all"} {
			if counts[class] > 0 {
				t.Logf("%s mean %-5s %.3f over %d", arm.name, class, sums[class]/float64(counts[class]), counts[class])
			}
		}
	}
}

func TestRecallQuestionsAreWellFormed(t *testing.T) {
	root := repoRoot(t)
	qs := loadRecallQuestions(t)
	trail, err := os.ReadFile(filepath.Join(root, ".agents", "trail.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, m := range trailID.FindAllStringSubmatch(string(trail), -1) {
		ids[m[1]] = true
	}
	var drift, structured int
	for _, q := range qs {
		if strings.HasPrefix(q.text, "drift: ") {
			drift++
		}
		if q.mode != "words" {
			structured++
		}
		for _, g := range q.gold {
			if recordLine.MatchString(g+"  x") && !strings.Contains(g, "/") {
				if !ids[g] {
					t.Errorf("gold record %s of %q is not in .agents/trail.jsonl", g, q.text)
				}
				continue
			}
			if _, err := os.Stat(filepath.Join(root, g)); err != nil {
				t.Errorf("gold path %s of %q: %v", g, q.text, err)
			}
		}
	}
	if len(qs) < 30 || drift < 10 || structured < 5 {
		t.Errorf("recall.tsv has %d questions, %d drift, %d structured; the plan asks for at least 30, 10 and 5", len(qs), drift, structured)
	}
}
