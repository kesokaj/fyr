package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

// allFlags is a FlagSet holding the global flags and every verb's flags, for
// the reorder tests: the value flags are read from it, as run does per verb.
func allFlags() *flag.FlagSet {
	c := &ctx{fs: flag.NewFlagSet("all", flag.ContinueOnError)}
	c.fs.StringVar(&c.repoFlag, "repo", "", "")
	c.fs.StringVar(&c.homeFlag, "home", "", "")
	c.fs.StringVar(&c.dirFlag, "dir", "", "")
	c.fs.StringVar(&c.dirFlag, "C", "", "")
	for _, v := range verbs {
		if v.flags == nil {
			continue
		}
		// each verb registers only its own flags; a name two verbs share
		// (dry-run, global) would panic on the second definition
		sub := &ctx{fs: flag.NewFlagSet("verb", flag.ContinueOnError)}
		v.flags(sub)
		sub.fs.VisitAll(func(f *flag.Flag) {
			if c.fs.Lookup(f.Name) == nil {
				c.fs.Var(f.Value, f.Name, f.Usage)
			}
		})
	}
	return c.fs
}

func TestReorder(t *testing.T) {
	cases := map[string][]string{
		// BUG-002: the flag package stops at the first positional argument.
		"adopt my-skill --home /x":          {"--home", "/x", "adopt", "my-skill"},
		"adopt a --force":                   {"--force", "adopt", "a"},
		"adopt -- --looks-like-a-flag":      {"--", "adopt", "--looks-like-a-flag"}, // one marker, before every positional: the flag package stops there
		"--repo r --home h adopt x --force": {"--repo", "r", "--home", "h", "--force", "adopt", "x"},
		// A value flag reorder does not know swallows the next argument:
		// --project would take "--dry-run" as its value and the run would
		// call the model when a dry run was asked (BUG-002's shape).
		"dream --project /x --dry-run":              {"--project", "/x", "--dry-run", "dream"},
		"dream --dry-run":                           {"--dry-run", "dream"},
		"search --slice Reconcile --budget 1800":    {"--slice", "Reconcile", "--budget", "1800", "search"},
		"search --path EntryPoint LeafTarget":       {"--path", "EntryPoint", "search", "LeafTarget"},
		"search --affected CoreStore --home /x":     {"--affected", "CoreStore", "--home", "/x", "search"},
		"search --vet Reconcile --home /x":          {"--vet", "Reconcile", "--home", "/x", "search"},
		"search --hook pre-invocation --home /x":    {"--hook", "pre-invocation", "--home", "/x", "search"},
		"auto on --conversation-idle 2h --home /x":  {"--conversation-idle", "2h", "--home", "/x", "auto", "on"},
		"metrics show --since-tag v9.6.3 --home /x": {"--since-tag", "v9.6.3", "--home", "/x", "metrics", "show"},
		"report --since-tag v9.6.3 --md --home /x":  {"--since-tag", "v9.6.3", "--md", "--home", "/x", "report"},
		"report --daily-digest --json --home /x":    {"--daily-digest", "--json", "--home", "/x", "report"},
	}
	for in, want := range cases {
		if got := reorder(allFlags(), strings.Fields(in)); !reflect.DeepEqual(got, want) {
			t.Errorf("reorder(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestVerbRefusesAnotherVerbsFlag: every verb has its own FlagSet (IN5), so a
// flag that belongs to another verb is a usage error — exit 2, named on the
// caller's writer — instead of being parsed and silently ignored.
func TestVerbRefusesAnotherVerbsFlag(t *testing.T) {
	t.Setenv(harness.ChildEnv, "1")
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"list", "--check", "--force"},
		{"version", "--dry-run"},
		{"index", "--kind", "bug"},
		{"status", "--rebuild"},
		// The CLI refuses these before search.validate sees them, so its
		// index and --rebuild refusals were removed as unreachable (project
		// review 2026-10-09, row 23).
		{"index", "--refs", "BUG-001"},
		{"index", "--open"},
		{"search", "--rebuild", "x"},
		{"search", "--include-vendored", "x"},
		{"list", "--include-vendored"},
		{"list", "--daily-digest"},
		{"search", "--daily-digest", "x"},
	} {
		home := t.TempDir()
		var out bytes.Buffer
		err := run(append(args, "--repo", repo, "--home", home), &out)
		var code exitCode
		if !errors.As(err, &code) || code != 2 {
			t.Errorf("%q: err = %v, want exit 2", args, err)
		}
		if !strings.Contains(out.String(), "flag provided but not defined") {
			t.Errorf("%q: the refusal should name the flag on the writer, got:\n%s", args, out.String())
		}
	}
	// index takes no words either.
	var words bytes.Buffer
	var code exitCode
	if err := run([]string{"index", "some", "words", "--repo", repo, "--home", t.TempDir()}, &words); !errors.As(err, &code) || code != 2 {
		t.Errorf("index with words: err = %v, want exit 2\n%s", err, words.String())
	}
	// The verb's own flags still parse, in any order around positionals.
	home := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"list", "--repo", repo, "--home", home}, &out); err != nil {
		t.Fatalf("list with only global flags: %v\n%s", err, out.String())
	}
}

