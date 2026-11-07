#!/usr/bin/env bash
# fixture-extra.sh: scenario helpers sourced by .agents/eval/fixture.sh before
# demo_tree wipes the working tree, keeping each file well under 400 lines.

# reviewer_wide commits the regression inside a 30-file change across two
# directories plus README.md at the repository root, under a subject that does
# not name the regression: a registry of greeting words in internal/lang, its
# test, 26 generated language files, internal/greet/greet.go, and README.md.
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
    mt:bongu nl:hallo no:hei pl:czesc pt:ola ro:salut sk:ahoj; do
    printf 'package lang\n\nfunc init() { register("%s", "%s") }\n' "${pair%%:*}" "${pair#*:}" >"internal/lang/${pair%%:*}.go"
  done
  cat >>README.md <<'EOF'

Languages: see `internal/lang` for ISO 639-1 greeting words.
EOF
  untrimmed_greet
  git add README.md internal
  git commit -qm "lang: greeting words for 26 languages"
  verifier_seed
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

# security_seed seeds BUG-001 and commits an unvalidated ADMIN_TOKEN /
# X-Admin-Token authentication bypass in cmd/server/main.go (when ADMIN_TOKEN is
# unset in the environment, r.Header.Get("X-Admin-Token") == adminToken matches
# "" == "") alongside the untrimmed internal/greet/greet.go.
security_seed() {
  seeded_bug
  untrimmed_greet
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
	adminToken := os.Getenv("ADMIN_TOKEN")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Admin-Token") == adminToken {
			fmt.Fprintln(w, "admin:"+greet.Hello(r.URL.Query().Get("name")))
			return
		}
		fmt.Fprintln(w, greet.Hello(r.URL.Query().Get("name")))
	})
	slog.Info("listening", "port", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		slog.Error("serve", "err", err)
		os.Exit(1)
	}
}
EOF
  git commit -qam "server: add admin greeting header and update greet"
}

# verifier_seed writes a pre-existing scratch home canary so the verifier row
# checks that existing scratch directories survive verification (BUG-207).
verifier_seed() {
  mkdir -p .agents/scratch/keep-home
  printf 'keep\n' >.agents/scratch/keep-home/canary.txt
}

