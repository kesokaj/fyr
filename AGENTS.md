# AGENTS.md — fyr

Project rules (override `context/`).

## What this repo is

The agent harness (`context/`, `rules/`, `skills/`, `agents/`) and `fyr` binary deploying it to Antigravity CLI with `< 250 ms` hooks (`~/.gemini/config/hooks.json`, `R3`, `ADR-511`), second brain, and build control (`PRD.md` R0); delivery claim in `SPEC-fyr-delivery` § The model. Details in `.agents/rules/{delivery-map,customizations-and-redact,eval-and-release}.md`.

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

```
PRD.md SDD.md TDD.md TODO.md README.md   reqs, arch, design, queue, map
context/, rules/, commands/, agents/     context, rules, empty cmds, <name>/agent.md (ADR-062)
skills/<name>/, embed.go, *_test.go      embedded skills (`.agents/skills` links) & repo tests
cmd/fyr, internal/                       CLI & packages (`SDD.md` § Components, `TDD.md`)
.agents/rules/, .agents/eval/            repo rules & `fyr eval` (`GUIDE-eval.md`, `fixture.sh`)
.agents/scratch/                         gitignored temp/generated files, never /tmp
```

Trail records live in `~/.config/fyr/brain.db` (`fyr record export` writes `.agents/trail.jsonl`).

## Rules for working here

- **Scratch `--home` always:** Never run `fyr install` or state-writing verbs (`record`, `memory add`, `index`, `search`, `metrics ingest`, `dream`, `eval`, `auto`) without `--home .agents/scratch/<name>` (`ADR-505` `cmd/fyr/realhome_guard.go`; tests never enable real-home `fyr auto`/timers). Exceptions: `fyr eval` (`~/.config/fyr/dev/fyr-eval`), approved `fyr dream` (`GUIDE-testing` § 3e, § 4), and `DEPLOY-local` § Operator steps.
- **Dependencies:** Stdlib only in Go, except `modernc.org/sqlite` in `internal/search`, `internal/metrics`, `internal/brain` (`R4`, `ADR-045`, `ADR-047a`, `ADR-055`, `ADR-063`; never installer). Other deps require `PRD.md` change.
- **Map, customizations, eval & release:** See `.agents/rules/delivery-map.md` (`TestHarnessMap`, `SPEC-fyr-delivery`), `.agents/rules/customizations-and-redact.md` (`knownTools`, frontmatter, `VENDORED.md`, `redact.go`), `.agents/rules/eval-and-release.md` (`GUIDE-testing`, `fyr eval`, `/fyr-preflight vX.Y.Z` `ADR-053`).
- **Git:** Commit and push on `main` after each task.