// `--` ends the flags for the flag package and for reorder alike: what follows
// is positional, so a verb-scoped flag after it is never parsed (security
// review 2026-10-01, f2), and a verb that takes no positional arguments
// refuses it instead of ignoring it.
func TestDoubleDashKeepsFollowingArgumentsPositional(t *testing.T) {
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	fs.String("from-zip", "", "")
	fs.String("home", "", "") // the global flags sit on every verb's FlagSet in run()
	got := reorder(fs, []string{"--home", "h", "--", "--from-zip", "z"})
	want := []string{"--home", "h", "--", "--from-zip", "z"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("reorder dropped the -- marker: got %q", got)
	}
	// A positional before the marker: the marker still moves ahead of every
	// positional, so Args() is exactly the words (`search foo -- bar` searches
	// "foo bar", not "foo -- bar").
	got = reorder(fs, []string{"foo", "--", "bar", "--from-zip"})
	want = []string{"--", "foo", "bar", "--from-zip"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("a positional before -- left the marker in place: got %q", got)
	}
}

// A verb that takes no positional arguments refuses a stray one with a usage
// error: `list - --check` and `version extra` exit 2 instead of running with
// the argument silently ignored (security review 2026-10-01, f3).
func TestVerbRefusesStrayArguments(t *testing.T) {
	isolate(t) // no PATH, a scratch cwd: a regression here must never reach the real home or run git
	t.Setenv("HOME", t.TempDir())
	t.Setenv(harness.ChildEnv, "1")
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"version", "extra"},
		{"list", "-", "--check"},
		{"update", "--", "--from-zip", "nowhere.zip"},
	} {
		var out bytes.Buffer
		err := run(append([]string{args[0], "--repo", repo, "--home", t.TempDir()}, args[1:]...), &out)
		var code exitCode
		if !errors.As(err, &code) || code != 2 {
			t.Errorf("%v: want exit 2 for a stray argument, got %v (output %q)", args, err, out.String())
		}
	}
}

