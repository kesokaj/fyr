# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 2 — Universal CLI Ergonomics (`-C` / `--dir`, `--limit`, `--json`) & 1-Call Stdin/JSON Agent Protocol (`PLAN-2026-11-09-0000-plan-v8-0-0-cli-first-architecture-drop-fyr-serv`)

- [ ] Add `--dir <DIR>` and `-C <DIR>` to `newCtx` in `cmd/fyr/ctx_verbs.go`, implement `c.projectDir()`, wire across project verbs (`record`, `memory`, `resume`, `search`, `index`, `check`, `git-check`, `work`, `dream`), and add unit tests in `cmd/fyr/flags_test.go`.
- [ ] Create `internal/brain/stdin_doc.go` and `internal/brain/stdin_doc_test.go` (`ParseRecordStdin`, `ParseMemoryStdin`, `--json-in`), wire into `cmd/fyr/record.go` (`recordAdd`, `recordEdit`) and `cmd/fyr/verb_search_memory.go` (`runMemory`), and add CLI tests in `cmd/fyr/record_test.go` and `cmd/fyr/memory_test.go`.
- [ ] Add `--limit N` to `fyr record list` and `--json` to `fyr record list`, `fyr record get`, `fyr memory show`, `fyr status`, `fyr search`, and `fyr work --status`.
- [ ] Update `skills/work/SKILL.md`, `skills/project-memory/SKILL.md`, `context/AGENTS.md`, `AGENTS.md`, and `SPEC-fyr-record` to document 1-call `<<'EOF'` `stdin` headers, `--json-in`, `-C`/`--dir`, `--limit`, and `--json`.
- [ ] Run `gofmt -l . && go vet ./... && go test -race ./...` and verify `fyr check`.
- [ ] Checkpoint

## Next

- Phase 3 — CLI Trajectory Mapping (`fyr metrics trajectory`) & Live Antigravity `/work` Sidepanel (`--status-md`) (`PLAN-2026-11-09-0000-plan-v8-0-0-cli-first-architecture-drop-fyr-serv`)

## Backlog

- 2026-11-08 — Update stored trail records `GUIDE-onboarding` (lines 24, 50, 65: `search.db` schema `"4"`, AST graph / hooks / vetter surfaces, `fyr search --slice`), `SPEC-fyr-metrics` (lines 289, 353: `"r:"`/`"f:"` `steps.cmd_hash` read prefixes and `RedundantReads`/`RangedReadPct`), and `DEPLOY-remote` (line 4: clarify `no hooks` as deploy/git hooks alongside `ADR-511`) (`REVIEW-2026-11-08-1757-review-worktree-code-docs-2026-11-08`, `PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` → T8, T10)
- 2026-11-08 — Update `TDD.md:72-73` (`T6`) to name `~/.gemini/config/hooks.json` alongside the four installed customization trees (`REVIEW-2026-11-08-2349-review-v7-3-0-ef163c23-code-and-docs-2026-11-08` → T6)