# work_wave leaves one fyr work wave finished with two workers: greet-tab-p1
# (Phase 1, clean records delivered as files under records/, ADR-501) and
# greet-pad-p2 (Phase 2, whose delivered change record reuses greet-tab-p1's
# CHANGE-2026-11-02-1210-greet-tab-p1 ID, triggering the duplicate-ID guard).
work_wave() {
  local stamp=2026-11-02-1200 name1=greet-tab-p1 name2=greet-pad-p2
  local drafts1=".agents/scratch/work/$stamp/$name1"
  local recs1=".agents/scratch/work/$stamp/$name1/records/"
  local recs2=".agents/scratch/work/$stamp/$name2/records/"
  rec plan --id PLAN-2026-11-02-greet-tab --title "Greet: a tab-only name and README note" <<'EOF'
# PLAN-2026-11-02-greet-tab

## Phase 1 — a tab-only name greets the world

files: internal/greet/greet_test.go

- [ ] A table test pins that a tab-only name greets the world, who: AI
- [ ] Checkpoint — expect: `go test ./...` passes with TestHelloTab

## Phase 2 — document padding in README (after: none)

files: README.md

- [ ] Document padding behaviour in README.md, who: AI
- [ ] Checkpoint — expect: README.md mentions padding
EOF
  git checkout -qb "work/$name1"
  cat >>internal/greet/greet_test.go <<'EOF'

func TestHelloTab(t *testing.T) {
	if got := Hello("\t"); got != "hello, world" {
		t.Errorf("Hello(tab) = %q, want hello, world", got)
	}
}
EOF
  git commit -qam "greet: pin a tab-only name (PLAN-2026-11-02-greet-tab p1)"
  git checkout -q main
  git checkout -qb "work/$name2"
  cat >>README.md <<'EOF'

Names are trimmed of leading and trailing whitespace.
EOF
  git commit -qam "readme: document padding (PLAN-2026-11-02-greet-tab p2)"
  git checkout -q main
  mkdir -p "$drafts1" "$recs1" "$recs2"
  printf 'Worker %s: TestHelloTab added\n' "$name1" >"${recs1}HANDOFF-2026-11-02-1210-$name1.title"
  cat >"${recs1}HANDOFF-2026-11-02-1210-$name1.md" <<'EOF'
# Handoff: greet-tab p1

## Completed

- TestHelloTab added in internal/greet/greet_test.go; go test ./... passes (2 tests).

## Next Steps

- None.
EOF
  printf 'Change %s: TestHelloTab in internal/greet\n' "$name1" >"${recs1}CHANGE-2026-11-02-1210-$name1.title"
  cat >"${recs1}CHANGE-2026-11-02-1210-$name1.md" <<'EOF'
# Change: greet-tab p1

Added TestHelloTab in internal/greet/greet_test.go.
EOF
  printf 'Worker %s: README padding note\n' "$name2" >"${recs2}HANDOFF-2026-11-02-1210-$name2.title"
  cat >"${recs2}HANDOFF-2026-11-02-1210-$name2.md" <<'EOF'
# Handoff: greet-tab p2

## Completed

- Documented padding behaviour in README.md.

## Next Steps

- None.
EOF
  printf 'Change %s: duplicate ID collision\n' "$name2" >"${recs2}CHANGE-2026-11-02-1210-$name1.title"
  cat >"${recs2}CHANGE-2026-11-02-1210-$name1.md" <<'EOF'
# Change: greet-tab p2

Collides with Phase 1's CHANGE-2026-11-02-1210-greet-tab-p1 ID.
EOF
  cat >"$drafts1/bug-tab.md" <<'EOF'
# Bug: Hello accepted a tab-only name

Symptom: before the trim, Hello("\t") greeted a tab. Fixed by the trim;
TestHelloTab pins it.
EOF
  cat >.agents/scratch/work/result.txt <<EOF
work: stamp $stamp  2 workers  $PWD/.agents/scratch/work/$stamp/  runner host (--runner host)
work: $name1  PASS  agy exit 0  412s  model default  work/$name1 $(git rev-parse --short=7 main)..$(git rev-parse --short=7 "work/$name1") (1 commits)  runner host (--runner host)
work: $name1: handoff HANDOFF-2026-11-02-1210-$name1
work: $name1 | ## Completed
work: $name1 | - TestHelloTab added in internal/greet/greet_test.go; go test ./... passes (2 tests).
work: $name1 | ## Next Steps
work: $name1 | - None.
work: $name1: change CHANGE-2026-11-02-1210-$name1  Change $name1: TestHelloTab in internal/greet
work: $name1: records $recs1 (2 records)
work: $name1: drafts $drafts1 (1 files)
work: $name2  PASS  agy exit 0  380s  model default  work/$name2 $(git rev-parse --short=7 main)..$(git rev-parse --short=7 "work/$name2") (1 commits)  runner host (--runner host)
work: $name2: handoff HANDOFF-2026-11-02-1210-$name2
work: $name2 | ## Completed
work: $name2 | - Documented padding behaviour in README.md.
work: $name2 | ## Next Steps
work: $name2 | - None.
work: $name2: change CHANGE-2026-11-02-1210-$name1  Change $name2: duplicate ID collision
work: $name2: records $recs2 (2 records)
EOF
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

# work_plan_host seeds work_plans and configures fake-work.sh to report a host
# fallback reason so /work PLAN-ID must wait for the operator's go.
work_plan_host() {
  stand_in_verb "$1"
  work_plans
  printf 'colima is not running\n' >.agents/scratch/eval/work-host-reason
}

# reviewer_cold writes a draft plan with three seeded high-severity flaws.
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
