#!/usr/bin/env bash
# fake-work.sh <kickoff-file>...: stands in for `fyr work` in the eval's task
# rows (GUIDE-eval.md, scenario `tasks`), so a row tests the /work lead's
# plan-less mode without starting real workers inside a trial. fixture.sh
# copies it to .agents/scratch/eval/ in the demo. It refuses what the real verb
# refuses that a lead can get wrong (no Kickoff, a name outside the branch
# alphabet, not one Files: line, a dirty tree, a branch that exists, two
# Kickoffs sharing a path), logs each run it accepts with the HEAD it ran on,
# makes each worker's branch with a canned change chosen by the task's words,
# fails a branch that changed a path outside its Files:, writes the worker's
# handoff and change records where the real verb writes them, and prints the
# real verb's result contract (`fyr help work`).
set -uo pipefail

refuse() { echo "work: $*"; exit 2; }

# covers reports whether the Files: entry e holds the path p (kickoff.go covers).
covers() { [ "$1" = "$2" ] || { [ "${1%/}" != "$1" ] && [ "${2#"$1"}" != "$2" ]; }; }

# entries prints a Kickoff's Files: entries, one per line.
entries() { sed -n 's/^Files://p' "$1" | tr ',' '\n' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' | grep -v '^$'; }

[ $# -gt 0 ] || refuse "give at least one Kickoff file"
[ -z "$(git status --porcelain)" ] || refuse "the checkout has uncommitted changes; commit them first"
for k in "$@"; do
  n=$(basename "$k" .md)
  [ -f "$k" ] || refuse "the Kickoff $k: no such file"
  [[ $n =~ ^[a-z0-9][a-z0-9-]{0,62}$ ]] || refuse "the Kickoff $k: the name \"$n\" is not lower-case letters, digits and '-'"
  [ "$(grep -c '^Files:' "$k")" = 1 ] || refuse "the Kickoff $k: give one Files: line"
  ! git rev-parse -q --verify "refs/heads/work/$n" >/dev/null || refuse "the branch work/$n exists"
done
for a in "$@"; do
  for b in "$@"; do
    [[ $a < $b ]] || continue
    while read -r x; do
      while read -r y; do
        if covers "$x" "$y" || covers "$y" "$x"; then
          refuse "the Kickoffs $a and $b share a path ($x, $y)"
        fi
      done < <(entries "$b")
    done < <(entries "$a")
  done
done

cur=$(git symbolic-ref --short HEAD)
base=$(git rev-parse HEAD)
names=()
for k in "$@"; do names+=("$(basename "$k" .md)"); done
mkdir -p .agents/scratch/eval
echo "call head=$base kickoffs=${names[*]}" >>.agents/scratch/eval/work-calls.log
stamp=$(date +%Y-%m-%d-%H%M%S)-$$
echo "work: stamp $stamp  $# workers  $PWD/.agents/scratch/work/$stamp/"

# canned applies the change the task's words ask for and prints what it did.
canned() {
  local text
  text=$(tr '[:upper:]' '[:lower:]' <"$1")
  case "$text" in
    *goodbye*)
      printf '\n// Goodbye bids name farewell, or the world when name is blank.\nfunc Goodbye(name string) string {\n\tname = strings.TrimSpace(name)\n\tif name == "" {\n\t\tname = "world"\n\t}\n\treturn "goodbye, " + name\n}\n' >>internal/greet/greet.go
      printf '\nfunc TestGoodbye(t *testing.T) {\n\tif got := Goodbye(" ada "); got != "goodbye, ada" {\n\t\tt.Errorf("Goodbye = %%q, want goodbye, ada", got)\n\t}\n}\n' >>internal/greet/greet_test.go
      echo "Goodbye and TestGoodbye in internal/greet" ;;
    *welcome*)
      printf '\n// Welcome welcomes name, or the world when name is blank.\nfunc Welcome(name string) string {\n\tname = strings.TrimSpace(name)\n\tif name == "" {\n\t\tname = "world"\n\t}\n\treturn "welcome, " + name\n}\n' >>internal/greet/greet.go
      printf '\nfunc TestWelcome(t *testing.T) {\n\tif got := Welcome(""); got != "welcome, world" {\n\t\tt.Errorf("Welcome = %%q, want welcome, world", got)\n\t}\n}\n' >>internal/greet/greet_test.go
      echo "Welcome and TestWelcome in internal/greet" ;;
    *shout*)
      mkdir -p internal/shout
      printf '// Package shout raises a string to a shout.\npackage shout\n\nimport "strings"\n\n// Shout is s upper-cased with a trailing "!".\nfunc Shout(s string) string {\n\treturn strings.ToUpper(s) + "!"\n}\n' >internal/shout/shout.go
      printf 'package shout\n\nimport "testing"\n\nfunc TestShout(t *testing.T) {\n\tif got := Shout("hi"); got != "HI!" {\n\t\tt.Errorf("Shout = %%q, want HI!", got)\n\t}\n}\n' >internal/shout/shout_test.go
      echo "Shout and TestShout in internal/shout" ;;
    *tab*)
      printf '\nfunc TestHelloTab(t *testing.T) {\n\tif got := Hello("\\t"); got != "hello, world" {\n\t\tt.Errorf("Hello(tab) = %%q, want hello, world", got)\n\t}\n}\n' >>internal/greet/greet_test.go
      echo "TestHelloTab in internal/greet" ;;
  esac
}

