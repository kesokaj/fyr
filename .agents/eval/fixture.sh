#!/usr/bin/env bash
# fixture.sh <scenario>: turns a trial's clone of this checkout into the demo
# project a row of GUIDE-eval.md runs against. Run by a row's setup, in the
# clone, with $FYR (the fyr under test) and $SB (the trial's home) set by
# `fyr eval`. Every record it seeds goes to $SB's store, never the operator's.
# The whole body is one function, read before it runs, because the first step
# removes this file from the work tree.
main() {
  set -euo pipefail
  : "${FYR:?FYR is the fyr under test}" "${SB:?SB is the trial home}"
  local scenario="${1:?usage: fixture.sh <scenario>}"
  case "$scenario" in
    bare | project | project-empty | review | reviewer | reviewer-wide | reviewer-wide-bug | reviewer-cold | resume | work | work-plan | tasks | retro | distill | dream | preflight) ;;
    *) echo "fixture.sh: unknown scenario $scenario" >&2; return 2 ;;
  esac
  # The stand-in verb is read before demo_tree empties the work tree.
  local fake=""
  case "$scenario" in tasks | work-plan) fake=$(cat .agents/eval/fake-work.sh) ;; esac
  demo_tree
  [ "$scenario" = bare ] || scaffold
  [ "$scenario" != project-empty ] || rm -rf cmd internal go.mod
  git add -A
  git commit -qm "demo: a greeting service"
  replace_history
  [ "$scenario" = bare ] || seed_project
  case "$scenario" in
    review) review_change ;;
    reviewer) reviewer_commit ;;
    reviewer-wide) reviewer_wide ;;
    reviewer-wide-bug) reviewer_wide; seeded_bug ;;
    reviewer-cold) reviewer_cold ;;
    resume) resume_seed ;;
    work) work_wave ;;
    work-plan) stand_in_verb "$fake"; work_plans ;;
    tasks) stand_in_verb "$fake" ;;
    retro) feedback_trio "a correction" ;;
    distill) feedback_trio "a repeated procedure" ;;
    dream) memory_trio ;;
    preflight) preflight_tag ;;
  esac
}

rec() { "$FYR" record add "$@" --home "$SB" >/dev/null; }

# demo_tree empties the work tree on an orphan branch and writes the demo:
# a Go HTTP service with one tested package.
demo_tree() {
  git checkout -q --orphan demo
  git rm -rqf .
  git clean -fdq
  mkdir -p cmd/server internal/greet
  cat >go.mod <<'EOF'
module example.com/demo

go 1.22
EOF
  cat >cmd/server/main.go <<'EOF'
// Command server serves the demo's greeting over HTTP.
package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"example.com/demo/internal/greet"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, greet.Hello(r.URL.Query().Get("name")))
	})
	slog.Info("listening", "port", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		slog.Error("serve", "err", err)
		os.Exit(1)
	}
}
EOF
  greet_go
  cat >internal/greet/greet_test.go <<'EOF'
package greet

import "testing"

