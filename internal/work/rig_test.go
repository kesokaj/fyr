package work

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/sandbox"
)

// fakeAgy is the worker's CLI: it records what it was given, then does what
// the control files in @CTL@ say, as a session would, through git and the
// sandbox's fyr wrapper (D41). Its default is a good worker: one commit in
// scope, then a handoff record committed.
const fakeAgy = `#!/bin/bash
CTL=@CTL@
on() { [ -e "$CTL/$1" ]; }
val() { cat "$CTL/$1"; }
# The model check (refusal 21, D124): before $CTL/args and any git command.
if [ "$*" = models ]; then
  echo call >> "$CTL/models-calls"; pwd > "$CTL/models-cwd"
  # The environment agy was given: bash itself corrects a stale $PWD.
  [ -r /proc/$$/environ ] && tr '\0' '\n' < /proc/$$/environ | grep -E '^(OLD)?PWD=' > "$CTL/models-pwd"
  on models-helper && { sleep 30 >/dev/null 2>&1 & echo $! > "$CTL/models-helper-pid"; }
  on models-bigerr && yes e | head -c 200000 >&2
  on models-sleep && sleep "$(val models-sleep)"
  on models-big && { yes aaaaaaaa; exit 1; }  # forever, until its pipe closes
  on models-exit && { printf 'spinner\n%s\n' "$(val models-err)" >&2; exit "$(val models-exit)"; }
  if on models-list; then cat "$CTL/models-list"; else printf 'gemini-x\tGemini X\nm-a\tModel A\nm-b\tModel B\n'; fi
  exit 0
fi
printf '%s\n' "$@" > "$CTL/args"; env > "$CTL/env"; pwd > "$CTL/cwd"
git rev-parse --abbrev-ref HEAD > "$CTL/branch"; git remote > "$CTL/remotes"
box=$(dirname "$HOME"); me=$(basename "$box")
printf '%s\n' "$@" > "$CTL/args-$me"
if on peers; then
  ls "$CTL" | grep -c '^running-' > "$CTL/others-$me"; touch "$CTL/running-$me" "$CTL/up-$me"
  # up- files stay: neither worker leaves this loop before both are up, so
  # the one up first is still here when the other comes up. apart counts
  # the running- files instead, for workers that must never overlap.
  pat=up-; on apart && pat=running-
  on together && for i in $(seq 1 "$(val together)0"); do [ "$(ls "$CTL" | grep -c "^$pat")" -ge 2 ] && { touch "$CTL/met-$me"; break; }; sleep 0.1; done
  cp "$(dirname "$box")/pid" "$CTL/pid-$me"
fi
on lead && echo stray > "$(val lead)/stray.txt"
on lead-reset && { git -C "$(val lead-reset)" commit -q --allow-empty -m "record D5 for p3"; git -C "$(val lead-reset)" reset -q --hard HEAD~1; }
on realconfig && echo x >> "$(val realconfig)"
on store && echo x >> "$(val store)"
on leak && printf 'GH_TOKEN=%s\n' "$(val leak)" > "$HOME/.gemini/term.log"
if ! on nocommit; then
  f=src/a.txt; on outside && f=outside.txt; on "file-$me" && f=$(val "file-$me")
  mkdir -p "$(dirname "$f")"
  echo work >> "$f"; git add "$f"; git commit -qm "work on $f"
fi
on edit-record && fyr record edit "$(val edit-record)" --title "Edited by the worker" >/dev/null
on change && fyr record add change --title "Changed src/a.txt" <<<"the change" >/dev/null
on change2 && fyr record add change --title "A second change" <<<"another" >/dev/null
on nohandoff || fyr record add handoff --title "Handoff one" < "$CTL/handoff" >/dev/null
on twohandoffs && fyr record add handoff --title "Handoff two" < "$CTL/handoff" >/dev/null
on add-kind && { fyr record add "$(val add-kind)" --title "The worker's own" <<<"its body" >/dev/null 2>"$CTL/add-kind-err" || touch "$CTL/add-kind-failed"; }
on noexport || fyr record export >/dev/null 2>&1 || true
rawid=RAW-2026-10-30-x; on raw-id && rawid=$(val raw-id)
rawdate=$(date +%F); on raw-date && rawdate=$(val raw-date)
rawupd=$(date -u +%Y-%m-%dT%H:%M:%SZ); on raw-updated && rawupd=$(val raw-updated)
on raw-kind && printf '{"date":"%s","id":"%s","kind":"%s","status":"-","title":"raw","updated":"%s","body":"b"}\n' "$rawdate" "$rawid" "$(val raw-kind)" "$rawupd" >> .agents/trail.jsonl
if [ -n "$(git status --porcelain -- .agents/trail.jsonl)" ]; then git add .agents/trail.jsonl; git commit -qm records; fi
on rewrite && git reset -q --hard "$(git commit-tree 'HEAD^{tree}' -m orphan)"
if on drafts; then
  d=.agents/scratch/agents; mkdir -p "$d/sub"; echo adr > "$d/adr-draft.md"; echo bug > "$d/bug-draft.md"
  ln -s "$CTL/handoff" "$d/link.md"; head -c 1048577 /dev/zero > "$d/big.md"; mkfifo "$d/pipe.md"; ln "$CTL/handoff" "$d/hard.md"
fi
on symlink && { ln -s "$(val symlink)" src/link; git add src/link; git commit -qm link; }
on gitlink && { git update-index --add --cacheinfo "160000,$(git rev-parse HEAD),src/link"; git commit -qm sub; }
if on add-remove; then echo x > outside.txt; git add outside.txt; git commit -qm add; git rm -q outside.txt; git commit -qm remove; fi
on graft && { mkdir -p "$(val leadpath)/.git/info"; echo "$(git rev-parse HEAD) $(git rev-parse HEAD~4)" > "$(val leadpath)/.git/info/grafts"; }
on hook && { printf '#!/bin/sh\ntouch %s\n' "$(val hook)" > "$(val leadpath)/.git/hooks/reference-transaction"; chmod +x "$(val leadpath)/.git/hooks/reference-transaction"; }
if on plant; then
  box=$(dirname "$HOME"); stamp=$(basename "$(dirname "$box")"); name=$(basename "$box")
  case "$(val plant)" in
  name) mkdir -p "$(val leadpath)/.agents/scratch/work/$stamp"; ln -s "$(val outdir)" "$(val leadpath)/.agents/scratch/work/$stamp/$name" ;;
  work) mkdir -p "$(val leadpath)/.agents/scratch"; ln -s "$(val outdir)" "$(val leadpath)/.agents/scratch/work" ;;
  stamp) mkdir -p "$(val leadpath)/.agents/scratch/work/$stamp" ;;
  namedir) mkdir -p "$(val leadpath)/.agents/scratch/work/$stamp/$name" ;;
  esac
fi
if on draft-names; then
  d=.agents/scratch/agents; mkdir -p "$d"
  while IFS= read -r n; do echo x > "$d/$n"; done < "$CTL/draft-names"
fi
on many-drafts &&{ mkdir -p .agents/scratch/agents; for i in $(seq -w 1 51); do echo "$i" > ".agents/scratch/agents/d$i.md"; done; }
on alternates && val alternates > "$(git rev-parse --git-dir)/objects/info/alternates"
on delete-branch && { git checkout -q --detach; git branch -q -D "$(cat "$CTL/branch")"; }
on hang && { touch "$CTL/started" "$CTL/started-$me"; exec sleep 300; }
if on toplevel-repo; then cp -r .git/objects .git/refs .git/HEAD .; rm .git/HEAD; fi
if on nested-repo; then cp -r .git ../nested; rm .git/HEAD; mv ../nested .git/.git; fi
on "slow-$me" && sleep "$(val "slow-$me")"
on peers && rm -f "$CTL/running-$me"
echo "agy says ok"
exit "$(cat "$CTL/exit" 2>/dev/null || echo 0)"
`