// `update --home -- --from-zip FILE`: the flag package takes `--` as --home's
// value, so a scan of the raw arguments that stops at `--` never sees the zip
// flag; the branch is decided from the parsed FlagSet instead, and the zip path
// refuses the missing file rather than running a normal update.
func TestUpdateFromZipDecidedByParsedFlags(t *testing.T) {
	isolate(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv(harness.ChildEnv, "1")
	var out bytes.Buffer
	err := run([]string{"update", "--home", "--", "--from-zip", "nowhere.zip"}, &out)
	if err == nil {
		t.Fatalf("a missing zip was accepted (output %q)", out.String())
	}
	if !strings.Contains(err.Error(), "nowhere.zip") && !strings.Contains(out.String(), "nowhere.zip") {
		t.Fatalf("the zip branch did not run: err %v, output %q", err, out.String())
	}
}

// TestOneRepositoryName: the clone URL a reader is told to use names the
// repository a release binary updates from (harness.ReleaseRepo), never the
// name before the rename (code review of v2.7.0..881389a, 2026-10-25).
func TestOneRepositoryName(t *testing.T) {
	_, name, _ := strings.Cut(harness.ReleaseRepo, "/")
	want := "git@github.com:" + name + ".git"
	for _, rel := range []string{"README.md", "DEPLOY-remote", "cmd/fyr/verb_install.go", "internal/harness/repo_git.go"} {
		var data []byte
		if rel == "DEPLOY-remote" {
			// The deploy guide is a record since the trail's migration.
			data = []byte(trailRecord(t, rel))
		} else {
			var err error
			if data, err = os.ReadFile(filepath.Join("..", "..", rel)); err != nil {
				t.Fatal(err)
			}
		}
		if strings.Contains(string(data), "kesokaj/styrman-harness") {
			t.Errorf("%s names kesokaj/styrman-harness, the repository before the rename", rel)
		}
		if !strings.Contains(string(data), want) {
			t.Errorf("%s does not give the clone URL %s", rel, want)
		}
	}
}

func TestSearchGraphModesCLI(t *testing.T) {
	t.Setenv(harness.ChildEnv, "1")
	repo, _ := gitFixture(t)
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".agents/scratch/*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "svc.go"), []byte("package main\n\n// Reconcile runs sync (BUG-007).\nfunc Reconcile() error {\n\treturn Helper()\n}\n\nfunc Helper() error {\n\treturn nil\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Chdir(repo)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"search", "--slice", "Reconcile", "--budget", "1800", "--home", home}, "[Hop 0]"},
		{[]string{"search", "--skeleton", "svc.go", "--home", home}, "4-6  func Reconcile() error"},
		{[]string{"search", "--path", "Reconcile", "--to", "Helper", "--home", home}, "Reconcile -> Helper"},
		{[]string{"search", "--path", "Reconcile", "Helper", "--home", home}, "Reconcile -> Helper"},
		{[]string{"search", "--affected", "Helper", "--home", home}, "direct_callers:"},
		{[]string{"search", "--map", "--home", home}, "god_nodes"},
		{[]string{"search", "--vet", "Fix panic in Reconcile in svc.go", "--home", home}, "[FYR_TURN_CONTRACT]"},
	} {
		var out bytes.Buffer
		if err := run(tc.args, &out); err != nil {
			t.Fatalf("run(%v): %v\n%s", tc.args, err, out.String())
		}
		if !strings.Contains(out.String(), tc.want) {
			t.Errorf("run(%v) missing %q in:\n%s", tc.args, tc.want, out.String())
		}
	}

	origStdin := stdin
	t.Cleanup(func() { stdin = origStdin })
	stdin = strings.NewReader(`{"conversationId":"cli-hook","workspacePaths":["` + filepath.ToSlash(repo) + `"],"invocationNum":1,"lastUserInput":"Check Reconcile behavior"}`)
	var hookOut bytes.Buffer
	if err := run([]string{"search", "--hook", "pre-invocation", "--home", home}, &hookOut); err != nil {
		t.Fatalf("search --hook pre-invocation: %v\n%s", err, hookOut.String())
	}
	if !strings.Contains(hookOut.String(), `"injectSteps"`) || !strings.Contains(hookOut.String(), "[Hop 0]") {
		t.Errorf("search --hook pre-invocation output missing capsule:\n%s", hookOut.String())
	}
}

