package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// allFlags is a FlagSet holding the global flags and every verb's flags, for
// the reorder tests: the value flags are read from it, as run does per verb.
func allFlags() *flag.FlagSet {
	c := &ctx{fs: flag.NewFlagSet("all", flag.ContinueOnError)}
	c.fs.StringVar(&c.repoFlag, "repo", "", "")
	c.fs.StringVar(&c.homeFlag, "home", "", "")
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
		"dream --project /x --dry-run":           {"--project", "/x", "--dry-run", "dream"},
		"dream --dry-run":                        {"--dry-run", "dream"},
		"search --slice Reconcile --budget 1800": {"--slice", "Reconcile", "--budget", "1800", "search"},
		"search --path EntryPoint LeafTarget":    {"--path", "EntryPoint", "search", "LeafTarget"},
		"search --affected CoreStore --home /x":  {"--affected", "CoreStore", "--home", "/x", "search"},
		"search --hook pre-invocation --home /x": {"--hook", "pre-invocation", "--home", "/x", "search"},
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
	for _, rel := range []string{"README.md", "DEPLOY-remote", "cmd/fyr/verb_install.go", "internal/harness/repo.go"} {
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
