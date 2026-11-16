# AGENTS.md — fyr

Project rules for this repo; override the global context sourced from `context/`.

## What this repo is

The agent harness: global context (agents.md standard), skills (`SKILL.md`), subagents, and `fyr`, the Go binary installing them into Antigravity CLI with `< 250 ms` fail-open CLI hooks (`~/.gemini/config/hooks.json`, `R3`, `ADR-511`). No daemons, no gates, no CI. `fyr` is also the CLI's second brain and build controller (`PRD.md` R0); the delivery claim lives once in `SPEC-fyr-delivery` § The model. Detailed repo rules live in `.agents/rules/{delivery-map,customizations-and-redact,eval-and-release}.md`.

## Setup commands

```bash
gofmt -l cmd internal && go vet ./... && go test -race -timeout 30m ./... # auto: test
go run ./cmd/fyr install --home .agents/scratch/home   # exercise installer in scratch home
go run ./cmd/fyr list   # sanity-check skills and agents parse
go mod download   # cache the one module (R4) for offline builds
"$(go env GOPATH)/bin/govulncheck" ./...   # watch for NEW findings (known: DEPLOY-local § Operator steps)
```

`context/*.md` and `rules/*.md` changes must pass `internal/harness/size_test.go` (`TDD.md` § T1); never raise a limit. `go.mod` carries NO `toolchain` directive (`ADR-029`).

## Layout

```
PRD.md SDD.md TDD.md            what/why (R#), architecture (M#), technical design (T#); TODO.md, README.md
context/, rules/, commands/     global context (`AGENTS.md` + `dev-setup.md`), rules/, empty commands/
agents/<name>/agent.md          subagents, installed as written (ADR-062)
skills/<name>/                  skills (`.agents/skills` links here); `google-catalog/catalog/` is routed, never installed
cmd/fyr, internal/              CLI binary and packages (`SDD.md` § Components, `TDD.md`)
embed.go, *_test.go             `go:embed` content and repo tests
.agents/rules/, .agents/eval/   repo model-decision rules and `fyr eval` suite (`GUIDE-eval.md`, `fixture.sh`)
.agents/scratch/                gitignored; every generated or temporary file, never /tmp
```

Trail records live in `~/.config/fyr/brain.db` (`fyr record export` writes `.agents/trail.jsonl` on demand).

## Rules for working here

- **Scratch `--home` always:** Never run `fyr install` against the real `$HOME` in development (`--home .agents/scratch/<name>`). Every state-writing verb (`record`, `memory add`, `index`, `search`, `metrics ingest`, `dream`, `eval`, `auto`) takes `--home .agents/scratch/<name>` in dev, Kickoffs and handoffs (`ADR-505` `cmd/fyr/realhome_guard.go` exits 2 otherwise; tests never enable `fyr auto` or its timer in real home). Two exceptions sit outside any checkout: `fyr eval` (`~/.config/fyr/dev/fyr-eval`) and an approved visible `fyr dream` run (`GUIDE-testing` § 3e, § 4). Real-home installs happen only per `DEPLOY-local` § Operator steps.
- **Dependencies:** Stdlib only in Go, except `modernc.org/sqlite` imported only by `internal/search`, `internal/metrics`, and `internal/brain` (`R4`, `ADR-045`, `ADR-047a`, `ADR-055`, `ADR-063`; never by the installer). Any other dependency requires a `PRD.md` change.
- **Map, customizations, eval & release:** Read `.agents/rules/delivery-map.md` for `README.md` § How it works rules (`TestHarnessMap`, `SPEC-fyr-delivery`), `.agents/rules/customizations-and-redact.md` for `knownTools`, frontmatter allowlists, `VENDORED.md`, and `redact.go`, and `.agents/rules/eval-and-release.md` for `GUIDE-testing`, `fyr eval`, and unsigned `/fyr-preflight vX.Y.Z` tags (`ADR-053`).
- **Git:** Commit and push on `main` after each task.
