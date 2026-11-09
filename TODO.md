# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 4 — Indexing Lifecycle (`/fyr-init-project`, `/fyr-index-project`, `/fyr-refresh-project-index`) & `fyr-*` Prefix for All `fyr`-Vital Skills (`PLAN-2026-11-09-0000-plan-v8-0-0-cli-first-architecture-drop-fyr-serv`, after: Phase 3)
- [x] Record `ADR-514` & update `PRD.md` (lines 84, 91, 114) + `TODO.md` in one commit.
- [ ] Rename all 18 existing `fyr`-vital skills to `fyr-<skill>` (`git mv`) and update frontmatter, `/fyr-<name>` slash invocations, and `~/.gemini/config/skills/fyr-<name>/` paths across skills and agents.
- [ ] Implement the 3-skill project indexing lifecycle (`skills/fyr-init-project/SKILL.md`, `skills/fyr-index-project/SKILL.md`, `skills/fyr-refresh-project-index/SKILL.md`).
- [ ] Update Go callers, hook banner (`internal/search/hook.go`), vetter (`internal/search/vet.go`), `internal/auto/*`, test/eval pins (`init_project_test.go`, `skills_text_test.go`, `areas_test.go`, `cmd/fyr/record_body_file_test.go`, `cmd/fyr/testdata/recall.tsv`, `internal/eval/*`, `.agents/eval/GUIDE-eval.md`), `context/AGENTS.md`, `AGENTS.md`, `README.md`, and `rules/session-sizing.md`.
- [ ] Run verification (`gofmt -l . && go vet ./... && go test -race ./...`, `fyr check`) and full Definition of Done (`verifier`, `reviewer --code --docs`, `security`, `testing`/`review`/`change` records, close plan, clear `TODO.md` `## Now`, and `/fyr-handoff`).
- [ ] Checkpoint

## Next

## Backlog

- 2026-11-08 — Update stored trail records `GUIDE-onboarding` (lines 24, 50, 65: `search.db` schema `"4"`, AST graph / hooks / vetter surfaces, `fyr search --slice`), `SPEC-fyr-metrics` (lines 289, 353: `"r:"`/`"f:"` `steps.cmd_hash` read prefixes and `RedundantReads`/`RangedReadPct`), and `DEPLOY-remote` (line 4: clarify `no hooks` as deploy/git hooks alongside `ADR-511`) (`REVIEW-2026-11-08-1757-review-worktree-code-docs-2026-11-08`, `PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` → T8, T10)
- 2026-11-08 — Update `TDD.md:72-73` (`T6`) to name `~/.gemini/config/hooks.json` alongside the four installed customization trees (`REVIEW-2026-11-08-2349-review-v7-3-0-ef163c23-code-and-docs-2026-11-08` → T6)