// fakeSelf is the fyr that installs: the real one, copied to where the
// wrapper execs it.
const fakeSelf = `#!/bin/bash
[ -e @CTL@/install-fail ] && { echo "install broke" >&2; exit 1; }
[ -e @CTL@/token-block ] && { mkdir -p "$3/.gemini/antigravity-cli"; echo x > "$3/.gemini/antigravity-cli/antigravity-oauth-token"; }
case "$1" in
install) mkdir -p "$3/.local/bin"; cp @FYR@ "$3/.local/bin/fyr" ;;
esac
`

const handoffBody = "# Handoff one\n\n## Context\n\nctx\n\n## Open / In Progress\n\n- open item\n\n" +
	"## Next Steps\n\n1. next step\n\n## Kickoff\n\n```text\nProject: x\n## not a heading inside the fence\n```\n"

// rig is a worker's world: a lead checkout with a trail and a gitignored
// scratch, a real home with a token, fyr's home, and the fakes on PATH, all
// in a temporary directory outside every checkout (D11).
type rig struct {
	t                                     *testing.T
	tmp, lead, real, home, bin, ctl, kick string
	guide                                 string // the lead's one record at the base
	out                                   bytes.Buffer
}

func newRig(t *testing.T) *rig {
	t.Helper()
	fyr := realFyr(t)
	tmp := outsideTemp(t)
	r := &rig{t: t, tmp: tmp, lead: filepath.Join(tmp, "lead"), real: filepath.Join(tmp, "real"),
		home: filepath.Join(tmp, "home"), bin: filepath.Join(tmp, "bin"), ctl: filepath.Join(tmp, "ctl"), kick: filepath.Join(tmp, "kick")}
	for _, d := range []string{r.bin, r.ctl, r.kick, r.home} {
		mkdir(t, d)
	}
	r.git("init", "-q", r.lead)
	r.git("-C", r.lead, "config", "user.name", "Lead Person")
	r.git("-C", r.lead, "config", "user.email", "lead@localhost")
	put(t, filepath.Join(r.lead, ".gitignore"), ".agents/scratch/\n")
	put(t, filepath.Join(r.lead, "README.md"), "demo\n")
	put(t, filepath.Join(r.lead, "src", "a.txt"), "a\n")
	put(t, filepath.Join(r.lead, "pkg", "b.go"), "b\n")
	add := exec.Command(fyr, "record", "add", "guide", "--title", "Old guide", "--home", r.home)
	add.Dir, add.Stdin = r.lead, strings.NewReader("the guide\n")
	var stderr bytes.Buffer
	add.Stderr = &stderr
	id, err := add.Output()
	if err != nil {
		t.Fatalf("record add: %v\n%s%s", err, stderr.String(), id)
	}
	r.guide = strings.TrimSpace(string(id))
	exp := exec.Command(fyr, "record", "export", "--home", r.home)
	exp.Dir = r.lead
	if out, err := exp.CombinedOutput(); err != nil {
		t.Fatalf("record export: %v\n%s", err, out)
	}
	r.git("-C", r.lead, "add", "-A")
	r.git("-C", r.lead, "commit", "-qm", "base")
	put(t, filepath.Join(r.real, sandbox.TokenRel), "token")
	put(t, filepath.Join(r.real, ".gemini", "config", "rules", "x.md"), "rule\n")
	put(t, filepath.Join(r.bin, "agy"), strings.ReplaceAll(fakeAgy, "@CTL@", sandbox.ShellQuote(r.ctl)))
	self := strings.ReplaceAll(fakeSelf, "@FYR@", sandbox.ShellQuote(fyr))
	put(t, filepath.Join(r.bin, "self"), strings.ReplaceAll(self, "@CTL@", sandbox.ShellQuote(r.ctl)))
	for _, f := range []string{"agy", "self"} {
		os.Chmod(filepath.Join(r.bin, f), 0o755)
	}
	put(t, filepath.Join(r.ctl, "handoff"), handoffBody)
	t.Setenv("HOME", r.real)
	t.Setenv("PATH", r.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// A known zone for refusal 22: a developer's TZ=:/etc/localtime would
	// refuse every run (workers D145, G3); UTC loads without tzdata.
	t.Setenv("TZ", "UTC")
	return r
}

func (r *rig) opts() Options {
	return Options{Root: r.lead, Home: r.home, RealHome: r.real, Self: filepath.Join(r.bin, "self"),
		Timeout: 10 * time.Minute, Out: &r.out, StopGrace: 100 * time.Millisecond,
		Now: func() time.Time { return time.Date(2026, 10, 29, 18, 0, 0, 0, time.Local) }}
}

// on sets a control of the fake agy; val "" makes it a flag.
func (r *rig) on(name, val string) { put(r.t, filepath.Join(r.ctl, name), val) }

// kickoff writes a Kickoff file named name.md with the Files: line files.
func (r *rig) kickoff(name, files string) string {
	p := filepath.Join(r.kick, name+".md")
	put(r.t, p, "Project: "+r.lead+". Do the phase.\nFiles: "+files+"\nStanding approvals: commit.\n")
	return p
}

// run is Run on a Kickoff, failing the test on a refusal.
func (r *rig) run(file string) Result {
	r.t.Helper()
	res, err := first(Run(context.Background(), r.opts(), file))
	if err != nil {
		r.t.Fatalf("refused: %v", err)
	}
	return res
}

func (r *rig) git(args ...string) string {
	r.t.Helper()
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *rig) read(name string) string {
	b, _ := os.ReadFile(filepath.Join(r.ctl, name))
	return string(b)
}

func outsideTemp(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	if res, err := filepath.EvalSymlinks(tmp); err == nil {
		tmp = res
	}
	if out, err := exec.Command("git", "-C", tmp, "rev-parse", "--git-dir").CombinedOutput(); err == nil {
		t.Skipf("the test's temporary directory is inside a git checkout (%s): a worker's home must be outside every checkout (D11)", strings.TrimSpace(string(out)))
	}
	return tmp
}

func mkdir(t *testing.T, d string) {
	t.Helper()
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
}

func put(t *testing.T, p, s string) {
	t.Helper()
	mkdir(t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func wantReasons(t *testing.T, res Result, want ...string) {
	t.Helper()
	if strings.Join(res.Reasons, "; ") != strings.Join(want, "; ") {
		t.Errorf("reasons %q, want %q; notes:\n%s", res.Reasons, want, strings.Join(res.Notes, "\n"))
	}
}
