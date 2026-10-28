package main

import (
	"bytes"
	"flag"
	"io"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

// The help pages (plan ledger Phase 4b, D19-D21): the short page, one page
// per verb, and the usage errors that print a verb's synopsis.

// helpRun runs fyr and returns what it wrote on the writer and its exit
// status.
func helpRun(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	err := run(args, &out)
	return out.String(), exitOf(err)
}

// verbKeys is every key of the command table, sorted.
func verbKeys() []string {
	var k []string
	for v := range verbs {
		k = append(k, v)
	}
	sort.Strings(k)
	return k
}

// (a) Every line of every page fits an 80-column terminal.
func TestHelpLinesFit(t *testing.T) {
	texts := map[string]string{"the short page": shortPage}
	for _, v := range verbKeys() {
		texts[v] = verbHelp(v)
	}
	for name, text := range texts {
		for i, line := range strings.Split(text, "\n") {
			if n := utf8.RuneCountInString(line); n > 79 {
				t.Errorf("%s, line %d: %d runes, over 79: %q", name, i+1, n, line)
			}
		}
	}
}

// (b) The short page names every verb but sync, and nothing else; every verb
// has a page, sync install's.
func TestHelpShortPageListsEveryVerb(t *testing.T) {
	var listed []string
	for _, m := range regexp.MustCompile(`(?m)^  ([a-z]+) `).FindAllStringSubmatch(shortPage, -1) {
		listed = append(listed, m[1])
	}
	sort.Strings(listed)
	var want []string
	for _, v := range verbKeys() {
		if v != "sync" {
			want = append(want, v)
		}
	}
	if strings.Join(listed, " ") != strings.Join(want, " ") {
		t.Errorf("the short page lists %v, the command table has %v", listed, want)
	}
	for _, v := range verbKeys() {
		if _, ok := pageOf(v); !ok {
			t.Errorf("%s has no page", v)
		}
	}
	if p, _ := pageOf("sync"); p.synopsis[0] != pages["install"].synopsis[0] {
		t.Error("sync's page is not install's")
	}
}

// (c) A page's Flags: are exactly the verb's FlagSet minus --home and
// --repo; a verb with none prints no Flags: heading.
func TestHelpFlagsAreTheFlagSet(t *testing.T) {
	for _, v := range verbKeys() {
		var want []string
		newCtx(v, verbs[v], io.Discard).fs.VisitAll(func(f *flag.Flag) {
			if f.Name != "home" && f.Name != "repo" {
				want = append(want, f.Name)
			}
		})
		page := verbHelp(v)
		_, list, has := strings.Cut(page, "\nFlags:\n")
		if len(want) == 0 {
			if has {
				t.Errorf("%s takes no flag of its own but its page has Flags:", v)
			}
			continue
		}
		var got []string
		for _, m := range regexp.MustCompile(`(?m)^  --([a-z-]+)`).FindAllStringSubmatch(list, -1) {
			got = append(got, m[1])
		}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("%s's page lists flags %v, its FlagSet has %v", v, got, want)
		}
	}
}

// (d) Every way to ask for a verb's page prints the same bytes, exit 0, with
// nothing of the flag package's own usage.
func TestHelpFormsAgree(t *testing.T) {
	placeholder := regexp.MustCompile(`(?m)^  --\S+ (string|int|int64|uint|float64|value|duration)$`)
	for _, v := range verbKeys() {
		want := verbHelp(v)
		for _, args := range [][]string{{"help", v}, {v, "-h"}, {v, "--help"}, {"-h", v}, {"--help", v}} {
			got, code := helpRun(t, append(args, "--home", t.TempDir())...)
			if code != 0 || got != want {
				t.Errorf("fyr %v: exit %d, output differs from fyr help %s:\n%s", args, code, v, got)
			}
		}
		if strings.Contains(want, "Usage of") || placeholder.MatchString(want) || strings.Contains(want, "`") {
			t.Errorf("%s's page carries the flag package's usage, a type placeholder or a backquote:\n%s", v, want)
		}
	}
}

