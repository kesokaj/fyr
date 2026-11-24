# AGENTS.md

<!-- Track 2: 100% agent-read (Telegraphic Markdown, <= 4,500 B). Humans: read README.md.
     Keep non-discoverable operational commands, constraints, and pointers only.
     Forbidden here: human onboarding prose, directory-tree dumps, and restating source layout. -->

> Audience: 100% agent-read (Track 2, Telegraphic Markdown, <= 4,500 B). Humans: see `README.md`.

## Scope

<!-- One line: what this repo builds/serves and non-obvious domain boundary. Never write human onboarding prose. -->

## Setup commands

<!-- Non-discoverable build, run, test, and verify commands only. Delete unconfirmed lines.
     Extended tiers/e2e live in GUIDE-testing (`fyr record get GUIDE-testing`). -->

```bash
go mod download          # deps
go build ./cmd/...       # build
go run ./cmd/server/     # run
go test -race ./...      # test
go vet ./...             # static analysis
fyr verify               # format, vet, race test, fyr check -> .agents/scratch/verify.stamp
```

<!-- Standalone CLI / non-Go / TS app: swap in native entry point and test/lint commands; close tasks with `fyr record add handoff` (`fyr close` requires Go verify.stamp). -->

## Boundaries & overrides

<!-- Only rules that DIFFER from global context (`~/.gemini/config/rules/AGENTS.md`). Delete if none.
     Never duplicate global rules, write human onboarding prose, or paste directory-tree dumps. -->

## Deploy

<!-- One line per active target; procedures live in DEPLOY-local, DEPLOY-remote, DEPLOY-gcp. -->

Local only: `fyr record get DEPLOY-local` (remote: `DEPLOY-remote`; GCP defaults: `DEPLOY-gcp`).

## Pointers

- Contracts (Track 3): `PRD.md` (requirements), `TODO.md` (active queue), `fyr record list --kind spec` (`SPEC-*`).
- Human docs (Track 1): `README.md`, `SDD.md`, `TDD.md`, `GUIDE-testing`, `DEPLOY-*`.
- Trail & code discovery: `fyr record list --kind adr`, `fyr search --slice` / `--contracts` / `--skeleton` / `--refs`; scratch under `.agents/scratch/` (never `/tmp`).