func TestClassifyTurnRecordSkipsUnrelatedADRs(t *testing.T) {
	recs := []brain.Record{
		{ID: "ADR-001", Kind: "adr", Status: "accepted", Title: "Unrelated one", Body: "We never use XML soap envelopes."},
		{ID: "ADR-002", Kind: "adr", Status: "accepted", Title: "Unrelated two", Body: "Rejected: soap envelopes."},
		{ID: "ADR-003", Kind: "adr", Status: "accepted", Title: "Unrelated three", Body: "Never commit binaries."},
		{ID: "ADR-004", Kind: "adr", Status: "accepted", Title: "Cache architecture", Body: "Rejected: memcached cluster."},
	}
	prompt := "Build a new memcached cluster client"
	terms := extractTurnQueryTerms(prompt)
	var turnCtx search.TurnBrainContext
	for _, r := range recs {
		classifyTurnRecord(&turnCtx, "", prompt, nil, terms, r)
	}
	if len(turnCtx.ADRs) != 1 || turnCtx.ADRs[0].ID != "ADR-004" {
		t.Fatalf("got ADRs=%+v, want only [ADR-004]", turnCtx.ADRs)
	}
	if len(turnCtx.Conflicts) != 1 || !strings.Contains(turnCtx.Conflicts[0], "ADR-004") {
		t.Fatalf("got Conflicts=%v, want ADR-004 conflict", turnCtx.Conflicts)
	}
	// An unrelated prompt should match 0 ADRs so constraints anchor is not falsely satisfied.
	var emptyCtx search.TurnBrainContext
	unrelated := "Build a new dashboard widget"
	uTerms := extractTurnQueryTerms(unrelated)
	for _, r := range recs {
		classifyTurnRecord(&emptyCtx, "", unrelated, nil, uTerms, r)
	}
	if len(emptyCtx.ADRs) != 0 {
		t.Fatalf("expected 0 matched ADRs for unrelated prompt, got %+v", emptyCtx.ADRs)
	}
}

