#!/usr/bin/env bash
# check-trailers.sh — run before every push (handoff Step 6, preflight Step 6.4).
# Exits 1 and names each unpushed commit whose message carries a CLI trailer
# line (Claude-Session:, Co-Authored-By:), because the author is the local git
# config only and the rule written as a sentence did not hold (BUG-177).
# Unpushed means `git log @{u}..HEAD`; on a branch with no upstream, every
# commit no remote has. Exit 0: clean. Exit 1: a trailer. Exit 2: git failed,
# which never passes for clean. Not a git hook: a step the skill runs.
set -euo pipefail

if ! git rev-parse --git-dir >/dev/null 2>&1; then
  echo "check-trailers: not inside a git repository" >&2
  exit 2
fi
if git rev-parse --verify --quiet '@{u}' >/dev/null 2>&1; then
  range=('@{u}..HEAD')
else
  range=(HEAD --not --remotes)
fi
if ! commits=$(git log --format=%H "${range[@]}"); then
  echo "check-trailers: git log ${range[*]} failed" >&2
  exit 2
fi

bad=0
for c in $commits; do
  # The message is read whole first: `git log | grep -q` under pipefail turns
  # grep's early exit into git's SIGPIPE, and a trailer would read as clean (BUG-155).
  msg=$(git log -1 --format=%B "$c") || { echo "check-trailers: cannot read $c" >&2; exit 2; }
  if grep -qiE '^[[:space:]]*(Claude-Session|Co-Authored-By):' <<<"$msg"; then
    echo "check-trailers: $(git log -1 --format='%h %s' "$c") carries a trailer; amend or reword it before the push" >&2
    bad=1
  fi
done
exit "$bad"
