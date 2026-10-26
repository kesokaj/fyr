#!/usr/bin/env bash
# refresh-catalog.sh — vendor the Google skills catalog at two pins.
#
#   bash skills/google-catalog/scripts/refresh-catalog.sh [--dest <dir>] [--work <dir>]
#        [--exclude <entry>]... <agents-cli pin> <google-skills pin>
#
# A pin is a full 40-digit commit id, never a short one, a tag or a branch.
#
# Run from the harness checkout, never from a session in another project. It
# clones both repositories in full (so any pin resolves) into the work
# directory, builds the catalog there, and only then replaces the entries under
# the destination. Vendored files are copied byte for byte; the one local
# change is the file mode (no executable bit: the embed has none).
#
# It refuses, naming the entry and leaving the destination untouched: a pin
# that does not resolve; an entry without SKILL.md frontmatter (name and
# description); an entry name in two source sets; a symlink; a go.mod, go.sum
# or *.go file (the Go tool and the embed would both mishandle the directory).
# A dotfile inside an entry, a symlinked LICENSE or source directory, and a
# directory name that is not lowercase letters, digits and hyphens are
# refused the same way. The operator then passes --exclude <entry> and records
# it under the manifest's "## Excluded"; every name listed there is excluded on
# each later refresh without the flag. What it cannot see cheaply — a secret-shaped file
# name, a path git ignores — is caught by the suite it tells you to run next
# (TestCatalogInstallsClean, TestEmbedHoldsOnlyTrackedFiles).
#
# The destination is replaced only when it is a catalog: an empty --dest, /,
# the home directory, the checkout and an ancestor of either are refused, in
# any spelling, as is a non-empty destination without CHECKSUMS; the swap
# removes only the directories that file names. The manifest's "## Excluded"
# and "## Notes" sections are kept byte for byte. The work directory is cloned
# into and its build directory removed: it is never /, the home directory or
# the checkout itself, never nested with --dest, and when it already holds
# files it must be one this script marked (.refresh-catalog-work) or completed
# a run in (origins.txt).
#
# INDEX.md and CHECKSUMS are written by the Go tests in write mode, so the
# index is derived by the installer's own frontmatter parser.
#
# Needs git, go and (for the default sources) the network. Two environment
# variables replace the source repositories, for the test's local fixtures:
# CATALOG_AGENTS_CLI_REPO and CATALOG_GOOGLE_SKILLS_REPO, each an https:// URL
# (with no user information or query string in it) or an absolute path,
# nothing else. No message of the script shows such a value, git's own output
# and tracing are off for its calls and git does not prompt, on the terminal
# or through an askpass program, and the manifest records
# the upstream repositories' names, never an override. git itself keeps the
# value in the work clones, and a run that used an override says so when it
# ends, whether it succeeded or failed. While git runs, the value is one of
# its arguments, which other users of the machine can read in the process
# list, and `bash -x` prints it: a token belongs in a credential helper,
# never in the URL. Relative --dest and --work are resolved
# against the caller's directory.
set -euo pipefail

# git has channels that no redirect of its output covers: a trace target (from
# the caller's environment or git config) records a clone's arguments, and a
# credential prompt, on the terminal or through an askpass program, shows the
# URL. All are off for every git call below; a credential belongs in a
# helper, which still runs. An empty GIT_ASKPASS also stops git falling back
# to core.askPass and SSH_ASKPASS. The three TRACE2 targets are set to 0
# because git config can name them with no variable of the caller's; every
# other GIT_TRACE* variable the caller exports is zeroed by the loop, except
# GIT_TRACE_REDACT, where 0 would switch redaction off; GIT_CURL_VERBOSE is
# removed, never zeroed: set to anything, 0 included, it turns curl's trace on.
export GIT_TERMINAL_PROMPT=0 GIT_ASKPASS= GIT_TRACE2=0 GIT_TRACE2_EVENT=0 GIT_TRACE2_PERF=0
unset GIT_CURL_VERBOSE
while IFS= read -r v; do export "$v=0"; done < <(compgen -e | grep '^GIT_TRACE' | grep -v '^GIT_TRACE_REDACT$' || true)
export GIT_TRACE_REDACT=1

