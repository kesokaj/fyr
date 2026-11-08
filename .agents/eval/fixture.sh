#!/usr/bin/env bash
# fixture.sh <scenario>: turns a trial's clone of this checkout into the demo
# project a row of GUIDE-eval.md runs against. Run by a row's setup, in the
# clone, with $FYR (the fyr under test) and $SB (the trial's home) set by
# `fyr eval`. Every record it seeds goes to $SB's store, never the operator's.
# The whole body is read before it runs, because demo_tree removes .agents/eval
# from the work tree.
main() {
  set -euo pipefail
  : "${FYR:?FYR is the fyr under test}" "${SB:?SB is the trial home}"
  local scenario="${1:?usage: fixture.sh <scenario>}"
  case "$scenario" in
    bare | project | project-empty | project-ast | project-ast-nohooks | project-ast-inject | review | reviewer | reviewer-wide | reviewer-wide-bug | reviewer-cold | security | verifier | resume | work | work-plan | work-plan-host | tasks | retro | distill | dream | preflight) ;;
    *) echo "fixture.sh: unknown scenario $scenario" >&2; return 2 ;;
  esac
  source .agents/eval/fixture-extra.sh
  source .agents/eval/fixture-ast.sh
  # The stand-in verb is read before demo_tree empties the work tree.
  local fake=""
  case "$scenario" in tasks | work-plan | work-plan-host) fake=$(cat .agents/eval/fake-work.sh) ;; esac
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
    security) security_seed ;;
    verifier) verifier_seed ;;
    resume) resume_seed ;;
    work) work_wave ;;
    work-plan) stand_in_verb "$fake"; work_plans ;;
    work-plan-host) work_plan_host "$fake" ;;
    tasks) stand_in_verb "$fake" ;;
    retro) feedback_trio "a correction" ;;
    distill) feedback_trio "a repeated procedure" ;;
    dream) memory_trio ;;
    preflight) preflight_tag ;;
    project-ast) project_ast ;;
    project-ast-nohooks) project_ast_nohooks ;;
    project-ast-inject) project_ast_inject ;;
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

# stand_in_verb writes fake-work.sh, the stand-in for fyr work that the
# work-plan and task rows run, where the scratch keeps it out of every commit.
stand_in_verb() {
  mkdir -p .agents/scratch/eval
  printf '%s\n' "$1" >.agents/scratch/eval/fake-work.sh
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

main "$@"