// (e) help's own arguments: one known verb, or none.
func TestHelpArguments(t *testing.T) {
	home := t.TempDir()
	for name, tc := range map[string]struct {
		args []string
		code int
		want string
	}{
		"bare":            {[]string{}, 0, shortPage},
		"help":            {[]string{"help"}, 0, shortPage},
		"-h":              {[]string{"-h"}, 0, shortPage},
		"--help":          {[]string{"--help"}, 0, shortPage},
		"-help record":    {[]string{"-help", "record"}, 0, verbHelp("record")},
		"help help":       {[]string{"help", "help"}, 0, shortPage},
		"help --home":     {[]string{"help", "--home", home, "record"}, 0, verbHelp("record")},
		"help record -h":  {[]string{"help", "record", "-h"}, 0, verbHelp("record")},
		"help nosuch":     {[]string{"help", "nosuch"}, 2, "fyr help: unknown command \"nosuch\"\n" + helpSynopsis + "\n"},
		"help two":        {[]string{"help", "record", "list"}, 2, "fyr help takes one command: [\"record\" \"list\"]\n" + helpSynopsis + "\n"},
		"-h two":          {[]string{"-h", "record", "list"}, 2, "fyr help takes one command: [\"record\" \"list\"]\n" + helpSynopsis + "\n"},
		"help a bad flag": {[]string{"help", "--nosuch"}, 2, "flag provided but not defined: -nosuch\n" + helpSynopsis + "\n"},
	} {
		t.Run(name, func(t *testing.T) {
			got, code := helpRun(t, tc.args...)
			if code != tc.code || got != tc.want {
				t.Errorf("fyr %v: exit %d, want %d\ngot:\n%s\nwant:\n%s", tc.args, code, tc.code, got, tc.want)
			}
		})
	}
}

// (f) sync is install's alias: not on the short page, named on install's.
func TestHelpSyncIsInstalls(t *testing.T) {
	if strings.Contains(shortPage, "\n  sync ") {
		t.Error("the short page lists sync")
	}
	if !strings.Contains(verbHelp("install"), "sync is an alias of install") {
		t.Error("install's page does not name sync")
	}
	if verbHelp("sync") != verbHelp("install") {
		t.Error("sync's page is not install's")
	}
}

// (g) Each page's shape: the synopsis names its verb first, the description
// is one to ten lines, and the page ends with the global flags line.
func TestHelpPageShape(t *testing.T) {
	if !strings.HasSuffix(shortPage, "\n"+helpLast+"\n") {
		t.Error("the short page does not end with the global flags line")
	}
	for v, p := range pages {
		head := "usage: fyr " + v
		if s := p.synopsis[0]; s != head && !strings.HasPrefix(s, head+" ") {
			t.Errorf("%s's synopsis begins %q", v, s)
		}
		if n := len(p.about); n == 0 || n > 10 {
			t.Errorf("%s's description is %d lines, want 1 to 10", v, n)
		}
		if !strings.HasSuffix(verbHelp(v), "\n\n"+helpLast+"\n") {
			t.Errorf("%s's page does not end with the global flags line", v)
		}
	}
}