usage() { sed -n '2,7p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2; }
fail() { echo "refresh-catalog: $*" >&2; exit 1; }
# git keeps a clone's source in its config, FETCH_HEAD and reflogs: with an
# override, that is the override's value, on disk under the work directory
# from the first clone or fetch on. The run says so when it ends, however it
# ends: done, refused through fail, stopped by a command under set -e, or
# ended by INT or TERM (the two traps below exit through the EXIT trap).
on_disk=""
override_note() {
  local rc=$?
  [ -n "$on_disk" ] || return 0
  local note="note: a source override was used; git keeps its value in the work clones under $work: delete that directory if the value is sensitive"
  if [ "$rc" -eq 0 ]; then echo "$note"; else echo "$note" >&2; fi
}
trap override_note EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo=$(cd "$script_dir/../../.." && pwd)
dest="$repo/skills/google-catalog/catalog"
work="$repo/.agents/scratch/catalog-refresh"
# One excluded name per line: a name is compared whole, so one with a space
# never excludes each of its words.
excludes=""
while [ $# -gt 0 ]; do
  case "$1" in
    --dest) dest=$2; shift 2 ;;
    --work) work=$2; shift 2 ;;
    --exclude) excludes="$excludes"$'\n'"$2"; shift 2 ;;
    --) shift; break ;;
    -*) usage; exit 2 ;;
    *) break ;;
  esac
