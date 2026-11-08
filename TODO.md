# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 1 — Drop `fyr serve` Cleanly (`-3,507` LOC) (`PLAN-2026-11-09-0000-plan-v8-0-0-cli-first-architecture-drop-fyr-serv`)

- [ ] Record new ADR superseding `ADR-075` and edit `SPEC-fyr-serve` status to `retired`.
- [ ] Run `git ls-files internal/serve cmd/fyr/*serve*` and `git rm` all 24 files by name.
- [ ] Remove `fyr serve` from `cmd/fyr/main.go`, `cmd/fyr/help_text.go`, `cmd/fyr/realhome_guard_test.go`, `cmd/fyr/verb_metrics.go`, `internal/metrics/evals_write.go`, `SDD.md` (`M16`), `TDD.md` (`T16`), `AGENTS.md`, `context/AGENTS.md`, and `README.md`.
- [ ] Run `gofmt -l . && go vet ./... && go test -race ./...` and `go run ./cmd/fyr check --home ~/.config/fyr/dev/check`.
- [ ] Checkpoint

## Next

- Phase 2 — Universal CLI Ergonomics (`-C` / `--dir`, `--limit`, `--json`) & 1-Call Stdin/JSON Agent Protocol (`PLAN-2026-11-09-0000-plan-v8-0-0-cli-first-architecture-drop-fyr-serv`)
- Phase 3 — CLI Trajectory Mapping (`fyr metrics trajectory`) & Live Antigravity `/work` Sidepanel (`--status-md`) (`PLAN-2026-11-09-0000-plan-v8-0-0-cli-first-architecture-drop-fyr-serv`)

## Backlog

- 2026-11-08 — Update stored trail records `GUIDE-onboarding` (lines 24, 50, 65: `search.db` schema `"4"`, AST graph / hooks / vetter surfaces, `fyr search --slice`), `SPEC-fyr-metrics` (lines 289, 353: `"r:"`/`"f:"` `steps.cmd_hash` read prefixes and `RedundantReads`/`RangedReadPct`), and `DEPLOY-remote` (line 4: clarify `no hooks` as deploy/git hooks alongside `ADR-511`) (`REVIEW-2026-11-08-1757-review-worktree-code-docs-2026-11-08`, `PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` → T8, T10)
- 2026-11-08 — Update `TDD.md:72-73` (`T6`) to name `~/.gemini/config/hooks.json` alongside the four installed customization trees (`REVIEW-2026-11-08-2349-review-v7-3-0-ef163c23-code-and-docs-2026-11-08` → T6)
