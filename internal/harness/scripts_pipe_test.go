package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScriptsHaveNoEarlyExitPipe pins that rule on the shell scripts the
// repository tracks that run under pipefail (the skills' scripts), and proves
// the check on both states first.
func TestScriptsHaveNoEarlyExitPipe(t *testing.T) {
	t.Parallel()
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
	if len(scripts) < 2 {
		t.Fatalf("want the skill scripts, found %v", scripts)
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

// TestCheckoutInstallsClean: the install plan over the real checkout has no
// refusal — a secret-shaped name or a symlink anywhere under skills/ fails it.
func TestCheckoutInstallsClean(t *testing.T) {
	t.Parallel()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Plan(DirSource(repo), nil); err != nil {
		t.Fatalf("the checkout does not install: %v", err)
	}
}