func TestHello(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"ada", "hello, ada"},
		{"  bob  ", "hello, bob"},
		{"", "hello, world"},
		{"   ", "hello, world"},
	} {
		if got := Hello(c.in); got != c.want {
			t.Errorf("Hello(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
EOF
  cat >README.md <<'EOF'
# demo

A greeting service: `GET /hello?name=ada` answers `hello, ada`, and
`GET /healthz` answers `ok`. Run it with `go run ./cmd/server` (port 8080,
or `PORT`).
EOF
}

greet_go() {
  cat >internal/greet/greet.go <<'EOF'
// Package greet builds the demo's greeting.
package greet

import "strings"

// Hello greets name, or the world when name is blank.
func Hello(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "world"
	}
	return "hello, " + name
}
EOF
}

# scaffold writes the files /init-project gives a project.
scaffold() {
  mkdir -p .agents/scratch .agents/skills .agents/rules
  : >.agents/scratch/.gitkeep
  printf '.agents/scratch/*\n!.agents/scratch/.gitkeep\n' >.gitignore
  cat >AGENTS.md <<'EOF'
# AGENTS.md — demo

A greeting service in Go: `cmd/server` serves `internal/greet` over HTTP.

## Setup commands

```bash
gofmt -l . && go vet ./... && go test ./...   # before any completion claim
go run ./cmd/server                            # serves on :8080, or $PORT
```

## Rules for working here

- This repository has no remote: commit after each task and never push.
- Stdlib only.
EOF
  cat >PRD.md <<'EOF'
# PRD — demo

## Must

- R1: `GET /hello?name=N` answers `hello, N`, trimmed; a blank name answers `hello, world`.
- R2: `GET /healthz` answers `ok`.

## Non-goals

- Authentication.
EOF
  cat >SDD.md <<'EOF'
# SDD — demo

- M1 `cmd/server`: the HTTP mux, the port from `PORT` (default 8080), slog logging.
- M2 `internal/greet`: `Hello`, the greeting rule of R1.
EOF
  cat >TDD.md <<'EOF'
# TDD — demo

- T1 `internal/greet`: table tests over `Hello`, run with `go test ./...`.
EOF
  cat >TODO.md <<'EOF'
# TODO

## Now

- Nothing in progress.

## Next

## Backlog
EOF
}

# replace_history makes the demo branch the only history: every other branch
# and tag and the remote go, so nothing the agent runs reaches this checkout.
replace_history() {
  local cur b
  cur=$(git symbolic-ref --short HEAD)
  git for-each-ref --format='%(refname:short)' refs/heads | while read -r b; do
    [ "$b" = "$cur" ] || git branch -D "$b" >/dev/null
  done
  git branch -m main
  git tag -l | while read -r t; do git tag -d "$t" >/dev/null; done
  git remote remove origin 2>/dev/null || true
}

seed_project() {
  rec guide --id GUIDE-testing --title "Testing the demo" <<'EOF'
# GUIDE-testing

Every change: `gofmt -l . && go vet ./... && go test ./...` from the root.
There are no integration or browser tiers.
EOF
  rec deploy --id DEPLOY-local --title "Running the demo locally" <<'EOF'
# DEPLOY-local

`go run ./cmd/server` serves on port 8080 by default, or on `PORT`.
Check it with `curl localhost:8080/healthz`.
EOF
}

# review_change leaves an uncommitted, unused function for /review to find.
review_change() {
  cat >>internal/greet/greet.go <<'EOF'

func shout(s string) string {
	return strings.ToUpper(s) + "!"
}
EOF
}

# untrimmed_greet writes the reviewer rows' regression: a blank or padded
# name is no longer trimmed, so "   " greets "   " instead of the world.
untrimmed_greet() {
  cat >internal/greet/greet.go <<'EOF'
// Package greet builds the demo's greeting.
package greet

// Hello greets name, or the world when name is empty.
func Hello(name string) string {
	if name == "" {
		name = "world"
	}
	return "hello, " + name
}
EOF
}

# reviewer_commit commits the regression alone.
reviewer_commit() {
  untrimmed_greet
  git commit -qam "greet: skip the trim"
}

# reviewer_wide commits the regression inside a 30-file change in two
# directories, under a subject that does not name it: a registry of greeting
# words in internal/lang, its test, and 27 generated language files.
reviewer_wide() {
  local pair
  mkdir -p internal/lang
  cat >internal/lang/lang.go <<'EOF'
// Package lang holds the greeting word of each language, by its ISO 639-1 code.
package lang

var words = map[string]string{}

// register records a language's greeting word; each language file calls it.
func register(code, word string) { words[code] = word }

// Word returns the greeting word for code, and whether the language is known.
func Word(code string) (string, bool) {
	w, ok := words[code]
	return w, ok
}
EOF
  cat >internal/lang/lang_test.go <<'EOF'
package lang

import "testing"

func TestWord(t *testing.T) {
	if w, ok := Word("en"); !ok || w != "hello" {
		t.Errorf("Word(en) = %q, %v; want hello, true", w, ok)
	}
	if _, ok := Word("xx"); ok {
		t.Error("Word(xx) is known; want unknown")
	}
}
EOF
  for pair in cs:ahoj cy:helo da:hej de:hallo en:hello eo:saluton es:hola et:tere eu:kaixo \
    fi:hei fr:bonjour ga:dia hr:bok hu:szia id:halo is:hallo it:ciao lt:labas lv:sveiki \
    mt:bongu nl:hallo no:hei pl:czesc pt:ola ro:salut sk:ahoj sv:hej; do
    printf 'package lang\n\nfunc init() { register("%s", "%s") }\n' "${pair%%:*}" "${pair#*:}" >"internal/lang/${pair%%:*}.go"
  done
  untrimmed_greet
  git add internal
  git commit -qm "lang: greeting words for 27 languages"
}

# seeded_bug stores, as fixed, the bug the reviewer rows' regression brings back.
seeded_bug() {
  rec bug --id BUG-001 --status fixed --title "Bug: a padded name was greeted with its spaces" <<'EOF'
# BUG-001: a padded name was greeted with its spaces

Symptom: `GET /hello?name=%20bob%20` answered `hello,  bob ` and a blank name
answered `hello,    ` instead of `hello, world`.

Root cause: `Hello` in `internal/greet/greet.go` used the name as given.

Fix: `strings.TrimSpace` before the empty check; `TestHello` pins the padded
and the blank rows.

Prevention rule: a change to `internal/greet/greet.go` keeps the trim.
EOF
}

# work_wave leaves one fyr work wave finished: the plan, the worker's branch,
# its handoff record, a bug draft and the verb's printed result.
work_wave() {
  local stamp=2026-11-02-1200 name=greet-tab-p1
  local drafts=".agents/scratch/work/$stamp/$name"
  rec plan --id PLAN-2026-11-02-greet-tab --title "Greet: a tab-only name" <<'EOF'
# PLAN-2026-11-02-greet-tab

## Phase 1 — a tab-only name greets the world

files: internal/greet/greet_test.go

- [ ] A table test pins that a tab-only name greets the world, who: AI
- [ ] Checkpoint — expect: `go test ./...` passes with TestHelloTab
EOF
  git checkout -qb "work/$name"
  cat >>internal/greet/greet_test.go <<'EOF'

func TestHelloTab(t *testing.T) {
	if got := Hello("\t"); got != "hello, world" {
		t.Errorf("Hello(tab) = %q, want hello, world", got)
	}
}
EOF
  git commit -qam "greet: pin a tab-only name (PLAN-2026-11-02-greet-tab p1)"
  git checkout -q main
  rec handoff --id "HANDOFF-2026-11-02-1210-$name" --title "Worker $name: TestHelloTab added" <<'EOF'
# Handoff: greet-tab p1

## Completed

- TestHelloTab added in internal/greet/greet_test.go; go test ./... passes (2 tests).

## Next Steps

- None.
EOF
  mkdir -p "$drafts"
  cat >"$drafts/bug-tab.md" <<'EOF'
# Bug: Hello accepted a tab-only name

Symptom: before the trim, Hello("\t") greeted a tab. Fixed by the trim;
TestHelloTab pins it.
EOF
  cat >.agents/scratch/work/result.txt <<EOF
work: stamp $stamp  1 workers  $PWD/.agents/scratch/work/$stamp/  runner host (--runner host)
work: $name  PASS  agy exit 0  412s  model default  work/$name $(git rev-parse --short=7 main)..$(git rev-parse --short=7 "work/$name") (1 commits)  runner host (--runner host)
work: $name: handoff HANDOFF-2026-11-02-1210-$name
work: $name | ## Next Steps
work: $name | - None.
work: $name: drafts $drafts (1 files)
EOF
}

# stand_in_verb writes fake-work.sh, the stand-in for fyr work that the
# work-plan and task rows run, where the scratch keeps it out of every commit.
stand_in_verb() {
  mkdir -p .agents/scratch/eval
  printf '%s\n' "$1" >.agents/scratch/eval/fake-work.sh
}

# work_plans seeds three plans with no work/* branches: one ready to start
# (cold-read box ticked, workers D-row yes), one with its cold-read box open,
# and one with no workers D-row.
work_plans() {
  local phases
  phases=$(cat <<'EOF'
### Phase 1 — a tab-only name greets the world

files: internal/greet/greet_test.go

- [ ] A table test TestHelloTab pins that a tab-only name greets the world — verify: unit, `go test ./internal/greet`, who: AI — expect: TestHelloTab passes
- [ ] Checkpoint — expect: `go test ./...` passes with TestHelloTab in internal/greet

### Phase 2 — add the shout package (after: none)

files: internal/shout/

- [ ] Add package internal/shout with Shout(s string) string and TestShout — verify: unit, `go test ./internal/shout`, who: AI — expect: TestShout passes
- [ ] Checkpoint — expect: `go test ./...` passes with Shout and TestShout in internal/shout
EOF
  )
  rec plan --id PLAN-2026-11-02-greetings --title "Greet: tab-only name and shout package" <<EOF
# PLAN-2026-11-02-greetings

## Phases

- [x] Cold-reader check — one report (REVIEW-2026-11-02-cold-greetings), 0 gaps, 0 blocking questions; design ready, implementation ready

$phases

## Decisions

D1 — 2026-11-02 — workers: yes, Phase 1 and Phase 2 have only who: AI boxes, depend on no Open: row, and have disjoint files: lines — by: planner
EOF
  rec plan --id PLAN-2026-11-02-greet-alpha --title "Greet: tab-only name and shout package (alpha)" <<EOF
# PLAN-2026-11-02-greet-alpha

## Phases

- [ ] Cold-reader check — one report, every item answered

$phases

## Decisions

D1 — 2026-11-02 — workers: yes, Phase 1 and Phase 2 have only who: AI boxes, depend on no Open: row, and have disjoint files: lines — by: planner
EOF
  rec plan --id PLAN-2026-11-02-greet-beta --title "Greet: tab-only name and shout package (beta)" <<EOF
# PLAN-2026-11-02-greet-beta

## Phases

- [x] Cold-reader check — one report (REVIEW-2026-11-02-cold-beta), 0 gaps, 0 blocking questions; design ready, implementation ready

$phases

## Decisions

D1 — 2026-11-02 — Greeting prefix stays lower-case — by: cold read round 1
EOF
}

# feedback_trio stores three global corrections about this project.
feedback_trio() {
  local what="$1" i
  for i in 1 2 3; do
    "$FYR" record add feedback --global --project "$PWD" --title "Use log/slog, not fmt.Println, for server logs ($what $i)" --home "$SB" >/dev/null <<EOF
The operator corrected the agent ($what, time $i): server logs go through
log/slog as JSON; fmt.Println is never used for logging in cmd/server.
EOF
  done
}

# memory_trio stores three memories, two of them saying the same thing.
memory_trio() {
  printf 'The demo ships on Fridays.\n' |
    "$FYR" memory add --type project --name ship-day --description "The demo ships on Fridays" --home "$SB" >/dev/null
  printf 'Releases of the demo go out on Friday.\n' |
    "$FYR" memory add --type project --name release-day --description "Demo releases go out on Friday" --home "$SB" >/dev/null
  printf 'The operator wants table tests for every function.\n' |
    "$FYR" memory add --type feedback --name table-tests --description "Table tests for every function" --home "$SB" >/dev/null
}

# preflight_tag tags the first release and commits one change after it.
preflight_tag() {
  git tag -a v0.1.0 -m "v0.1.0"
  cat >>README.md <<'EOF'

`GET /healthz` is for a load balancer's health check.
EOF
  git commit -qam "readme: say what healthz is for"
}

# reviewer_cold writes a draft plan with three seeded high-severity flaws:
# (1) a contradiction with PRD.md Non-goals (Bearer auth) and JSON vs plain-text
# return type between Interfaces and Phase 1; (2) a missing verification on
# Phase 1's task; (3) an unowned step (missing who:) on Phase 2's task.
reviewer_cold() {
  cat >.agents/scratch/plan-farewell.md <<'EOF'
# Plan — Farewell endpoint

**Date:** 2026-11-02 · **Status:** draft

## Why

Add `GET /goodbye?name=N` to `cmd/server/main.go` backed by `internal/farewell`, answering `goodbye, N` (or `goodbye, world` when `N` is blank).

## Surfaces touched

- `internal/farewell/farewell.go` (new)
- `cmd/server/main.go:73`

## Interfaces

- `Farewell(name string) string` in `internal/farewell` returns JSON `{"farewell":"goodbye, N"}`.
- `GET /goodbye?name=N` requires an `Authorization: Bearer <token>` header (`PRD.md` R1) and writes `Farewell(name)`.

## Phases

- [ ] Cold-reader check — one report, every item answered

### Phase 1 — Farewell package
files: internal/farewell/

- [ ] Add `Farewell(name string) string` in `internal/farewell/farewell.go` returning plain text `"goodbye, " + name`, who: AI
- [ ] Checkpoint — verify: `go vet ./...`, who: AI — expect: exit 0

### Phase 2 — Server route
files: cmd/server/main.go, README.md
after: Phase 1

- [ ] Wire `GET /goodbye` into `cmd/server/main.go` and document it in `README.md` — verify: `go test ./...`, expect: exit 0
- [ ] Checkpoint — verify: `gofmt -l . && go vet ./... && go test ./...`, who: AI — expect: exit 0
EOF
}

# resume_seed seeds an open plan (Phase 1 ticked, Phase 2 open), a handoff,
# TODO.md Now, a change record, and a project memory for fyr resume.
resume_seed() {
  cat >TODO.md <<'EOF'
# TODO

## Now

- [ ] Phase 2 of `PLAN-2026-11-02-farewell`: wire `GET /goodbye` into `cmd/server/main.go`

## Next

## Backlog
EOF
  git commit -qam "todo: phase 2 of PLAN-2026-11-02-farewell"
  rec plan --id PLAN-2026-11-02-farewell --title "Plan — Farewell endpoint" <<'EOF'
# PLAN-2026-11-02-farewell

## Phases

- [x] Cold-reader check — one report (REVIEW-2026-11-02-cold-farewell), 0 gaps, 0 blocking questions; design ready, implementation ready

### Phase 1 — Farewell package
files: internal/greet/

- [x] Add `Farewell(name string) string` to `internal/greet/greet.go` — verify: `go test ./...`, who: AI — expect: exit 0
- [x] Checkpoint — verify: `gofmt -l . && go vet ./... && go test ./...`, who: AI — expect: exit 0 — evidence: `go test ./...` ok

### Phase 2 — Server route
files: cmd/server/main.go

- [ ] Wire `GET /goodbye` into `cmd/server/main.go` — verify: `go test ./...`, who: AI — expect: exit 0
- [ ] Checkpoint — verify: `gofmt -l . && go vet ./... && go test ./...`, who: AI — expect: exit 0
EOF
  rec change --id CHANGE-2026-11-02-1155-phase1-farewell --title "Change — Phase 1 farewell helper" <<'EOF'
# Change — Phase 1 farewell helper

Completed Phase 1 of `PLAN-2026-11-02-farewell`.
EOF
  rec handoff --id HANDOFF-2026-11-02-1200-phase1-done --title "Handoff — 2026-11-02 — Phase 1 done, Phase 2 next" <<'EOF'
# Handoff — 2026-11-02 — Phase 1 done, Phase 2 next

## Summary

Phase 1 of `PLAN-2026-11-02-farewell` is complete. Next is Phase 2: wire `GET /goodbye` into `cmd/server/main.go`.
EOF
  printf 'The local demo server listens on port 8080 by default.\n' |
    "$FYR" memory add --type project --name goodbye-port --description "Local demo server port 8080" --home "$SB" >/dev/null
}

main "$@"
