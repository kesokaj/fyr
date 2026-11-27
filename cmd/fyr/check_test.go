package main

import (
	"bytes"
	"encoding/json"
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
	const metricsUnknown = `fyr metrics: unknown subcommand "frob" (ingest, show, report, evals, trajectory, export, record-run)` + "\n"
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

func TestResumeVerb(t *testing.T) {
	home := t.TempDir()
	root := checkRepo(t, map[string]string{
		"AGENTS.md": "# AGENTS.md\n",
		"PRD.md":    "# PRD.md\n",
		"TODO.md":   "# TODO.md\n\n## Now\n\n- [ ] Task 1\n",
	})
	before := gitStatus(t, root)
	var out bytes.Buffer
	err := run([]string{"resume", "--home", home}, &out)
	if code := exitOf(err); code != 0 {
		t.Fatalf("resume: exit %d, out %q", code, out.String())
	}
	for _, want := range []string{
		"## AGENTS.md\n# AGENTS.md\n",
		"## Open records\n(none)\n",
		"## Newest handoff\n(none)\n",
		"## TODO.md Now\n## Now\n\n- [ ] Task 1\n",
		"## Open plans\n(none)\n",
		"## Recent changes\n(none)\n",
		"## Memory\n(none)\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("resume missing %q in:\n%s", want, out.String())
		}
	}
	if after := gitStatus(t, root); after != before {
		t.Errorf("resume changed checkout:\n%s\nwas:\n%s", after, before)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("resume wrote under --home: %v", entries)
	}

	t.Chdir(t.TempDir())
	out.Reset()
	err = run([]string{"resume", "--home", home}, &out)
	if code := exitOf(err); code != 2 || !strings.Contains(out.String(), "is not a git checkout") {
		t.Errorf("resume outside checkout: exit %d, out %q", code, out.String())
	}
}

func TestGitCheckVerb(t *testing.T) {
	home := t.TempDir()
	root := checkRepo(t, map[string]string{"README.md": "hello\n"})
	if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := run([]string{"git-check", "--home", home}, &out)
	if code := exitOf(err); code != 0 {
		t.Fatalf("git-check: exit %d, out %q", code, out.String())
	}
	for _, want := range []string{
		"## git status --short --untracked-files=all\n?? untracked.txt\n",
		"## git log --branches --not --remotes --oneline\n",
		"## git tag\n(none)\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("git-check missing %q in:\n%s", want, out.String())
		}
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("git-check wrote under --home: %v", entries)
	}

	t.Chdir(t.TempDir())
	out.Reset()
	err = run([]string{"git-check", "--home", home}, &out)
	if code := exitOf(err); code != 2 || !strings.Contains(out.String(), "is not a git checkout") {
		t.Errorf("git-check outside checkout: exit %d, out %q", code, out.String())
	}
}

func TestVocabularyCoversStateAndAutoForce(t *testing.T) {
	v := vocabulary()
	st, ok := v["state"]
	if !ok {
		t.Fatal("vocabulary missing state")
	}
	for _, sub := range []string{"export", "import"} {
		if _, ok := st.Subverbs[sub]; !ok {
			t.Errorf("state missing subverb %q", sub)
		}
	}
	if !st.Subverbs["export"]["include-memory"] || !st.Subverbs["import"]["dry-run"] {
		t.Errorf("state subverb flags unexpected: %+v", st.Subverbs)
	}
	if !v["auto"].Subverbs["run"]["force"] {
		t.Errorf("auto run missing --force in vocabulary: %+v", v["auto"].Subverbs["run"])
	}
}

func TestVocabularyMetricsRecordRunFlags(t *testing.T) {
	v := vocabulary()
	recRun := v["metrics"].Subverbs["record-run"]
	if !recRun["json-in"] || !recRun["json"] || !recRun["home"] || !recRun["repo"] {
		t.Errorf("metrics record-run missing expected flags: %+v", recRun)
	}
	if recRun["since"] || recRun["source"] {
		t.Errorf("metrics record-run unexpectedly accepts unrelated flags: %+v", recRun)
	}
	covered := map[string]bool{}
	for _, sub := range metricsSubverbs {
		for f := range v["metrics"].Subverbs[sub] {
			covered[f] = true
		}
	}
	for f := range v["metrics"].Flags {
		if !covered[f] {
			t.Errorf("metrics flag --%s is not mapped to any metrics subverb", f)
		}
	}
}

