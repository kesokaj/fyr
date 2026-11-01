#!/usr/bin/env bash
# Builds the E2E fixture of `fyr auto` (D13): a clean checkout, pushed to its
# own bare remote, idle for eight hours by every clock G7 reads, whose one spec
# is stale. It runs nothing against a real home and removes nothing: a path
# that is already there stops it.
#
# usage: mkfixture.sh <fyr binary>
set -euo pipefail

fyr=${1:?usage: mkfixture.sh <fyr binary>}
base=$HOME/sandbox/fyr-auto
home=$base/home
remote=$base/fixture-remote.git
fixture=$base/fixture
source=$HOME/sandbox/fyr-eval/fixture

for path in "$home" "$remote" "$fixture"; do
	if [[ -e $path ]]; then
		echo "mkfixture: $path exists; move it away first" >&2
		exit 1
	fi
done

stamp=$(date -d '8 hours ago' --iso-8601=seconds)
export GIT_AUTHOR_DATE=$stamp GIT_COMMITTER_DATE=$stamp
export GIT_AUTHOR_NAME=fixture GIT_AUTHOR_EMAIL=f@localhost
export GIT_COMMITTER_NAME=fixture GIT_COMMITTER_EMAIL=f@localhost

git clone -q --bare --no-hardlinks "$source" "$remote"
git clone -q "$remote" "$fixture"
mkdir -p "$home"
cd "$fixture"

# The marker the apply step reads (D10), on the suite's own line.
sed -i 's|^gofmt -l cmd internal && go vet ./... && go test -race ./...$|go test ./...   # auto: test|' AGENTS.md
printf '`internal/greet/greet.go` greets.\n' | "$fyr" record add spec --title 'greeting' --id SPEC-greet --home "$home"
git add AGENTS.md .agents/trail.jsonl
git commit -q -m 'fixture: the spec and the test marker'

# A change to the spec's code after the spec: fyr check reports spec-stale.
printf '\n// A comment the spec never saw.\n' >>internal/greet/greet.go
git add internal/greet/greet.go
git commit -q -m 'fixture: change the code under the spec'
git push -q origin HEAD

git ls-files -z | xargs -0 touch -d '8 hours ago'
"$fyr" index --register --home "$home" >/dev/null
touch -d '8 hours ago' .agents/trail.jsonl
echo "$fixture registered under $home"