func TestProjectDirFlagAcrossVerbs(t *testing.T) {
	t.Setenv(harness.ChildEnv, "1")
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	proj := recordRepo(t)
	if err := os.WriteFile(filepath.Join(proj, "AGENTS.md"), []byte("# AGENTS.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "PRD.md"), []byte("# PRD.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "svc.go"), []byte("package main\nfunc RunSvc() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	t.Chdir(outside)
	home := t.TempDir()

	out, err := recordCmd(t, "Title: Scoped bug\n\nSymptom: x.\nRule: y.\n", "record", "add", "bug", "-C", proj, "--home", home)
	if err != nil || out != "BUG-001\n" {
		t.Fatalf("record add -C: %q %v", out, err)
	}
	out, err = recordCmd(t, "", "record", "list", "--dir", proj, "--home", home)
	if err != nil || !strings.Contains(out, "BUG-001") {
		t.Fatalf("record list --dir: %q %v", out, err)
	}
	out, err = recordCmd(t, "Type: project\nName: scoped-mem\nDescription: via -C\n\nMemory body.\n", "memory", "add", "-C", proj, "--home", home)
	if err != nil || out != "saved MEMORY-scoped-mem\n" {
		t.Fatalf("memory add -C: %q %v", out, err)
	}
	out, err = recordCmd(t, "", "memory", "show", "--dir", proj, "--home", home)
	if err != nil || !strings.Contains(out, "scoped-mem") {
		t.Fatalf("memory show --dir: %q %v", out, err)
	}
	out, err = recordCmd(t, "", "resume", "-C", proj, "--home", home)
	if err != nil || !strings.Contains(out, "BUG-001") || !strings.Contains(out, "scoped-mem") {
		t.Fatalf("resume -C: %q %v", out, err)
	}
	out, err = recordCmd(t, "", "git-check", "-C", proj, "--home", home)
	if err != nil || !strings.Contains(out, "## git status") {
		t.Fatalf("git-check -C: %q %v", out, err)
	}
	out, err = recordCmd(t, "", "index", "-C", proj, "--home", home)
	if err != nil || !strings.Contains(out, "files") {
		t.Fatalf("index -C: %q %v", out, err)
	}
	out, err = recordCmd(t, "", "search", "RunSvc", "--dir", proj, "--json", "--home", home)
	if err != nil || !strings.Contains(out, `"records"`) || !strings.Contains(out, "svc.go") {
		t.Fatalf("search --dir --json: %q %v", out, err)
	}
	out, err = recordCmd(t, "", "check", "-C", proj, "--home", home)
	if err != nil || !strings.Contains(out, "check: 0 findings") {
		t.Fatalf("check -C: %q %v", out, err)
	}
	out, err = recordCmd(t, "", "eval", "--list", "-C", repoRoot, "--home", home)
	if err != nil || !strings.Contains(out, "v1") {
		t.Fatalf("eval --list -C: %q %v", out, err)
	}
	dreamAgy(t)
	out, err = recordCmd(t, "", "dream", "-C", proj, "--dry-run", "--home", home)
	if err != nil || !strings.Contains(out, "[dry-run]") {
		t.Fatalf("dream -C --dry-run: %q %v", out, err)
	}
}

func TestStatusJSON(t *testing.T) {
	t.Setenv(harness.ChildEnv, "1")
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	var out bytes.Buffer
	err = run([]string{"status", "--json", "--repo", repo, "--home", home}, &out)
	if exitOf(err) != 1 {
		t.Fatalf("status --json uninstalled: want exit 1, got %v", err)
	}
	var sum statusSummary
	if jerr := json.Unmarshal(out.Bytes(), &sum); jerr != nil {
		t.Fatalf("unmarshal status --json: %v\n%s", jerr, out.String())
	}
	if !sum.Drifted || !strings.Contains(sum.Installed, "nothing") {
		t.Errorf("unexpected statusSummary: %+v", sum)
	}
}

func TestV910CLIErgonomicsAndCloseStdin(t *testing.T) {
	t.Run("leading_global_flags", testV910LeadingGlobalFlags)
	t.Run("auto_log_and_memory_show_limit", testV910AutoLogAndMemoryShowLimit)
	t.Run("models_explain_flag_aware", testV910ModelsExplainFlagAware)
	t.Run("report_auto_ingest_and_no_ingest", testV910ReportAutoIngest)
	t.Run("record_sync_stale_hint", testV910RecordSyncStaleHint)
	t.Run("local_bin_agy_lookup", testV910LocalBinAgyLookup)
}

func testV910LeadingGlobalFlags(t *testing.T) {
	t.Setenv(harness.ChildEnv, "1")
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	proj := recordRepo(t)
	home := t.TempDir()
	out, err := recordCmd(t, "Title: Lead flag bug\n\nBody.\n", "-C", proj, "--home", home, "record", "add", "bug")
	if err != nil || out != "BUG-001\n" {
		t.Fatalf("leading -C --home record add: %q %v", out, err)
	}
	for _, args := range [][]string{
		{"--dir=" + proj, "--home=" + home, "record", "list"},
		{"-dir", proj, "-home", home, "record", "list"},
		{"-C=" + proj, "--home", home, "record", "list"},
	} {
		got, rerr := recordCmd(t, "", args...)
		if rerr != nil || !strings.Contains(got, "BUG-001") {
			t.Errorf("%v: got=%q err=%v", args, got, rerr)
		}
	}
	var buf bytes.Buffer
	if err := run([]string{"--repo", repoRoot, "--home", home, "list"}, &buf); err != nil || !strings.Contains(buf.String(), "skills") {
		t.Errorf("leading --repo --home list: err=%v out=%q", err, buf.String())
	}
	for _, hArgs := range [][]string{
		{"--home", home, "help", "record"},
		{"-C", proj, "-h", "record"},
		{"--dir=" + proj, "--help", "record"},
		{"--home", home, "-h"},
	} {
		buf.Reset()
		if err := run(hArgs, &buf); err != nil {
			t.Errorf("%v: err=%v out=%q", hArgs, err, buf.String())
		}
	}
	buf.Reset()
	if err := run(nil, &buf); err != nil || buf.String() != shortPage {
		t.Errorf("empty args: err=%v", err)
	}
	for _, bad := range [][]string{
		{"-C"},
		{"--dir"},
		{"--home"},
		{"--repo"},
		{"--home="},
		{"-C", "--home", home, "version"},
		{"--home", home},
		{"--home", home, "no-such-verb"},
	} {
		buf.Reset()
		if err := run(bad, &buf); exitOf(err) != 2 {
			t.Errorf("%v: exit=%d want 2 (out=%q)", bad, exitOf(err), buf.String())
		}
	}
}

func testV910AutoLogAndMemoryShowLimit(t *testing.T) {
	home := t.TempDir()
	logPath := filepath.Join(home, harness.StateDir, "auto", "auto.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("l1\nl2\nl3\nl4\nl5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"auto", "log", "--limit", "2", "--home", home}, &out); err != nil {
		t.Fatalf("auto log --limit 2: %v (%q)", err, out.String())
	}
	if out.String() != "l4\nl5\n" {
		t.Errorf("auto log --limit 2 = %q, want l4\\nl5\\n", out.String())
	}
	for _, bad := range [][]string{
		{"auto", "log", "--limit", "0"},
		{"auto", "log", "--limit", "-1"},
		{"auto", "log", "--limit", "2", "--all"},
		{"auto", "log", "--limit", "2", "--lines", "2"},
		{"auto", "status", "--limit", "2"},
		{"auto", "run", "--dry-run", "--limit", "2"},
	} {
		out.Reset()
		if err := run(append(bad, "--home", home), &out); exitOf(err) != 2 {
			t.Errorf("%v: exit=%d want 2 (out=%q)", bad, exitOf(err), out.String())
		}
	}

	proj := recordRepo(t)
	for _, n := range []string{"m-one", "m-two", "m-three"} {
		if got, err := recordCmd(t, "body "+n+"\n", "memory", "add", "--type", "semantic", "--name", n, "--description", "desc "+n, "-C", proj, "--home", home); err != nil {
			t.Fatalf("memory add %s: %v %q", n, err, got)
		}
	}
	gotText, err := recordCmd(t, "", "memory", "show", "--limit", "2", "-C", proj, "--home", home)
	if err != nil || strings.Count(strings.TrimSpace(gotText), "\n") != 1 {
		t.Errorf("memory show --limit 2: err=%v out=%q", err, gotText)
	}
	gotJSON, err := recordCmd(t, "", "memory", "show", "--limit", "2", "--json", "-C", proj, "--home", home)
	if err != nil {
		t.Fatalf("memory show --limit 2 --json: %v %q", err, gotJSON)
	}
	var memItems []map[string]any
	if err := json.Unmarshal([]byte(gotJSON), &memItems); err != nil || len(memItems) != 2 {
		t.Errorf("memory show --limit 2 --json: len=%d err=%v out=%q", len(memItems), err, gotJSON)
	}
	for _, bad := range [][]string{
		{"memory", "add", "--type", "semantic", "--name", "x", "--description", "d", "--limit", "1"},
		{"memory", "show", "--limit", "-1"},
	} {
		out.Reset()
		if err := run(append(bad, "-C", proj, "--home", home), &out); exitOf(err) != 2 {
			t.Errorf("%v: exit=%d want 2 (out=%q)", bad, exitOf(err), out.String())
		}
	}
}

func testV910ModelsExplainFlagAware(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	catDir := filepath.Join(home, harness.StateDir)
	if err := os.MkdirAll(catDir, 0o700); err != nil {
		t.Fatal(err)
	}
	catJSON := `{"updated_at":"2026-11-22T10:00:00Z","entries":[` +
		`{"id":"gemini-4-carbon-high","family":"google","subfamily":"carbon","tier":"pro","status":"active"},` +
		`{"id":"claude-opus-5-5-high","family":"anthropic","subfamily":"opus","tier":"pro","status":"active"}]}`
	if err := os.WriteFile(filepath.Join(catDir, "models.json"), []byte(catJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	outStr, errStr, code := runCLIWithErr("models", "--explain", "--json", "--home", home)
	if code != 0 || errStr != "" {
		t.Fatalf("models --explain --json: code=%d out=%q err=%q", code, outStr, errStr)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(outStr), &items); err != nil || len(items) != 2 {
		t.Fatalf("models --explain --json decode: len=%d err=%v out=%q", len(items), err, outStr)
	}
	outStr, errStr, code = runCLIWithErr("models", "--home", home, "--explain")
	if code != 0 || !strings.Contains(outStr, "ID: gemini-4-carbon-high") || !strings.Contains(outStr, "ID: claude-opus-5-5-high") {
		t.Fatalf("bare models --explain: code=%d out=%q err=%q", code, outStr, errStr)
	}
	for _, bad := range [][]string{
		{"models", "--explain", "--pick", "builder", "--home", home},
		{"models", "--explain", "--bench", "--home", home},
	} {
		if _, _, c := runCLIWithErr(bad...); c != 2 {
			t.Errorf("%v: exit=%d want 2", bad, c)
		}
	}
}

func testV910ReportAutoIngest(t *testing.T) {
	freshHome := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	proj := recordRepo(t)
	agyHomeIn(t, freshHome, "33333333-3333-3333-3333-333333333333", proj)

	// --no-ingest skips auto-ingest and fails with exit 1 "no metrics store".
	outStr, _, code := runCLIWithErr("report", "--no-ingest", "--md", "--dir", proj, "--home", freshHome)
	if code != 1 || !strings.Contains(outStr, "no metrics store") {
		t.Fatalf("report --no-ingest on fresh home: code=%d out=%q", code, outStr)
	}
	if _, err := os.Stat(filepath.Join(freshHome, harness.StateDir, "metrics.db")); !os.IsNotExist(err) {
		t.Fatalf("report --no-ingest created metrics.db")
	}

	// Default report auto-ingests incrementally and renders without manual prior ingest.
	outStr, errStr, code := runCLIWithErr("report", "--md", "--since", "2026-10-01", "--dir", proj, "--home", freshHome)
	if code != 0 || !strings.Contains(outStr, "| Sessions | 1 |") {
		t.Fatalf("report auto-ingest: code=%d out=%q err=%q", code, outStr, errStr)
	}
	if _, _, c := runCLIWithErr("metrics", "show", "--no-ingest", "--home", freshHome); c != 2 {
		t.Errorf("metrics show --no-ingest: exit=%d want 2", c)
	}
}

func testV910RecordSyncStaleHint(t *testing.T) {
	proj := recordRepo(t)
	home := t.TempDir()
	if _, err := recordCmd(t, "body 1\n", "record", "add", "bug", "--title", "bug one", "-C", proj, "--home", home); err != nil {
		t.Fatal(err)
	}
	if _, err := recordCmd(t, "", "record", "export", "-C", proj, "--home", home); err != nil {
		t.Fatal(err)
	}
	// Matching counts: no stderr hint.
	_, errStr, code := runCLIWithErr("record", "sync", "-C", proj, "--home", home)
	if code != 0 || errStr != "" {
		t.Fatalf("matching sync: code=%d stderr=%q", code, errStr)
	}
	// Add another record in brain.db without exporting: trail.jsonl is now stale.
	if _, err := recordCmd(t, "body 2\n", "record", "add", "bug", "--title", "bug two", "-C", proj, "--home", home); err != nil {
		t.Fatal(err)
	}
	outStr, errStr, code := runCLIWithErr("record", "sync", "-C", proj, "--home", home)
	if code != 0 || outStr != "sync: 1 records loaded from .agents/trail.jsonl\n" {
		t.Fatalf("stale sync: code=%d out=%q", code, outStr)
	}
	if !strings.Contains(errStr, "fyr record export") {
		t.Errorf("stale sync missing export hint on stderr: %q", errStr)
	}
}

func testV910LocalBinAgyLookup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	agy := "#!/bin/sh\nif [ \"$1\" = \"models\" ]; then printf 'gemini-4-carbon-high\\tGemini 4 Carbon (High)\\n'; exit 0; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(localBin, "agy"), []byte(agy), 0o755); err != nil {
		t.Fatal(err)
	}
	outStr, errStr, code := runCLIWithErr("dream", "--all", "--dry-run", "--home", home)
	if code != 0 {
		t.Fatalf("bare-PATH dream --all --dry-run with ~/.local/bin/agy: code=%d out=%q err=%q", code, outStr, errStr)
	}
	outStr, errStr, code = runCLIWithErr("models", "--home", home)
	if code != 0 || !strings.Contains(outStr, "gemini-4-carbon-high") {
		t.Fatalf("bare-PATH models with ~/.local/bin/agy: code=%d out=%q err=%q", code, outStr, errStr)
	}
}
