package main

import (
	"bytes"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/check"
)

// The vocabulary is every verb of the table with the flags its FlagSet
// really takes, the globals on all; record's subverbs take recordFlags'
// flags, so `record get --title` is not taken (plan fyr-check D14).
func TestVocabularyVerbsAndFlags(t *testing.T) {
	v := vocabulary()
	for name, vb := range verbs {
		spec, ok := v[name]
		if !ok {
			t.Errorf("verb %s is not in the vocabulary", name)
			continue
		}
		c := newCtx(name, vb, &bytes.Buffer{})
		c.fs.VisitAll(func(f *flag.Flag) {
			if !spec.Flags[f.Name] {
				t.Errorf("%s takes --%s, the vocabulary does not", name, f.Name)
			}
		})
		for f := range spec.Flags {
			if f != "help" && c.fs.Lookup(f) == nil {
				t.Errorf("the vocabulary gives %s --%s, its FlagSet does not", name, f)
			}
		}
		if !spec.Flags["home"] || !spec.Flags["repo"] {
			t.Errorf("%s lacks a global flag: %v", name, spec.Flags)
		}
	}
	if _, ok := v["help"]; !ok {
		t.Error("help is not in the vocabulary")
	}
	if _, ok := v["check"]; !ok {
		t.Error("check is not in the vocabulary")
	}
	rec := v["record"].Subverbs
	if len(rec) != len(recordFlags) {
		t.Errorf("record subverbs %v, want recordFlags' %d", rec, len(recordFlags))
	}
	for sub, takes := range recordFlags {
		for f := range takes {
			if !rec[sub][f] {
				t.Errorf("record %s lacks --%s", sub, f)
			}
		}
		if !rec[sub]["home"] || !rec[sub]["repo"] {
			t.Errorf("record %s lacks a global flag", sub)
		}
	}
	if rec["get"]["title"] {
		t.Error("record get takes --title in the vocabulary; its dispatcher refuses it")
	}
}

// Each subverb list is what its dispatcher accepts: every listed word
// reaches its case, and an unlisted word gets the refusal text it had
// before the lists (D14).
func TestVocabularySubverbListsMatchDispatchers(t *testing.T) {
	home := t.TempDir()
	t.Chdir(t.TempDir())
	call := func(args ...string) string {
		var out bytes.Buffer
		run(append(args, "--home", home), &out)
		return out.String()
	}
	metricsSynopsis := strings.Join(pages["metrics"].synopsis, "\n") + "\n"
	const metricsUnknown = `fyr metrics: unknown subcommand "frob" (ingest, show, report, export)` + "\n"
	if got := call("metrics", "frob"); !strings.HasPrefix(got, metricsUnknown+metricsSynopsis) || !strings.HasSuffix(got, "run 'fyr help metrics' for its flags\n") {
		t.Errorf("metrics frob: %q, want the reason, the synopsis and the hint", got)
	}
	if got := call("metrics"); !strings.HasPrefix(got, "usage: fyr metrics") {
		t.Errorf("bare metrics: %q", got)
	}
	for _, sub := range metricsSubverbs {
		if got := call("metrics", sub); strings.Contains(got, "unknown subcommand") {
			t.Errorf("metrics %s does not reach its case: %q", sub, got)
		}
	}
	memorySynopsis := strings.Join(pages["memory"].synopsis, "\n") + "\n"
	if got := call("memory", "frob"); !strings.HasPrefix(got, memorySynopsis) || !strings.HasSuffix(got, "run 'fyr help memory' for its flags\n") {
		t.Errorf("memory frob: %q, want the synopsis and the hint", got)
	}
	for _, sub := range memorySubverbs {
		if got := call("memory", sub); strings.HasPrefix(got, memorySynopsis) {
			t.Errorf("memory %s does not reach its case", sub)
		}
	}
	v := vocabulary()
	for verb, list := range map[string][]string{"metrics": metricsSubverbs, "memory": memorySubverbs} {
		var got []string
		for s := range v[verb].Subverbs {
			got = append(got, s)
		}
		sort.Strings(got)
		want := append([]string(nil), list...)
		sort.Strings(want)
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("%s subverbs %v, want %v", verb, got, want)
		}
	}
}

// checkRepo is a committed scratch checkout holding files, the working
// directory moved into it.
func checkRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := recordRepo(t)
	for p, body := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "fixture"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return root
}

