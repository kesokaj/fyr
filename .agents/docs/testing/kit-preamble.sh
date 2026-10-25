#!/usr/bin/env bash
# kit-preamble.sh — sourced first by every behavioural or sandbox run kit built
# from guide.md § 4 (`. .agents/docs/testing/kit-preamble.sh` at the top of the
# kit script in scratch). A run proves its setup before its output counts
# (BUG-087, 090, 091, 106, 107, 115, 116, 123): this file is that proof, in
# one place, so a kit cannot skip a step by forgetting it. Sourced, never
# executed, so its mode is 0644 like every other file under the docs root.
#
# Inputs (environment):
#   KIT_CLONE          the fixture clone the CLI runs in (required)
#   KIT_HOME           the sandbox home the run uses (required; never the real home)
#   KIT_ASSERT_GREP    newline-separated `file:pattern` lines, each relative to
#                      KIT_CLONE; every pattern must match its file (optional)
#   KIT_ASSERT_COMMIT  one commit SHA (7 to 40 hex digits, never a ref name)
#                      that must be in the clone's history (optional)
#   KIT_BIN            the fyr build to wrap (default: a fresh `go build` of the
#                      harness checkout this file lives in, run from any cwd)
# Effects: asserts the fixture, refuses a clone that sits inside another git
# checkout (BUG-106), lists ~/.config/fyr and ~/.config/sh-agent before the
# run and traps EXIT to list them after (BUG-107, BUG-123; both until every
# place has crossed, plan fyr D29), and puts a wrapper first on PATH that pins
# `--home "$KIT_HOME"` on every fyr call the agent makes (BUG-107). At exit
# it also fails the run when any credential is still in KIT_HOME (BUG-160): a
# kit deletes what it copied before it ends, and this check proves it did.
# The exit check (processes left by the run, the real config, credentials) is that EXIT trap and nothing else: a kit never sets an EXIT
# trap of its own after sourcing this file and never ends in `exec <cli>`,
# and a run counts only when its output holds "kit: no processes left by the run"
# (on a machine without /proc, "kit: no /proc here: …", which its log names as a gap),
# "kit: real config unchanged" and "kit: no credentials left in KIT_HOME" (or a FAIL line).
set -euo pipefail
: "${KIT_CLONE:?KIT_CLONE is required}"
: "${KIT_HOME:?KIT_HOME is required}"
case "$KIT_HOME" in "$HOME"|"$HOME/") echo "kit: KIT_HOME is the real home; refusing" >&2; exit 2;; esac
# Absolute: the exit check runs after the kit has changed directory.
case "$KIT_HOME" in /*) ;; *) echo "kit: KIT_HOME must be an absolute path, got '$KIT_HOME'" >&2; exit 2;; esac
case "$KIT_CLONE" in /*) ;; *) echo "kit: KIT_CLONE must be an absolute path, got '$KIT_CLONE'" >&2; exit 2;; esac
# The exit trap sends SIGTERM to processes under these two roots (BUG-163), so they are resolved once, here, and
# refused when they are /, the real home or a parent of it, or the harness checkout or a parent of it: a
# KIT_HOME of "$HOME/." or a link to the home passes the literal check above (security review 2026-10-08).
kit_root_clone=$(cd "$KIT_CLONE" 2>/dev/null && pwd -P) || { echo "kit: $KIT_CLONE does not exist" >&2; exit 2; }
# Resolved without creating it: a refused root is never made.
if [ -d "$KIT_HOME" ]; then kit_root_home=$(cd "$KIT_HOME" && pwd -P)
else kit_root_home=$(cd "$(dirname "$KIT_HOME")" 2>/dev/null && pwd -P)/$(basename "$KIT_HOME") || { echo "kit: the parent of $KIT_HOME does not exist" >&2; exit 2; }; fi
kit_real_home=$(cd "$HOME" && pwd -P)
kit_repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd -P)
for kit_r in "$kit_root_clone" "$kit_root_home"; do
  case "$kit_real_home/" in "$kit_r"/*) echo "kit: $kit_r is the real home or a parent of it; refusing" >&2; exit 2;; esac
  case "$kit_repo_root/" in "$kit_r"/*) echo "kit: $kit_r is the harness checkout or a parent of it; refusing" >&2; exit 2;; esac
  [ "$kit_r" != / ] || { echo "kit: a kit root resolves to /; refusing" >&2; exit 2; }
done
mkdir -p "$KIT_HOME"
kit_home_env=$KIT_HOME   # the HOME the CLI ran with, frozen here: the exit check never re-reads a changeable KIT_HOME
readonly kit_root_clone kit_root_home kit_home_env
[ -d "$KIT_CLONE/.git" ] || { echo "kit: $KIT_CLONE is not a git clone" >&2; exit 2; }
# The clone's parent is no checkout: an ancestor AGENTS.md would load (BUG-106).
if git -C "$(dirname "$KIT_CLONE")" rev-parse --git-dir >/dev/null 2>&1; then
  echo "kit: $(dirname "$KIT_CLONE") is inside a git checkout; the clone must live outside every checkout (BUG-106)" >&2; exit 2
fi
# Fixture facts the dispatch states, asserted before the CLI starts.
if [ -n "${KIT_ASSERT_GREP:-}" ]; then
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    f="${line%%:*}"; p="${line#*:}"
    grep -q -- "$p" "$KIT_CLONE/$f" || { echo "kit: fixture assert failed: $f lacks '$p'" >&2; exit 2; }
  done <<< "$KIT_ASSERT_GREP"
fi
if [ -n "${KIT_ASSERT_COMMIT:-}" ]; then
  # Asked of git directly, never `git log | grep -q`: under pipefail grep's
  # early exit kills git log on a long history and the status is 141, which
  # refused a commit that was there (BUG-157).
  # A SHA, nothing else: `HEAD`, a branch or a tag name would be an ancestor
  # of HEAD whatever the fixture holds, and the assertion would prove nothing.
  [[ "$KIT_ASSERT_COMMIT" =~ ^[0-9a-f]{7,40}$ ]] || { echo "kit: KIT_ASSERT_COMMIT must be a commit SHA of 7 to 40 hex digits, got '$KIT_ASSERT_COMMIT'" >&2; exit 2; }
  # Resolved first, and the full id must start with the value: git takes a
  # tag or branch whose name is hex digits (a date, `20261001`) before an
  # abbreviated id, and such a ref would pass as "the commit".
  kit_full=$(git -C "$KIT_CLONE" rev-parse --verify --quiet --end-of-options "${KIT_ASSERT_COMMIT}^{commit}" 2>/dev/null) || kit_full=""
  case "$kit_full" in
    "") echo "kit: commit $KIT_ASSERT_COMMIT not in the clone's log" >&2; exit 2 ;;
    "$KIT_ASSERT_COMMIT"*) ;;
    *) echo "kit: KIT_ASSERT_COMMIT must be a commit SHA: '$KIT_ASSERT_COMMIT' names a ref in the clone" >&2; exit 2 ;;
  esac
  git -C "$KIT_CLONE" merge-base --is-ancestor "$kit_full" HEAD 2>/dev/null || { echo "kit: commit $KIT_ASSERT_COMMIT not in the clone's log" >&2; exit 2; }
fi
# The real ~/.config/fyr and ~/.config/sh-agent before and after: a run that
# changes a name or a file's contents in either fails the kit. Both, until
# every place has crossed to fyr (plan fyr D29, Phase 6): before its crossing
# a box's real state is the old directory, and an old binary or a migration
# reaching it from a run is the same leak. The fingerprint covers names and
# contents, so an appended line to an existing file changes it; a names-only
# listing did not (docs-lens review 2026-10-01). It does not see: a rewrite
# with the same bytes, a `touch` or a `chmod`; a file made and removed again
# before the run ends; anything inside a directory it cannot list, or inside
# a subdirectory that is itself a symlink.
# The real home is remembered here: a kit overrides HOME for the CLI it runs,
# and the exit check must still look at the operator's own directory.
kit_real_config="$HOME/.config/fyr"
kit_old_config="$HOME/.config/sh-agent"
# Both tolerate a home that has no such directory yet: under pipefail a
# failing `ls` ended the preamble with status 2 and no message (BUG-158).
# The fingerprint is every path plus a checksum of every file, by tools macOS
# has too: `ls --time-style` and `md5sum` are GNU's, and without them the
# fingerprint was the same empty value before and after, proving nothing.
kit_list_real_config() { { ls -la "$kit_real_config/" "$kit_old_config/" 2>/dev/null || true; } | sed 's/^/  /'; }
kit_fingerprint() {
  # -H: the directory itself may be a symlink (a dotfiles setup), and find
  # would otherwise stop at it. Links inside are listed by name, never
  # walked (-L would follow one to `/` and read the whole machine). A link
  # that points at a regular file is read through, so that file's content
  # counts; any other link gives the path it points at, so re-pointing it
  # shows, and a link to a device or a fifo, which never ends, is not read.
  { find -H "$kit_real_config" "$kit_old_config" -print 2>/dev/null || true
    find -H "$kit_real_config" "$kit_old_config" -type f -exec cksum {} + 2>/dev/null || true
    find -H "$kit_real_config" "$kit_old_config" -type l -exec sh -c 'for p; do if [ -f "$p" ]; then cksum "$p"; else printf "%s -> %s\n" "$p" "$(readlink "$p")"; fi; done' sh {} + 2>/dev/null || true; } | LC_ALL=C sort | cksum | cut -d' ' -f1
}
echo "kit: real $kit_real_config and $kit_old_config before the run:"; kit_list_real_config
kit_before=$(kit_fingerprint)
# Credentials left in the sandbox home (BUG-160). It FAILS CLOSED: a path it
# cannot read is reported, never passed over (a guard keeps the data when its
# own check errors). By NAME: the kinds a kit copies or a CLI logs in to. By
# CONTENT: a token or private key a CLI wrote anywhere — Antigravity dumped
# GH_TOKEN into a terminal log — read in binary files too (a conversation
# database), except executables: the installed fyr carries the
# redaction patterns as text, as does the kit's own bin/fyr.real. A link
# to a file is read through; a link to a directory is reported, never walked
# (it could lead anywhere on the machine); KIT_HOME itself may be a link.
# Paths only, never the text.
kit_cred_names='^(id_rsa|id_ecdsa|id_ed25519|id_dsa|\.credentials\.json|application_default_credentials\.json|credentials\.db|access_tokens\.db|.*oauth-token.*|hosts\.yml|\.netrc|\.git-credentials|.*\.p12|.*\.pfx)$'
kit_cred_paths='/\.aws/credentials$|/\.docker/config\.json$'
kit_cred_text='gh[opsur]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,}|-----BEGIN [A-Z ]*PRIVATE KEY-----|sk-ant-[A-Za-z0-9_-]{20,}|sk-proj-[A-Za-z0-9_-]{20,}|"refresh_token"|ya29\.[A-Za-z0-9_-]{20,}|AIza[A-Za-z0-9_-]{35}|glpat-[A-Za-z0-9_-]{20,}'
# AWS access keys are checked apart: AWS's published example key is
# documentation, not a credential, and an agent that reads a test holding it
# (internal/redact/redact_test.go) leaves it in its transcripts. Only that
# exact key is set aside; any other AWS key, or any match of kit_cred_text, in
# the same file still fails the run.
kit_cred_aws='AKIA[A-Z0-9]{16}'
kit_cred_example='AKIAIOSFODNN7EXAMPLE'
kit_credential_leftovers() {
  if [ ! -e "$KIT_HOME" ]; then
    [ ! -L "$KIT_HOME" ] || printf '%s (a dangling link)\n' "$KIT_HOME"
    return 0
  fi
  local f base magic unreadable n
  # The scan's external tools are the programs on PATH, not a shell function
  # the kit script defined after sourcing this file (security review of
  # v2.6.1); the trap calls this in $( ), so the unset reaches only that
  # subshell. The scan otherwise trusts the kit's own shell: a kit that
  # redefines a builtin (printf, read, [, unset) can still blind it. The agent
  # under test runs in its own process and defines nothing here.
  unset -f find head od tr grep 2>/dev/null || true
  # What find itself cannot enter is a failure. Captured whole, never piped
  # into a reader that stops early (BUG-155).
  unreadable=$(find -H "$KIT_HOME" -print 2>&1 >/dev/null) || true
  [ -z "$unreadable" ] || printf '%s (find could not read)\n' "$unreadable"
  while IFS= read -r -d '' f; do
    base=${f##*/}
    if [ -L "$f" ] && [ -d "$f" ]; then printf '%s (a link to a directory, not walked)\n' "$f"; continue; fi
    if [ ! -r "$f" ]; then printf '%s (unreadable)\n' "$f"; continue; fi
    if [[ "$base" =~ $kit_cred_names ]] || [[ "$f" =~ $kit_cred_paths ]]; then printf '%s\n' "$f"; continue; fi
    [ -f "$f" ] || continue   # a link to a directory, device or fifo is not read
    magic=$(head -c 4 "$f" 2>/dev/null | od -An -tx1 | tr -d ' \n')
    case "$magic" in 7f454c46|cffaedfe|feedfacf|cafebabe) continue ;; esac   # ELF, Mach-O
    LC_ALL=C command grep -aqE -- "$kit_cred_text" "$f" 2>/dev/null; case $? in
      0) printf '%s\n' "$f"; continue ;;
      1) ;;
      *) printf '%s (could not be scanned)\n' "$f"; continue ;;
    esac
    # A matched key never sits in a variable (xtrace would print it) or a
    # here-string (a temp file on older bash): the matches are piped into a
    # count of the AWS keys that are not the example, and the count reads all
    # of its input, so no early exit (BUG-157). An empty count fails closed.
    # A read error (exit 2) adds a line that is not the example, so the file
    # is reported, never passed as "no key".
    n=$( { rc=0; LC_ALL=C command grep -aoE -- "$kit_cred_aws" "$f" 2>/dev/null || rc=$?; [ "$rc" -le 1 ] || echo unscannable; } | LC_ALL=C command grep -cvxF -- "$kit_cred_example" || true)
    [ "$n" = 0 ] || printf '%s\n' "$f"
  done < <(find -H "$KIT_HOME" \( -type f -o -type l \) -print0 2>/dev/null)
  return 0
}
# Processes the run left behind (BUG-163): a server or watcher the agent started outlives the CLI. A process
# counts when its physical cwd or executable is under the clone or KIT_HOME, or its environment carries
# HOME=$KIT_HOME (the kits run the CLI with that HOME, so a server it started from /tmp still carries it). Same uid
# only; this shell, its ancestors and its descendants (the check's own subshells) are never matched. Each match
# gets SIGTERM and fails the run. Every /proc read tolerates a vanished process. Linux /proc only: elsewhere the
# check says it did not run, which a log then names as a gap.
kit_ppid() { awk '/^PPid:/{print $2; exit}' "/proc/$1/status" 2>/dev/null || true; }
kit_process_leftovers() {
  if [ ! -d /proc/self ]; then echo "kit: no /proc here: the leftover-process check did not run"; return 0; fi
  local roots=("$kit_root_clone" "$kit_root_home") r p pid cwd exe hit a me found=0 skip name
  me=$(id -u); skip=" $$ "; a=$$
  while [ -n "$a" ] && [ "$a" -gt 1 ]; do a=$(kit_ppid "$a"); skip+="$a "; done
  for p in /proc/[0-9]*; do
    pid=${p#/proc/}
    case "$skip" in *" $pid "*) continue ;; esac
    [ "$(awk '/^Uid:/{print $2; exit}' "$p/status" 2>/dev/null || true)" = "$me" ] || continue
    a=$pid; while [ -n "$a" ] && [ "$a" -gt 1 ] && [ "$a" != "$$" ]; do a=$(kit_ppid "$a"); done
    [ "$a" != "$$" ] || continue                       # a descendant of this shell: the check itself
    cwd=$(readlink "$p/cwd" 2>/dev/null || true); exe=$(readlink "$p/exe" 2>/dev/null || true); hit=""
    for r in "${roots[@]}"; do
      case "$cwd" in "$r"|"$r"/*) hit=cwd ;; esac
      case "$exe" in "$r"/*) hit=exe ;; esac
    done
    # The environment is matched in the file, never copied into a variable (bash -x would print it) and never
    # piped (a grep -q pipe exits early: BUG-155); an exact NUL-separated entry, so a HOME that merely starts
    # with KIT_HOME does not match.
    if [ -z "$hit" ] && grep -qzxF -- "HOME=$kit_home_env" "$p/environ" 2>/dev/null; then hit=home; fi
    [ -n "$hit" ] || continue
    [ "$found" = 1 ] || echo "kit: FAIL processes left by the run (BUG-163); each was sent SIGTERM:" >&2
    # pid, match kind and the executable's name only: argv can carry a token, or escapes that fake a line.
    name=$(printf '%s' "${exe##*/}" | LC_ALL=C tr -cd '[:alnum:]._+-' | cut -c1-40)
    printf '  %s (%s) %s\n' "$pid" "$hit" "${name:-?}" >&2
    kill "$pid" 2>/dev/null || true; found=1
  done
  if [ "$found" = 1 ]; then sleep 1; return 1; fi
  echo "kit: no processes left by the run"
}
kit_on_exit() {
  local rc=$? failed=0 left
  cd / || true   # the check's own subshells must not sit in the clone (plan 2026-10-08 D4)
  kit_process_leftovers || failed=1
  echo "kit: real $kit_real_config and $kit_old_config after the run:"; kit_list_real_config
  if [ "$kit_before" != "$(kit_fingerprint)" ]; then
    echo "kit: FAIL the real ~/.config/fyr or ~/.config/sh-agent changed during the run (BUG-107, BUG-123); the run does not count" >&2
    failed=1
  else
    # Said in so many words: a run whose output holds neither this line nor
    # the FAIL line above did not reach the check.
    echo "kit: real config unchanged"
  fi
  left=$(kit_credential_leftovers)
  if [ -n "$left" ]; then
    echo "kit: FAIL credentials left in KIT_HOME (BUG-160); delete them and re-check before the run counts:" >&2
    printf '%s\n' "$left" | sed 's/^/  /' >&2
    failed=1
  else
    echo "kit: no credentials left in KIT_HOME"
  fi
  [ "$failed" = 0 ] || exit 1
  exit "$rc"
}
trap kit_on_exit EXIT
# A wrapper first on PATH pins --home for every fyr call the agent makes.
mkdir -p "$KIT_HOME/bin"
# Built fresh every run (the build cache makes it cheap): a stale binary from
# an earlier kit measured the wrong code (docs-lens review 2026-10-01). A kit
# that must use a given binary sets KIT_BIN.
if [ -z "${KIT_BIN:-}" ]; then
  KIT_BIN="$KIT_HOME/bin/fyr.real"
  kit_repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)
  go build -C "$kit_repo" -o "$KIT_BIN" ./cmd/fyr || { echo "kit: go build of fyr in $kit_repo failed; set KIT_BIN to a build" >&2; exit 2; }
fi
printf '#!/usr/bin/env bash\nexec %q "$1" --home %q "${@:2}"\n' "$KIT_BIN" "$KIT_HOME" > "$KIT_HOME/bin/fyr"
chmod 0755 "$KIT_HOME/bin/fyr"
export PATH="$KIT_HOME/bin:$PATH"
hash -r
echo "kit: preamble ok — clone $KIT_CLONE, home $KIT_HOME, fyr wrapper first on PATH"