fails=0
for k in "$@"; do
  n=$(basename "$k" .md)
  model=$(sed -n 's/^Model:[[:space:]]*//p' "$k" | head -1)
  git checkout -q -b "work/$n" "$base"
  did=$(canned "$k")
  line_tail="agy exit 0  61s  model ${model:-default}"
  if [ -z "$did" ]; then
    git checkout -q "$cur"
    echo "work: $n  FAIL (no commits)  $line_tail  work/$n not fetched"
    fails=1
    continue
  fi
  git add -A
  git -c user.name=worker -c user.email=worker@localhost commit -qm "$n: $did"
  tip=$(git rev-parse HEAD)
  outside=""
  while read -r p; do
    ok=""
    while read -r e; do covers "$e" "$p" && ok=1; done < <(entries "$k")
    [ -n "$ok" ] || outside="$p"
  done < <(git diff --name-only "$base" "$tip")
  git checkout -q "$cur"
  branch="work/$n ${base:0:7}..${tip:0:7} (1 commits)"
  if [ -n "$outside" ]; then
    echo "work: $n  FAIL (changed outside its Files: $outside)  $line_tail  $branch"
    fails=1
    continue
  fi
  when=$(date +%Y-%m-%d-%H%M)
  hid="HANDOFF-$when-$n" cid="CHANGE-$when-$n"
  rel=".agents/scratch/work/$stamp/$n/records/"
  mkdir -p "$rel"
  printf 'Worker %s: %s\n' "$n" "$did" >"$rel$hid.title"
  printf '# Handoff: %s\n\n## Completed\n\n- %s, committed on work/%s; go test ./... passes.\n\n## Next Steps\n\n- None.\n' "$n" "$did" "$n" >"$rel$hid.md"
  printf 'Change %s: %s\n' "$n" "$did" >"$rel$cid.title"
  printf '# Change: %s\n\n%s.\n' "$n" "$did" >"$rel$cid.md"
  echo "work: $n  PASS  $line_tail  $branch"
  echo "work: $n: handoff $hid"
  echo "work: $n | ## Completed"
  echo "work: $n | - $did, committed on work/$n; go test ./... passes."
  echo "work: $n | ## Next Steps"
  echo "work: $n | - None."
  echo "work: $n: change $cid  Change $n: $did"
  echo "work: $n: records $rel (2 records)"
done
exit "$fails"