func gitStatus(t *testing.T, root string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "status", "--porcelain", "--untracked-files=all", "--ignored").CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	return string(out)
}

// checkCmd runs `fyr check` with stderr captured.
func checkCmd(t *testing.T, args ...string) (out, errOut string, code int) {
	t.Helper()
	var o, e bytes.Buffer
	stderr = &e
	defer func() { stderr = os.Stderr }()
	err := run(append([]string{"check"}, args...), &o)
	return o.String(), e.String(), exitOf(err)
}

// The verb end to end: exit 0 clean, 1 with the summary on findings, 1 with
// no summary and the reason on stderr for a refusal, 2 for a positional
// word; the checkout and the --home directory are unchanged (ADR-064).
func TestCheckVerb(t *testing.T) {
	home := t.TempDir()
	t.Run("clean", func(t *testing.T) {
		root := checkRepo(t, map[string]string{"README.md": "run `fyr check` and `fyr record get --global`\n", "internal/a.go": "package a\n"})
		before := gitStatus(t, root)
		out, errOut, code := checkCmd(t, "--home", home)
		if code != 0 || !strings.Contains(out, "check: 0 findings (docs 0, specs 0, requirements 0, traces 0); fyr ") || errOut != "" {
			t.Errorf("clean: exit %d, out %q, stderr %q", code, out, errOut)
		}
		if after := gitStatus(t, root); after != before {
			t.Errorf("check changed the checkout:\n%s\nwas:\n%s", after, before)
		}
	})
	t.Run("findings", func(t *testing.T) {
		root := checkRepo(t, map[string]string{"README.md": "x\n`fyr frobnicate` `internal/gone.go`\n", "internal/a.go": "package a\n"})
		before := gitStatus(t, root)
		out, _, code := checkCmd(t, "--home", home)
		want := "README.md:2  path  `internal/gone.go`: internal/gone.go does not exist\nREADME.md:2  verb  `fyr frobnicate`: fyr has no verb frobnicate\n"
		if code != 1 || !strings.HasPrefix(out, want) || !strings.Contains(out, "\ncheck: 2 findings (docs 2, specs 0, requirements 0, traces 0); fyr ") {
			t.Errorf("findings: exit %d, out %q", code, out)
		}
		if after := gitStatus(t, root); after != before {
			t.Errorf("check changed the checkout:\n%s\nwas:\n%s", after, before)
		}
	})
	t.Run("not a checkout", func(t *testing.T) {
		t.Chdir(t.TempDir())
		out, errOut, code := checkCmd(t, "--home", home)
		if code != 1 || out != "" || !strings.HasPrefix(errOut, "check: ") || !strings.Contains(errOut, "is not a git checkout") {
			t.Errorf("not a checkout: exit %d, out %q, stderr %q", code, out, errOut)
		}
	})
	t.Run("broken trail", func(t *testing.T) {
		checkRepo(t, map[string]string{".agents/trail.jsonl": "{\"id\": \n"})
		out, errOut, code := checkCmd(t, "--home", home)
		if code != 1 || strings.Contains(out, "check:") || !strings.Contains(errOut, "line 1") {
			t.Errorf("broken trail: exit %d, out %q, stderr %q", code, out, errOut)
		}
	})
	t.Run("positional word", func(t *testing.T) {
		checkRepo(t, map[string]string{"a.txt": "a\n"})
		if _, _, code := checkCmd(t, "--home", home, "extra"); code != 2 {
			t.Errorf("check extra: exit %d, want 2", code)
		}
	})
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("check wrote under --home: %v", entries)
	}
}

// The summary counts each rule group in its place: one finding of each
// group but docs gives distinct counts, so a swapped pair is seen.
func TestPrintCheckSummaryOrder(t *testing.T) {
	var b bytes.Buffer
	printCheck(&b, check.Report{Findings: []check.Finding{
		{Where: "SPEC-a", Rule: "spec-stale"},
		{Where: "PRD.md", Line: 3, Rule: "requirement"}, {Where: "PRD.md", Line: 4, Rule: "requirement"},
		{Where: "PRD.md", Line: 5, Rule: "trace"}, {Where: "PRD.md", Line: 6, Rule: "trace"}, {Where: "SDD.md", Line: 7, Rule: "trace"},
	}})
	if want := "check: 6 findings (docs 0, specs 1, requirements 2, traces 3); fyr "; !strings.Contains(b.String(), want) {
		t.Errorf("summary %q, want %q", b.String(), want)
	}
}
