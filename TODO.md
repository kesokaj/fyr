# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Wave 1 — Parallel Core Foundations (`PLAN-2026-11-17-1413-plan-unified-pre-hook-coordinator-worker-jit-cap`)
#### Phase 1A — `PreInvocation` JIT `improvedPrompt`, `skillDigests`, and `DecomposeSubtasks`
- [ ] Add `SubtaskSlice`, `isWorkerBriefPrompt` (including `"This is a Kickoff written by fyr auto."`), and `DecomposeSubtasks` in `internal/search/vet_decompose.go`, wire `hasBatchOrMultiPhaseCues` in `internal/search/vet_route.go`, attach `Subtasks` in `vetPromptCoreWithHistory` (`internal/search/vet.go`), and add `TestDecomposeSubtasksClausesAndPackages` (`internal/search/vet_decompose_test.go`)
- [ ] Emit `improvedPrompt`, `skillDigests`, `subtasks`, and `directive: "COORDINATE_SUBTASKS"` in `internal/search/telemetry_json.go`, bump `schemaVersion` to `"7"` (`internal/search/store.go`, `internal/search/reconcile_test.go`) to persist `subtask_count` + `directive` in `vet_history` (`internal/search/vet_history.go`, `internal/search/process_artifact.go`, `internal/search/hook_dedup.go`), and add `TestPreInvocationEmitsImprovedPromptAndSkillDigests` (`internal/search/vet_history_test.go`)
#### Phase 1B — `metrics.db` Schema v8 (`session_ratings`) & All-Conversation Workspace Discovery
- [ ] Add `metrics.db` migration 8 (`session_ratings` table), `UpsertSessionRatings`/`SessionRatings` helpers (`internal/metrics/ratings.go`), and rating summary fields in `internal/metrics/` (`Summary.A` in `show_types.go`, `report_md.go`, `report.go`)
- [ ] Export `GlobalWorkspace`, add `ListAgyWorkspaces`, `AgyConversationTextInWindow` (`maxModified` stale cutoff), and global/non-git workspace support in `internal/search/{dream,dream_workspace}.go` (`TestListAgyWorkspacesAndWindow`)

## Next

- Wave 2 (Phase 2A `PreToolUse` JIT/guard + Phase 2B registration-free dream rating) of `PLAN-2026-11-17-1413-plan-unified-pre-hook-coordinator-worker-jit-cap`
- Wave 3–4 (Phase 3A binary `fyr auto` + Phase 3B hook docs/smoke -> Phase 4 unified traces, review, security & close) of `PLAN-2026-11-17-1413-plan-unified-pre-hook-coordinator-worker-jit-cap`
- Phase 7 box 2 of `PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`: 7-day post-tag `fyr report --md` retro against `RETRO-2026-11-15-0102-graph-a-b-baseline` (earliest 2026-11-23)

## Backlog

- 2026-11-10: Optional scheduled HTML report generation in `fyr auto` (source: `PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep` Follow-ups).
