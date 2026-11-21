# AGENTS.md — fyr

Project rules (override `context/`).

## What this repo is

The agent harness (`context/`, `rules/`, `skills/`, `agents/`) and `fyr` binary deploying it to Antigravity CLI: `< 250 ms` hooks (`~/.gemini/config/hooks.json`, `R3`, `ADR-511`), second brain, build control (`PRD.md` R0); `SPEC-fyr-delivery` § The model. Details: `.agents/rules/{delivery-map,customizations-and-redact,eval-and-release}.md`.

## Setup commands

```bash
gofmt -l cmd internal && go vet ./... && go test -race -timeout 30m ./... # auto: test
go run ./cmd/fyr install --home .agents/scratch/home   # scratch install
go run ./cmd/fyr list   # check skills & agents
go mod download   # offline cache (R4)
"$(go env GOPATH)/bin/govulncheck" ./...   # watch NEW (known: DEPLOY-local § Operator steps)
```

`context/*.md` and `rules/*.md` changes must pass `internal/harness/size_test.go` (`TDD.md` § T1); never raise limits. `go.mod` has NO `toolchain` directive (`ADR-029`).

## Layout

- Root docs: `PRD.md`, `SDD.md`, `TDD.md`, `TODO.md`, `README.md`
- Harness: `context/`, `rules/`, `commands/`, `agents/<name>/agent.md` (`ADR-062`), `skills/<name>/`, `embed.go`, `*_test.go` (`.agents/skills`)
- Code & local: `cmd/fyr`, `internal/` (`SDD.md` § Components, `TDD.md`), `.agents/{rules,eval}/` (`GUIDE-eval.md`, `fixture.sh`), `.agents/scratch/` (never `/tmp`)

Trail: `~/.config/fyr/brain.db` (`fyr record export` -> `.agents/trail.jsonl`).

## Rules for working here

- **Scratch `--home` always:** Pass `--home .agents/scratch/<name>` on `fyr install` and writes (`record`, `memory add`, `index`, `search`, `metrics ingest`, `dream`, `eval`, `auto`; `ADR-505` `cmd/fyr/realhome_guard.go`; tests skip real-home `auto`/timers). Exceptions: `fyr eval` (`~/.config/fyr/dev/fyr-eval`), approved `fyr dream` (`GUIDE-testing` § 3e, § 4), `DEPLOY-local` § Operator steps.
- **Dependencies:** Stdlib only except `modernc.org/sqlite` in `internal/search`, `internal/metrics`, `internal/brain` (`R4`, `ADR-045`, `ADR-047a`, `ADR-055`, `ADR-063`; never installer); others need `PRD.md` change.
- **Repo rules:** `.agents/rules/` — `delivery-map.md` (`TestHarnessMap`, `SPEC-fyr-delivery`), `customizations-and-redact.md` (`knownTools`, frontmatter, `VENDORED.md`, `redact.go`), `eval-and-release.md` (`GUIDE-testing`, `fyr eval`, `/fyr-preflight vX.Y.Z` `ADR-053`).
- **Git:** Commit and push on `main` per task.
