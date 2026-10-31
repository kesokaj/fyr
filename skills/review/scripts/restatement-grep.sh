#!/usr/bin/env bash
# restatement-grep.sh — every living file that restates a claim.
#
# A behaviour change includes the text that describes it; a description that
# outlives its behaviour reads as a guarantee. This script finds every
# restatement of a changed claim across the living files of the checkout, so a
# review's --docs lens reads them before the first round, not one per round.
#
# Mode 0644 on purpose and always invoked as `bash <path>`: the harness is also
# embedded in a release binary, and an embedded filesystem carries no exec bit
# (the harness's embedded-plan test fails on a 0755 file).
#
# Usage: bash restatement-grep.sh [-i] [--all] [--] <word> [<word> ...]
#   -i     case-insensitive
#   --all  a line counts only when it holds every word (default: any word)
#   --     ends the options; a word may start with `-` (`--append-system-prompt`)
#          and any token after the first word is a word too
# Output: path:line: text, one per hit (`-H`: the path even for a single file), over the LIVING files: `git ls-files`
# minus the records (`.agents/trail.jsonl`, the brain store's export) and
# minus the vendored skills and vendored catalogs named in skills/VENDORED.md (a
# first cell may be a path, `google-catalog/catalog`), which are never edited.
# The kit's scripts ARE living files; the living records (specs, guides, deploy
# docs) are searched with `fyr search <words> --kind spec|guide|deploy`.
# Tracked files only: a file created this session is invisible until it is
# staged. Exit 0 with hits, 1 with none, 2 on usage.
set -euo pipefail
ci=""; all=0; words=()
while [ $# -gt 0 ]; do
  case "$1" in
    -i) [ ${#words[@]} -eq 0 ] && { ci="-i"; shift; continue; } ;;
    --all) [ ${#words[@]} -eq 0 ] && { all=1; shift; continue; } ;;
    --) shift; words+=("$@"); break ;;
  esac
  words+=("$1"); shift
done
[ ${#words[@]} -gt 0 ] || { echo "usage: bash restatement-grep.sh [-i] [--all] [--] <word> [<word> ...]" >&2; exit 2; }
root=$(git rev-parse --show-toplevel 2>/dev/null) || { echo "restatement-grep: not a git checkout" >&2; exit 2; }
cd "$root"
# The living files: everything git tracks except the records and the vendored skills.
# `|| true` on the whole pipeline: without VENDORED.md the list is empty, the
# final grep exits 1, and under pipefail the substitution would take the script
# down with it — exit 1, indistinguishable from "no hits", in every project
# that is not the harness (BUG-138).
vendored=$( { { grep -E '^\| [a-z0-9/-]+ \|' skills/VENDORED.md 2>/dev/null || true; } | awk -F'|' '{gsub(/ /,"",$2); print $2}' | grep -v -E '^(Skill)$' | sed 's#^#^skills/#; s#$#/#'; } || true )
mapfile -t files < <(git ls-files -z | tr '\0' '\n' \
  | grep -v -x -F '.agents/trail.jsonl' \
  | { if [ -n "$vendored" ]; then grep -v -E "$(printf '%s\n' "$vendored" | paste -sd'|' -)"; else cat; fi; } || true)
[ ${#files[@]} -gt 0 ] || exit 1
hits=0
if [ "$all" -eq 1 ]; then
  # Every word on the line: chain one grep per word.
  while IFS= read -r line; do
    keep=1
    for w in "${words[@]}"; do
      grep -q $ci -F -- "$w" <<< "${line#*:*:}" || { keep=0; break; }
    done
    [ "$keep" -eq 1 ] && { printf '%s\n' "$line"; hits=$((hits+1)); }
  done < <(grep -nH $ci -F -- "${words[0]}" "${files[@]}" 2>/dev/null || true)
else
  pattern=$(printf '%s\n' "${words[@]}")
  while IFS= read -r line; do
    printf '%s\n' "$line"; hits=$((hits+1))
  done < <(grep -nH $ci -F -- "$pattern" "${files[@]}" 2>/dev/null || true)
fi
[ "$hits" -gt 0 ] && exit 0 || exit 1