done
[ $# -eq 2 ] || { usage; exit 2; }
[ -n "$dest" ] && [ -n "$work" ] || fail "--dest and --work must not be empty"
# Absolute before anything runs: the Go writer below runs with another working
# directory, where a relative path would name nothing (BUG-146).
abs() { case "$1" in /*) printf '%s\n' "$1" ;; *) printf '%s/%s\n' "$PWD" "$1" ;; esac; }
dest=$(abs "$dest")
work=$(abs "$work")
# One spelling per place before the guard compares anything: `..` and
# symlinks resolved through the nearest ancestor that exists; what is left
# below it is the part `mkdir -p` would create (security review of the
# v2.1.0 gate).
phys() {
  local p=$1 rest="" base
  while [ ! -d "$p" ] && [ "$p" != / ] && [ "$p" != . ]; do
    rest="/$(basename "$p")$rest"
    p=$(dirname "$p")
  done
  # A directory that cannot be entered resolves to nothing, and nothing plus
  # the rest would be a path under /: the callers stop on this status
  # (errexit does not reach into a command substitution).
  base=$(cd -P "$p" 2>/dev/null && pwd -P) || return 1
  # `pwd -P` keeps a leading `//` as it was typed; one slash is the same place.
  base="/${base#"${base%%[!/]*}"}"
  if [ -z "$rest" ]; then printf '%s\n' "$base"; else printf '%s%s\n' "${base%/}" "$rest"; fi
}
dest=$(phys "$dest") || fail "cannot resolve --dest: a directory on its path cannot be entered"
repo_phys=$(phys "$repo") || fail "cannot resolve the checkout's own path"
# No HOME, no home-directory guard: stop rather than run without it.
[ -n "${HOME:-}" ] || fail "HOME is not set: the destination guard compares --dest with the home directory"
home_phys=$(phys "$HOME") || fail "cannot resolve the home directory"
# After that, a `.` or `..` can only sit below a directory that does not
# exist, where nothing can say what it names until `mkdir -p` makes it real:
# refused, since it can name the home directory, the checkout or /.
case "$dest/" in
  */../* | */./*) fail "--dest $dest goes through . or .. below a directory that does not exist: name the directory itself" ;;
esac
# The swap below deletes directories under --dest, so --dest is never a place
# that holds anything else: not /, the home directory, the checkout, or an
# ancestor of either (security review S1).
case "${dest%/}/" in
  / | "${repo_phys%/}/") fail "--dest $dest is not a catalog directory" ;;
esac
case "${repo_phys%/}/" in "${dest%/}/"*) fail "--dest $dest contains the checkout" ;; esac
case "${dest%/}/" in "${home_phys%/}/") fail "--dest $dest is not a catalog directory" ;; esac
case "${home_phys%/}/" in "${dest%/}/"*) fail "--dest $dest contains the home directory" ;; esac
if [ -d "$dest" ] && [ -n "$(ls -A "$dest")" ] && [ ! -f "$dest/CHECKSUMS" ]; then
  fail "--dest $dest is not empty and has no CHECKSUMS: not a catalog, nothing is replaced there"
fi
# --work is cloned into and its build directory removed, so it gets the same
# care: one spelling, never a place that holds something else, never nested
# with --dest, and when it already holds files, only a directory this script
# marked (before its first clone) or completed a run in (origins.txt).
work=$(phys "$work") || fail "cannot resolve --work: a directory on its path cannot be entered"
case "$work/" in
  */../* | */./*) fail "--work $work goes through . or .. below a directory that does not exist: name the directory itself" ;;
esac
case "${work%/}/" in
  / | "${repo_phys%/}/" | "${home_phys%/}/") fail "--work $work must not be /, the home directory or the checkout itself" ;;
esac
case "${work%/}/" in "${dest%/}/"*) fail "--work $work and --dest $dest must not contain each other" ;; esac
case "${dest%/}/" in "${work%/}/"*) fail "--work $work and --dest $dest must not contain each other" ;; esac
if [ -d "$work" ] && [ -n "$(ls -A "$work")" ] && [ ! -f "$work/.refresh-catalog-work" ] && [ ! -f "$work/origins.txt" ]; then
  fail "--work $work is not a work directory of this script (not empty, and neither .refresh-catalog-work nor origins.txt in it): it would be cloned into and its build directory removed; name an empty or new directory, or delete this one if it is a leftover of this script"
fi
pin_a=$1
pin_g=$2
# A pin is the commit's full id. git checks a tag or branch named like a
# shorter one out before the abbreviated id, and nothing after the checkout
# can tell a commit that was made to share the prefix: whoever names refs at
# the source would choose what a short pin vendors. (A branch named like the
# full id is handled at the checkout.)
for pin in "$pin_a" "$pin_g"; do
  case "$pin" in *[!0-9a-f]*) pin_shape=bad ;; *) pin_shape=ok ;; esac
  if [ "$pin_shape" = bad ] || [ "${#pin}" -ne 40 ]; then
    fail "a pin must be a full commit id (40 hex digits), got: $pin"
  fi
done
# The upstream repositories, as the manifest names them. An override changes
# where the script fetches from, never what the manifest records: the pin
# says what the content is, and an override's value may hold a token.
upstream_a=https://github.com/google/agents-cli
upstream_g=https://github.com/google/skills
url_a=${CATALOG_AGENTS_CLI_REPO:-$upstream_a}
url_g=${CATALOG_GOOGLE_SKILLS_REPO:-$upstream_g}
# An override is an https URL or an absolute path (a local clone, the tests'
# fixtures): git has transports that run a command (`ext::`), and plain http
# can be rewritten on the way.
for v in CATALOG_AGENTS_CLI_REPO CATALOG_GOOGLE_SKILLS_REPO; do
  val=${!v:-}
  case "$val" in
    "" | /*) ;;
    https://*)
      # No user information in the URL: a credential belongs in a helper,
      # and no message here ever shows the value.
      host=${val#https://}
      case "${host%%/*}" in
        *@*) fail "$v must be an https:// URL or an absolute path without credentials in it ; use a credential helper" ;;
      esac
      case "$val" in
        *\?*) fail "$v must be an https:// URL or an absolute path without credentials in it: no query string" ;;
      esac ;;
    *) fail "$v must be an https:// URL or an absolute path (its value is not shown: it may hold a credential)" ;;
  esac
done

# The four source sets: label, clone, path below it.
sets="agents-cli|agents-cli|skills
agents-cli-langchain|agents-cli|extensions/langchain/template/.agents/skills
google-skills-cloud|google-skills|skills/cloud
google-skills-developers|google-skills|skills/developers"

mkdir -p "$work"
# The marker goes in before the first clone, so a run that fails later (a pin
# that does not resolve) leaves a directory the corrected run accepts.
: > "$work/.refresh-catalog-work"
fetch() { # <clone name> <url> <pin>: a full clone, reused; checked out at the pin
  # A source is named by its role in every message, never by its value, and
  # git's own complaint is not shown: git repeats the value, cut or rewritten
  # in ways no filter here can follow, and a token can sit in a URL's path.
  local dir="$work/$1"
  if [ -d "$dir/.git" ]; then
    git -C "$dir" remote set-url -- origin "$2" >/dev/null 2>&1 ||
      fail "cannot point the $1 clone at its source (git's own message is not shown: it repeats the source's value; remove $dir and run again)"
    git -C "$dir" fetch -q origin '+refs/heads/*:refs/remotes/origin/*' >/dev/null 2>&1 ||
      fail "cannot fetch the $1 source (git's own message is not shown: it repeats the source's value; run git fetch in $dir to see it)"
  else
    git clone -q -- "$2" "$dir" >/dev/null 2>&1 ||
      fail "cannot clone the $1 source (git's own message is not shown: it repeats the source's value; run the git clone yourself to see it)"
  fi
  # `<pin>^{commit}`, never the bare id: checkout takes a local branch of
  # that exact name first, and a clone makes the source's default branch
  # local. Then the commit checked out is compared with the pin, whatever
  # the checkout did.
  git -C "$dir" -c advice.detachedHead=false checkout -q --detach "$3^{commit}" 2>/dev/null || fail "pin $3 does not resolve in the $1 source"
  [ "$(git -C "$dir" rev-parse HEAD)" = "$3" ] || fail "pin $3 did not check out as itself in the $1 source"
}
if [ "$url_a" != "$upstream_a" ] || [ "$url_g" != "$upstream_g" ]; then on_disk=1; fi
fetch agents-cli "$url_a" "$pin_a"
fetch google-skills "$url_g" "$pin_g"
full_a=$(git -C "$work/agents-cli" rev-parse HEAD)
full_g=$(git -C "$work/google-skills" rev-parse HEAD)

# Entries the security review excluded stay excluded: the manifest's list is
# merged into --exclude, so a refresh cannot bring one back by forgetting a flag.
if [ -f "$dest/MANIFEST.md" ]; then
  while IFS= read -r name; do
    excludes="$excludes"$'\n'"$name"
  done < <(sed -n '/^## Excluded/,/^## Notes/p' "$dest/MANIFEST.md" | sed -n 's/^- `\([^`]*\)`.*/\1/p')
fi

build="$work/build"
rm -rf "$build"
mkdir -p "$build"
origins="$work/origins.txt"
: > "$origins"
excluded() { case $'\n'"$excludes"$'\n' in *$'\n'"$1"$'\n'*) return 0 ;; esac; return 1; }

while IFS='|' read -r label clone sub; do
  root="$work/$clone/$sub"
  [ -d "$root" ] || fail "source set $label: no $sub at the pin"
  # No path component of a source set is a symlink: one would vendor
  # directories from outside the clone (security review S2).
  [ "$(cd "$root" && pwd -P)" = "$(cd "$work/$clone" && pwd -P)/$sub" ] || fail "source set $label: $sub leaves the clone through a symlink"
  for d in "$root"/*/; do
    [ -d "$d" ] || continue
    name=$(basename "$d")
    excluded "$name" && continue
    # A skill name is lowercase letters, digits and hyphens: anything else
    # (a space, a capital, a glob character) would break the bookkeeping
    # below. Excluding it comes first, so one odd upstream directory never
    # blocks every refresh. The name is printed shell-quoted both times: it is
    # upstream's, the operator pastes the hint into a shell, and a raw escape
    # sequence must not reach the terminal.
    case "$name" in
      *[!a-z0-9-]* | -*) fail "entry $(printf '%q' "$name") ($label) is not a plain skill name (lowercase letters, digits, hyphens); the catalog cannot carry it (--exclude $(printf '%q' "$name"))" ;;
    esac
    [ ! -L "${d%/}" ] || fail "entry $name ($label) is itself a symlink (--exclude $name)"
    [ -f "$d/SKILL.md" ] || fail "entry $name ($label) has no SKILL.md (--exclude $name)"
    prev=$(awk -v n="$name" '$1 == n {print $2}' "$origins")
    [ -z "$prev" ] || fail "entry $name is in two source sets, $prev and $label (--exclude $name drops both)"
    # `-quit`, never `| head -1`: under pipefail a find that wrote more than a
    # pipe holds ended the script with status 141 and no message.
    bad=$(find "${d%/}" -type l -print -quit)
    [ -z "$bad" ] || fail "entry $name ($label) holds a symlink: $(printf '%q' "${bad#"$root"/}") (--exclude $name)"
    bad=$(find "${d%/}" -type f \( -name go.mod -o -name go.sum -o -name '*.go' \) -print -quit)
    [ -z "$bad" ] || fail "entry $name ($label) holds a Go file the module would pick up: $(printf '%q' "${bad#"$root"/}") (--exclude $name)"
    bad=$(find "${d%/}" -mindepth 1 -name '.*' -print -quit)
    [ -z "$bad" ] || fail "entry $name ($label) holds a dotfile that would change how git or a tool treats the subtree: $(printf '%q' "${bad#"$root"/}") (--exclude $name)"
    mkdir -p "$build/$name"
    cp -R "${d%/}/." "$build/$name/"
    printf '%s %s\n' "$name" "$label" >> "$origins"
  done
done <<SETS
$sets
SETS
[ -s "$origins" ] || fail "no entry found in the four source sets"

find "$build" -type f -exec chmod 644 {} +
find "$build" -type d -exec chmod 755 {} +
for lic in "$work/agents-cli/LICENSE" "$work/google-skills/LICENSE"; do
  [ ! -L "$lic" ] || fail "LICENSE is a symlink at the pin: ${lic#"$work"/} (it would copy a local file into the catalog)"
  [ -f "$lic" ] || fail "a repository has no LICENSE file at the pin: ${lic#"$work"/}"
done
cp -P "$work/agents-cli/LICENSE" "$build/LICENSE-agents-cli"
cp -P "$work/google-skills/LICENSE" "$build/LICENSE-google-skills"
chmod 644 "$build/LICENSE-agents-cli" "$build/LICENSE-google-skills"
# The catalog is data, not the checkout's own text: `fyr search` leaves a
# directory holding this marker out of its index.
: > "$build/.fyr-noindex"

# INDEX.md and CHECKSUMS, by the Go tests in write mode over the build; an
# entry without frontmatter name and description fails here, before the swap.
if ! (cd "$repo" && CATALOG_DIR="$build" CATALOG_WRITE=1 go test ./internal/harness \
    -run '^TestCatalog(MatchesChecksums|IndexMatchesEntries|FilesAreCarryable)$' -count=1 > "$work/writer.log" 2>&1); then
  grep -E 'catalog_[a-z_]*test\.go|catalog entry|FAIL' "$work/writer.log" | head -20 >&2 || true
  fail "the catalog built from these pins is refused (lines above; --exclude the entry named)"
fi

# The manifest: rows rewritten, the fetched date kept for a row whose pin did
# not change, the hand-written sections kept byte for byte.
old="$dest/MANIFEST.md"
today=$(date +%F)
count() { awk -v l="$1" '$2 == l' "$origins" | wc -l | tr -d ' '; }
fetched() { # <path cell> <pin>: the old row's date when its pin is the same
  if [ -f "$old" ]; then
    d=$(awk -F'|' -v p=" $1 " -v pin=" $2 " '$3 == p && $4 == pin {gsub(/ /, "", $6); print $6; exit}' "$old")
    [ -n "$d" ] && { echo "$d"; return; }
  fi
  echo "$today"
}
row() { printf '| %s | %s | %s | Apache-2.0 | %s | %s |\n' "$1" "$2" "$3" "$(fetched "$2" "$3")" "$(count "$4")"; }
table="| Repository | Path | Pin | License | Fetched | Entries |
|---|---|---|---|---|---|
$(row "$upstream_a" "skills" "$full_a" agents-cli)
$(row "$upstream_a" "extensions/langchain/template/.agents/skills" "$full_a" agents-cli-langchain)
$(row "$upstream_g" "skills/cloud" "$full_g" google-skills-cloud)
$(row "$upstream_g" "skills/developers" "$full_g" google-skills-developers)"
via=""
if [ "$url_a" != "$upstream_a" ] || [ "$url_g" != "$upstream_g" ]; then
  via="
Fetched through an override of a source (a mirror or a local clone), whose value is not recorded; the pins identify the content.
"
fi
tail_sections="## Excluded

Upstream entries kept out, with the reason; the refresh script reads this list and excludes each name on every run.

- none

## Notes

Per-entry notes from the security review; the vendored text itself is never edited.

- none"
manifest_date=$today
if [ -f "$old" ]; then
  if grep -q '^## Excluded' "$old"; then tail_sections=$(sed -n '/^## Excluded/,$p' "$old"); fi
  old_table=$(sed -n '/^| Repository | Path |/,/^$/p' "$old" | sed '/^$/d')
  if [ "$old_table" = "$table" ]; then manifest_date=$(awk '/^date: /{sub(/^date: /, ""); print; exit}' "$old"); fi
fi
cat > "$build/MANIFEST.md" <<MANIFEST
---
owner: main
date: $manifest_date
---
# Google skills catalog — provenance

Skills published by Google, vendored verbatim at the pins below by \`scripts/refresh-catalog.sh\` and reached through the router skill one directory up. \`INDEX.md\` is derived from the entries' frontmatter. \`CHECKSUMS\` shows that the committed tree has not changed since the refresh wrote it; it does not prove the tree matches upstream — for that, run the script at the pins with \`--dest\` in scratch and \`diff -r\` the result against this directory (provenance rests on git's commit ids and TLS to github.com).

$table
$via
Local changes: file modes only (no executable bit).

$tail_sections
MANIFEST

# The swap: entries replaced, entries upstream dropped removed.
mkdir -p "$dest"
before=$(cd "$dest" && find . -mindepth 1 -maxdepth 1 -type d | sed 's|^\./||' | sort)
after=$(awk '{print $1}' "$origins" | sort)
# Only the entry directories the old CHECKSUMS names are removed, never
# whatever else a wrong --dest might hold.
if [ -f "$dest/CHECKSUMS" ]; then
  # `if`, never `[ … ] && rm`: under pipefail a false test on the last name
  # would end the script here, silently, with the other entries already gone;
  # and a CHECKSUMS with no entry line must not end it either (BUG-138's shape).
  awk '{print $2}' "$dest/CHECKSUMS" | { grep / || true; } | cut -d/ -f1 | sort -u | while read -r old_entry; do
    case "$old_entry" in "" | . | .. | */*) continue ;; esac
    if [ -d "$dest/$old_entry" ]; then
      rm -rf -- "${dest:?}/$old_entry"
    fi
  done
fi
cp -R "$build/." "$dest/"

while IFS='|' read -r label clone sub; do
  echo "$label: $(count "$label") entries"
done <<SETS
$sets
SETS
added=$(comm -13 <(printf '%s\n' "$before") <(printf '%s\n' "$after") | tr '\n' ' ')
removed=$(comm -23 <(printf '%s\n' "$before") <(printf '%s\n' "$after") | tr '\n' ' ')
[ "$before" = "" ] && added="(first import: $(printf '%s\n' "$after" | wc -l | tr -d ' ') entries)"
added=${added% }; echo "added: ${added:-none}"
removed=${removed% }; echo "removed: ${removed:-none}"
ex=$(printf '%s\n' "$excludes" | sed '/^$/d' | sort -u | tr '\n' ' ')
echo "excluded: ${ex:-none}"
echo "next: git add skills/google-catalog && go test ./..."