func TestVocabularyMemorySubverbFlags(t *testing.T) {
	v := vocabulary()
	mem := v["memory"]
	add := mem.Subverbs["add"]
	show := mem.Subverbs["show"]
	for _, want := range []string{"type", "name", "description", "body-file", "global", "json-in", "provenance", "home", "repo", "dir", "C", "help"} {
		if !add[want] {
			t.Errorf("memory add missing --%s", want)
		}
	}
	for _, forbid := range []string{"json", "include-low", "limit"} {
		if add[forbid] {
			t.Errorf("memory add must not accept --%s", forbid)
		}
	}
	for _, want := range []string{"global", "include-low", "json", "limit", "home", "repo", "dir", "C", "help"} {
		if !show[want] {
			t.Errorf("memory show missing --%s", want)
		}
	}
	for _, forbid := range []string{"type", "name", "description", "body-file", "json-in", "provenance"} {
		if show[forbid] {
			t.Errorf("memory show must not accept --%s", forbid)
		}
	}
	covered := map[string]bool{}
	for _, sub := range memorySubverbs {
		for f := range mem.Subverbs[sub] {
			covered[f] = true
			if !mem.Flags[f] {
				t.Errorf("memory %s lists unknown flag --%s not on memory FlagSet", sub, f)
			}
		}
	}
	for f := range mem.Flags {
		if !covered[f] {
			t.Errorf("memory flag --%s is not mapped to any memory subverb", f)
		}
	}
	// End-to-end check.Run: valid subverb flags pass; cross-subverb misuse fails.
	good := checkRepo(t, map[string]string{
		"README.md": "Use `fyr memory add --type procedural --name k --description d --provenance operator --global` and `fyr memory show --global --include-low --limit 5 --json`.\n",
	})
	if rep, err := check.Run(good, v); err != nil || len(rep.Findings) != 0 {
		t.Fatalf("good memory docs: err=%v findings=%+v", err, rep.Findings)
	}
	bad := checkRepo(t, map[string]string{
		"README.md": "Bad: `fyr memory add --json` and `fyr memory show --type semantic`.\n",
	})
	rep, err := check.Run(bad, v)
	if err != nil {
		t.Fatalf("bad memory docs: %v", err)
	}
	if len(rep.Findings) != 2 || rep.Findings[0].Rule != "flag" || rep.Findings[1].Rule != "flag" {
		t.Fatalf("bad memory docs findings = %+v, want 2 flag findings", rep.Findings)
	}
}

