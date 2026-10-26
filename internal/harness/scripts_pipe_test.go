package harness

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestScriptsHaveNoEarlyExitPipe pins that rule on the shell scripts the
// repository tracks that run under pipefail (the skills' scripts), and proves
// the check on both states first.
func TestScriptsHaveNoEarlyExitPipe(t *testing.T) {
	for _, bad := range []string{
		"bad=$(find . -print | head -1)\n",
		"git log --format=%h | grep -q -- \"^$sha\" || exit 2\n",
		"printf '%s\\n' \"$line\" | grep -q $ci -F -- \"$w\" || keep=0\n",
		"if producer | grep -m1 x; then :; fi\n",
		"if producer | grep --quiet x; then :; fi\n",
		"if producer | grep -e x -q; then :; fi\n",
		"n=$(producer | grep --max-count=1 x)\n",
		"f=$(producer | grep -l x)\n",
		"first=$(producer | sed 1q)\n",
		"first=$(producer | sed q)\n",
		"first=$(producer | sed -n '1p;q')\n",
		"first=$(producer | sed '/re/q')\n",
		"first=$(producer | awk '{print; exit}')\n",
		"producer | read first\n",
		"producer |& head -1\n",
		"bad=$(find . -print | /usr/bin/head -1)\n",
		"bad=$(find . -print |\n  head -1)\n",
		"bad=$(find . -print \\\n  | head -1)\n",
		"v=\"$(find . -print | head -1)\"\n",
		"[ -n \"$(producer | grep -q x)\" ] || exit 1\n",
		"first=$(producer | sed -n '1{p;q}')\n",
		"producer | sed '/^#/!q'\n",
		"producer | sed '/x/Iq'\n",
		"producer | sed -n '/x/Mq'\n",
		"producer | sed '\\%x%q'\n",
		"producer | sed '/a/,/b/!q'\n",
		"producer | sed 1Q\n",
		"producer | sed '/x/q5'\n",
		"echo \"n: $(producer | (cat; true) | head -1) done\"\n",
	} {
		if earlyExitPipeProblem(bad) == "" {
			t.Errorf("the check passes %q", bad)
		}
	}
	for _, good := range []string{
		"# never `| head -1`\n",
		"grep x f | head -20 >&2 || true\n",
		"bad=$(find . -print -quit)\n",
		"grep -q x <<< \"$line\"\n",
		"sort f | grep -v x\n",
		"printf x | sed 's/q/z/'\n",
		"sed -n 's/^date: //p' f | sort\n",
		"[ -f x ] || head -1 f\n",
		"grep -E 'write|read|head' f\n",
		"grep -E \"quiet|head -1\" f | sort\n",
		"producer | while read -r x; do :; done\n",
		"d=$(awk '/^date: /{print; exit}' f)\n",
		"producer | awk '{print $1}' | sort\n",
		"echo a \\| head -1\n",
		"x=1 # then | head -1\n",
		"echo \"$(sort f | uniq)\" \"a|head\"\n",
		"sed 's/a/b/' faq | sort\n",
		"producer | sed 's/q/z/;s/x/q/' | sort\n",
	} {
		if got := earlyExitPipeProblem(good); got != "" {
			t.Errorf("the check refuses %q", good)
		}
	}
	// BUG-148's shape: a loop that is a pipeline stage whose body ends in
	// `test && cmd` returns 1 when the last test is false, and pipefail ends
	// the script with no message (TR13). The same loop fed by a redirection
	// does not end the script (bash exempts the && list), so it passes.
	for _, bad := range []string{
		"producer | while read -r x; do\n  [ -d \"$x\" ] && rm -rf -- \"$x\"\ndone\n",
		"producer |\n  while read -r x; do\n    test -d \"$x\" && rm -rf -- \"$x\"\n  done\n",
		"producer | while read -r x; do [ -d \"$x\" ] && rm -rf -- \"$x\"; done\n",
		"producer | sort | for x in a; do\n  [[ -n $x ]] && echo \"$x\"\ndone\n",
		"producer | while read -r x; do\n  case $x in\n    a) continue ;;\n  esac\n  [ -d \"$x\" ] && rm -rf -- \"$x\"  # the last name\ndone\n",
	} {
		if loopTailTestProblem(bad) == "" {
			t.Errorf("the loop-tail check passes %q", bad)
		}
	}
	for _, good := range []string{
		"while read -r x; do\n  [ -d \"$x\" ] && rm -rf -- \"$x\"\ndone < f\n",
		"producer | while read -r x; do\n  if [ -d \"$x\" ]; then rm -rf -- \"$x\"; fi\ndone\n",
		"producer | while read -r x; do\n  [ -z \"$x\" ] && continue\n  echo \"$x\"\ndone\n",
		"producer | while read -r x; do\n  [ -d \"$x\" ] && rm -rf -- \"$x\" || true\ndone\n",
		"producer | while read -r x; do\n  [ -d \"$x\" ] || continue\ndone\n",
		"producer | while read -r x; do\n  for y in a; do\n    [ -d \"$y\" ] && echo\n  done < f\n  echo ok\ndone\n",
	} {
		if got := loopTailTestProblem(good); got != "" {
			t.Errorf("the loop-tail check refuses %q (%s)", good, got)
		}
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	var scripts []string
	for _, pat := range []string{"skills/*/scripts/*.sh"} {
		m, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pat)))
		if err != nil {
			t.Fatal(err)
		}
		scripts = append(scripts, m...)
	}
	if len(scripts) < 3 {
		t.Fatalf("want the three skill scripts, found %v", scripts)
	}
	for _, f := range scripts {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "pipefail") {
			continue
		}
		if l := earlyExitPipeProblem(string(b)); l != "" {
			t.Errorf("%s pipes into a consumer that exits early, without a guard (BUG-155): %s", filepath.Base(f), l)
		}
		if l := loopTailTestProblem(string(b)); l != "" {
			t.Errorf("%s ends a piped loop's body in `test && cmd` (BUG-148): %s", filepath.Base(f), l)
		}
	}
}

