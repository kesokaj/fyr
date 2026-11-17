# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Wave 2 — Parallel Hook & Dream Integration (`PLAN-2026-11-17-1413-plan-unified-pre-hook-coordinator-worker-jit-cap`)
#### Phase 2A — `PreToolUse` Worker Capsule Overwrite & Coordinator Write Guard
- [ ] Pass `Options` to `handlePreToolUse` (`internal/search/{hook_deadline,hook}.go`), implement `[FYR_WORKER_CAPSULE]` JIT overwrite for `invoke_subagent` / `swarm add` (`AND` gating, `<= 1,200 B`, `TestPreToolSubagentAndSwarmCapsuleOverwrite`), and preserve `hook_workflow.go` literal safety
- [ ] Add `[COORDINATOR_GUARD]` write guard in `handlePreToolWrite` (`internal/search/hook.go`, `TestCoordinatorWriteGuardInHook`) blocking main-checkout Go edits (`COORDINATE_SUBTASKS` / `subtask_count >= 2` without `!` bypass) while allowing `.md`/docs, `.agents/`, linked worktrees, and Worker Role Exemptions

#### Phase 2B — Registration-Free Dream Rating & Harness Proposal Queue
- [ ] Drop `trail.Registered` (`G6`) and `migrated` (`noTrail`) gates in `internal/dream/{targets,auto_reads,dream}.go` and support non-git `--project` and `search.GlobalWorkspace`
- [ ] Implement sliding-window `(since, cutoff]` watermarks (`DreamWindow`), stale conversation rating via `AgyConversationTextInWindow`, `WriteRatings` persistence in `metrics.db`, and `target: harness` proposal queueing

## Next

- Wave 3 (Phase 3A binary `fyr auto` on/off + Phase 3B hook docs & `< 200 ms` smoke) of `PLAN-2026-11-17-1413-plan-unified-pre-hook-coordinator-worker-jit-cap`
- Wave 4 (Phase 4 unified traces, docs, cross-family `reviewer` + `security` audits, `/home/kesokaj/.local/bin/fyr verify`, `change`, push & `fyr close`) of `PLAN-2026-11-17-1413-plan-unified-pre-hook-coordinator-worker-jit-cap`
- Phase 7 box 2 of `PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`: 7-day post-tag `fyr report --md` retro against `RETRO-2026-11-15-0102-graph-a-b-baseline` (earliest 2026-11-23)

## Backlog

- 2026-11-10: Optional scheduled HTML report generation in `fyr auto` (source: `PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep` Follow-ups).