func TestCheckDocsStyleBudgetAndSpecStructureCLI(t *testing.T) {
	home := t.TempDir()
	specJSON := func(id, body string) string {
		b, err := json.Marshal(map[string]string{
			"date": "2026-11-24", "id": id, "kind": "spec", "status": "current",
			"title": id, "updated": "2026-11-24T10:00:00Z", "body": body,
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "\n"
	}

	t.Run("findings_text_and_json", func(t *testing.T) {
		checkRepo(t, map[string]string{
			"internal/a.go": "package a\n", "README.md": "A seamless workflow.\n",
			"AGENTS.md": strings.Repeat("a", 4501), ".agents/trail.jsonl": specJSON("SPEC-bad", "Code `internal/a.go`.\n"),
		})
		out, errOut, code := checkCmd(t, "--home", home)
		if code != 1 || errOut != "" {
			t.Fatalf("text check: exit %d, stderr %q, out:\n%s", code, errOut, out)
		}
		for _, want := range []string{
			"AGENTS.md  budget  AGENTS.md is 4501 bytes, over the 4500-byte ceiling",
			"README.md:1  docs-style  ", "SPEC-bad  spec-structure  ",
			"check: 3 findings (docs 2, specs 1, requirements 0, traces 0);",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("text output missing %q:\n%s", want, out)
			}
		}
		jout, jerr, jcode := checkCmd(t, "--json", "--home", home)
		var rep checkReportJSON
		if jcode != 1 || jerr != "" || json.Unmarshal([]byte(jout), &rep) != nil {
			t.Fatalf("json check: exit %d, stderr %q, out:\n%s", jcode, jerr, jout)
		}
		if rep.OK || rep.Counts.Total != 3 || rep.Counts.Docs != 2 || rep.Counts.Specs != 1 {
			t.Errorf("json counts = %+v (ok=%v), want total=3 docs=2 specs=1", rep.Counts, rep.OK)
		}
		rules := map[string]bool{}
		for _, f := range rep.Findings {
			rules[f.Rule] = true
		}
		for _, want := range []string{"docs-style", "budget", "spec-structure"} {
			if !rules[want] {
				t.Errorf("json findings missing rule %q: %+v", want, rep.Findings)
			}
		}
	})

	t.Run("advisory_notes_exit_0_text_and_json", func(t *testing.T) {
		specBody := "Code `internal/a.go`.\n## Purpose\nx\n## Interfaces\nx\n## Data\nx\n## Failure modes\nProse only.\n## Verification\nx\n"
		longLine := "This prose sentence intentionally exceeds twenty five words by adding many extra words and clauses to trigger the advisory style note without causing a hard error.\n"
		recJSON := func(id, kind, body string) string {
			b, err := json.Marshal(map[string]string{
				"date": "2026-11-24", "id": id, "kind": kind, "status": "current",
				"title": id, "updated": "2026-11-24T10:00:00Z", "body": body,
			})
			if err != nil {
				t.Fatal(err)
			}
			return string(b) + "\n"
		}
		checkRepo(t, map[string]string{
			"internal/a.go": "package a\n", "README.md": longLine + "Use whitelist here.\n",
			".agents/glossary.json": `{"whitelist":"allowlist"}`,
			".agents/trail.jsonl": specJSON("SPEC-ok", specBody) +
				recJSON("GUIDE-agent", "guide", longLine+"Seamless e.g. whitelist.\n") +
				recJSON("DEPLOY-agent", "deploy", longLine+"Zero-ops etc.\n"),
		})
		out, errOut, code := checkCmd(t, "--home", home)
		if code != 0 || errOut != "" || !strings.Contains(out, "check: 0 findings (docs 0, specs 0, requirements 0, traces 0);") {
			t.Fatalf("advisory text check: exit %d, stderr %q, out:\n%s", code, errOut, out)
		}
		for _, want := range []string{"note: docs-style-note:", "allowlist", "4-column"} {
			if !strings.Contains(out, want) {
				t.Errorf("advisory text output missing %q:\n%s", want, out)
			}
		}
		for _, forbid := range []string{"GUIDE-agent", "DEPLOY-agent"} {
			if strings.Contains(out, forbid) {
				t.Errorf("agent-only DB record %s must not emit docs-style findings or notes:\n%s", forbid, out)
			}
		}
		jout, jerr, jcode := checkCmd(t, "--json", "--home", home)
		var rep checkReportJSON
		if jcode != 0 || jerr != "" || json.Unmarshal([]byte(jout), &rep) != nil || !rep.OK || rep.Counts.Total != 0 || len(rep.Notes) < 3 {
			t.Fatalf("advisory json: exit %d, stderr %q, rep=%+v\n%s", jcode, jerr, rep, jout)
		}
	})

	t.Run("agent_db_safety_guards_still_fail", func(t *testing.T) {
		deployBad, err := json.Marshal(map[string]string{
			"date": "2026-11-24", "id": "DEPLOY-bad", "kind": "deploy", "status": "current",
			"title": "DEPLOY-bad", "updated": "2026-11-24T10:00:00Z",
			"body": "---\ntitle: bad\n---\n\n```bash\nrm -rf ./state\n```\n",
		})
		if err != nil {
			t.Fatal(err)
		}
		checkRepo(t, map[string]string{".agents/trail.jsonl": string(deployBad) + "\n"})
		out, errOut, code := checkCmd(t, "--home", home)
		if code != 1 || errOut != "" || !strings.Contains(out, "DEPLOY-bad:1  docs-style") || !strings.Contains(out, "DEPLOY-bad:6  docs-style") {
			t.Fatalf("agent db safety guard check: exit %d, stderr %q, out:\n%s", code, errOut, out)
		}
	})
}