// A usage error at each site D21 (2) names: its reason, when the site has
// one, then the verb's synopsis (the first lines of its page, from the same
// data), then the hint; exit 2.
func TestUsageErrSites(t *testing.T) {
	home := t.TempDir()
	for name, tc := range map[string]struct {
		args   []string
		verb   string
		reason string
	}{
		"a flag error":           {[]string{"list", "--nosuch"}, "list", "flag provided but not defined: -nosuch"},
		"a stray argument":       {[]string{"version", "extra"}, "version", `version takes no arguments: ["extra"]`},
		"bare record":            {[]string{"record"}, "record", ""},
		"record drop":            {[]string{"record", "drop"}, "record", `fyr record: unknown subcommand "drop"`},
		"record list x":          {[]string{"record", "list", "x"}, "record", ""},
		"record get --title":     {[]string{"record", "get", "x", "--title", "T"}, "record", "fyr record get does not take --title"},
		"adopt without a name":   {[]string{"adopt", "--repo", "../.."}, "adopt", ""},
		"eval without a row":     {[]string{"eval"}, "eval", ""},
		"memory frob":            {[]string{"memory", "frob"}, "memory", ""},
		"bare metrics":           {[]string{"metrics"}, "metrics", ""},
		"metrics frob":           {[]string{"metrics", "frob"}, "metrics", `fyr metrics: unknown subcommand "frob" (ingest, show, report, export)`},
		"sync with a stray word": {[]string{"sync", "x", "--repo", "../.."}, "sync", `sync takes no arguments: ["x"]`},
		"dream both timers":      {[]string{"dream", "--install-timer", "--remove-timer"}, "dream", "fyr dream: --install-timer or --remove-timer, not both"},
		"dream timer and scope":  {[]string{"dream", "--remove-timer", "--project", "x"}, "dream", "fyr dream: --install-timer and --remove-timer take no --all or --project"},
		"dream all and project":  {[]string{"dream", "--all", "--project", "x"}, "dream", "fyr dream: --all or --project <dir>, not both"},
		"bare dream":             {[]string{"dream"}, "dream", ""},
	} {
		t.Run(name, func(t *testing.T) {
			got, code := helpRun(t, append(tc.args, "--home", home)...)
			p, _ := pageOf(tc.verb)
			synopsis := strings.Join(p.synopsis, "\n") + "\n"
			want := synopsis + "run 'fyr help " + tc.verb + "' for its flags\n"
			if tc.reason != "" {
				want = tc.reason + "\n" + want
			}
			if code != 2 || got != want {
				t.Errorf("fyr %v: exit %d, want 2\ngot:\n%s\nwant:\n%s", tc.args, code, got, want)
			}
			if !strings.HasPrefix(verbHelp(tc.verb), synopsis) {
				t.Errorf("%s's page does not begin with the synopsis its usage error prints", tc.verb)
			}
			if strings.Contains(got, "Usage of") {
				t.Errorf("fyr %v printed the flag package's usage:\n%s", tc.args, got)
			}
		})
	}
}

// An unknown verb names itself and points at fyr help; it never prints the
// short page.
func TestUsageErrUnknownVerb(t *testing.T) {
	got, code := helpRun(t, "nosuch")
	want := "fyr: unknown command \"nosuch\"\nrun 'fyr help' for the commands\n"
	if code != 2 || got != want {
		t.Errorf("fyr nosuch: exit %d\n%s", code, got)
	}
	if strings.Contains(got, "Harness") {
		t.Errorf("fyr nosuch printed the short page:\n%s", got)
	}
}

// The exit-2 sites D21 (2) leaves out already say what is wrong, or are not
// usage errors: none prints the hint.
func TestUsageErrLeftOutSites(t *testing.T) {
	hint := "run 'fyr help"
	got, code := helpRun(t, "search", "--limit", "51", "--home", t.TempDir(), "x")
	if code != 2 || strings.Contains(got, hint) {
		t.Errorf("search --limit 51: exit %d\n%s", code, got)
	}
	t.Run("eval's refusal", func(t *testing.T) {
		t.Chdir(t.TempDir())
		got, code := helpRun(t, "eval", "row", "--home", t.TempDir())
		if code != 2 || !strings.Contains(got, "not a git checkout") || strings.Contains(got, hint) {
			t.Errorf("eval outside a checkout: exit %d\n%s", code, got)
		}
	})
	t.Run("update --check behind", func(t *testing.T) {
		repo, _ := gitFixture(t)
		advanceOrigin(t, repo)
		var out bytes.Buffer
		err := update(repo, t.TempDir(), true, "", &out)
		if exitOf(err) != 2 || strings.Contains(out.String(), hint) {
			t.Errorf("update --check behind: %v\n%s", err, out.String())
		}
	})
}

// search's page lists the --kind values in the order search itself refuses
// an unknown one with: the usage string is a copy, so it is pinned.
func TestHelpSearchKindsMatchSearch(t *testing.T) {
	got, code := helpRun(t, "search", "--kind", "nosuch", "--home", t.TempDir(), "x")
	_, list, ok := strings.Cut(strings.TrimSpace(got), "--kind must be one of: ")
	list, _, _ = strings.Cut(list, ";")
	if code != 2 || !ok {
		t.Fatalf("search --kind nosuch: exit %d\n%s", code, got)
	}
	_, use := flag.UnquoteUsage(newCtx("search", verbs["search"], io.Discard).fs.Lookup("kind"))
	if !strings.HasSuffix(use, ": "+list) {
		t.Errorf("search's --kind usage %q does not end with search's list %q", use, list)
	}
}
