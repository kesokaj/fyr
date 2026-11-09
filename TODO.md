# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 3 — CLI Trajectory Mapping (`fyr metrics trajectory`) & Live Antigravity `/work` Sidepanel (`--status-md`) (`PLAN-2026-11-09-0000-plan-v8-0-0-cli-first-architecture-drop-fyr-serv`)

- [ ] Create `internal/metrics/trajectory.go` and `internal/metrics/trajectory_test.go` and wire `fyr metrics trajectory [<session-id>] [--project DIR] [--limit N] [--json]` into `cmd/fyr/verb_metrics.go` and `cmd/fyr/help_text.go`.
- [ ] Create `internal/sandbox/transcript_watch.go` and `internal/sandbox/transcript_watch_test.go` and wire `b.watchTranscript(ctx)` into `internal/sandbox/docker.go` (`dockerAgy`) and `internal/sandbox/session.go` (`hostCmd`).
- [ ] Create `internal/work/progress_md.go` and wire `--status-md <FILE>` into `internal/work/progress.go`, `internal/work/work.go`, `cmd/fyr/verb_work.go`, `cmd/fyr/help_text.go`, and `skills/work/SKILL.md`.
- [ ] Update `SDD.md`, `TDD.md`, and `SPEC-fyr-metrics`, then run full Definition of Done (`verifier`, `reviewer --code --docs`, `security`, `change` record, close plan, `TODO.md`, and `/handoff`).
- [ ] Checkpoint

## Next

## Backlog

- 2026-11-08 — Update stored trail records `GUIDE-onboarding` (lines 24, 50, 65: `search.db` schema `"4"`, AST graph / hooks / vetter surfaces, `fyr search --slice`), `SPEC-fyr-metrics` (lines 289, 353: `"r:"`/`"f:"` `steps.cmd_hash` read prefixes and `RedundantReads`/`RangedReadPct`), and `DEPLOY-remote` (line 4: clarify `no hooks` as deploy/git hooks alongside `ADR-511`) (`REVIEW-2026-11-08-1757-review-worktree-code-docs-2026-11-08`, `PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` → T8, T10)
- 2026-11-08 — Update `TDD.md:72-73` (`T6`) to name `~/.gemini/config/hooks.json` alongside the four installed customization trees (`REVIEW-2026-11-08-2349-review-v7-3-0-ef163c23-code-and-docs-2026-11-08` → T6)