// TestCatalogNotesNeverAllowQuiet: the router does not follow an entry's
// `--quiet` (step 4), and a note never relaxes the router. A note that names
// the flag therefore says to leave it out, in one of three fixed phrases;
// "only on read-only commands" or "after approval" is an exception the router
// does not have (security reviews of the v2.1.0 gate: found three times as
// instances, pinned here as a kind).
func TestCatalogNotesNeverAllowQuiet(t *testing.T) {
	// The helper first, on lines that must and must not pass: a carve-out
	// beside an allowed phrase once passed (code review, R3-1).
	for line, bad := range map[string]bool{
		"- `x` — No `--quiet` on any command.":                                      false,
		"- `x` — bind on the bucket, drop `--quiet`, and confirm every IAM change.": false,
		"- `x` — run it without `--quiet`; ask before `services enable`.":           false,
		"- `x` — the operator only said so; drop `--quiet`; ask after the dry run.": false,
		"- `x` — No `--quiet` except after approval.":                               true,
		"- `x` — drop `--quiet` unless the command is read-only.":                   true,
		"- `x` — `--quiet` only on read-only commands.":                             true,
		"- `x` — No `--quiet` once the operator approved the plan.":                 true,
		"- `x` — it runs `gcloud compute ssh --quiet`.":                             true,
		"- `x` — drop `--quiet` if the command changes state.":                      true,
		"- `x` — No `--quiet` when the operator has not approved.":                  true,
		"- `x` — drop `--quiet`, but keep it on read commands.":                     true,
		"- `x` — pass `--project` on each command.":                                 false,
	} {
		if got := quietNoteProblem(line); (got != "") != bad {
			t.Errorf("quietNoteProblem(%q) = %q, want a problem: %v", line, got, bad)
		}
	}
	if os.Getenv("CATALOG_WRITE") == "1" {
		t.Skip("the refresh script's build directory has no notes yet")
	}
	data, err := os.ReadFile(filepath.Join(catalogDir(t), "MANIFEST.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, notes, ok := strings.Cut(string(data), "\n## Notes\n")
	if !ok {
		t.Fatal("MANIFEST.md has no ## Notes section")
	}
	seen := 0
	for _, line := range strings.Split(notes, "\n") {
		if !strings.HasPrefix(line, "- `") || !strings.Contains(line, "--quiet") {
			continue
		}
		seen++
		if problem := quietNoteProblem(line); problem != "" {
			t.Errorf("%s:\n%s", problem, line)
		}
	}
	if seen == 0 {
		t.Error("no note names --quiet: the test checks nothing (was the section renamed?)")
	}
}

// fixtureEnv is the environment for every subprocess the catalog tests
// start, the fixture's git calls and each run of the script: the caller's,
// without any GIT_* variable, plus the test's own additions. An exported
// GIT_DIR or GIT_INDEX_FILE (git sets them inside a hook) would aim the
// fixture's add, commit and reset --hard at another repository (the v2.1.0
// gate's L10).
func fixtureEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	return append(env, extra...)
}

// quietNoteProblem judges one note: every clause that names `--quiet` must
// say to leave it out, in one of three fixed phrases, and must not carve an
// exception beside the phrase. A clause ends at a semicolon or at a full
// stop followed by a space. It returns "" or the reason.
func quietNoteProblem(line string) string {
	for _, clause := range quietClauseEnd.Split(line, -1) {
		if !strings.Contains(clause, "--quiet") {
			continue
		}
		lower := strings.ToLower(clause)
		if !strings.Contains(lower, "no `--quiet`") && !strings.Contains(lower, "drop `--quiet`") && !strings.Contains(lower, "without `--quiet`") {
			return "a note names --quiet without saying to leave it out (no `--quiet`, drop `--quiet` or without `--quiet`)"
		}
		if word := quietCarveOut.FindString(lower); word != "" {
			return "a note carves an exception (\"" + word + "\") in the clause that names --quiet; the router has none"
		}
	}
	return ""
}

var (
	quietClauseEnd = regexp.MustCompile(`;|\. `)
	quietCarveOut  = regexp.MustCompile(`\b(except|unless|only|once|after|if|when|but)\b`)
)

// TestCatalogInstallsClean: the install plan over the real checkout has no
// refusal — a secret-shaped name or a symlink anywhere under skills/ fails it,
// through the installer's own list rather than a second copy in the script.
func TestCatalogInstallsClean(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Plan(DirSource(repo), nil); err != nil {
		t.Fatalf("the checkout does not install: %v", err)
	}
}

// TestRouterDescription: the router is the one catalog line the CLI lists, so
// its description is short (Claude Code's listing budget, TestSkillListingBudget)
// and says what it must not be used for.
func TestRouterDescription(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repo, "skills", "google-catalog", "SKILL.md"))
	if err != nil {
		t.Fatalf("no skills/google-catalog/SKILL.md: %v", err)
	}
	front, _, err := Split(string(data))
	if err != nil {
		t.Fatal(err)
	}
	desc := Parse(front)["description"]
	if n := len(desc); n < 240 || n > 300 {
		t.Errorf("the router's description is %d bytes, want 240 to 300", n)
	}
	if !strings.Contains(desc, "Do NOT use") {
		t.Errorf("the router's description names no skill that wins over it: %q", desc)
	}
}
