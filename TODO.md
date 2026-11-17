# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 1a — `metrics.db` Schema v8 & All-Conversation Workspace Discovery (`PLAN-2026-11-17-1331-plan-global-on-off-fyr-auto-across-all-conversat`)
- [ ] Add `metrics.db` migration 8 (`session_ratings` table), `UpsertSessionRatings`/`SessionRatings` helpers (`internal/metrics/ratings.go`), and rating summary fields in `internal/metrics/` (`Summary.A` in `show_types.go`, `report_md.go`, `report.go`)
- [ ] Export `GlobalWorkspace`, add `ListAgyWorkspaces`, `AgyConversationTextInWindow` (`maxModified` stale cutoff), and global/non-git workspace support in `internal/search/{dream,dream_workspace}.go` (`TestListAgyWorkspacesAndWindow`)

## Next

- Phase 1b — Registration-Free Dreaming & Stale-Conversation Rating (`PLAN-2026-11-17-1331-plan-global-on-off-fyr-auto-across-all-conversat`)
- Phase 2 — Unified Binary On/Off `fyr auto` Pipeline, CLI & Docs (`PLAN-2026-11-17-1331-plan-global-on-off-fyr-auto-across-all-conversat`)
- Phase 7 box 2 of `PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`: 7-day post-tag `fyr report --md` retro against `RETRO-2026-11-15-0102-graph-a-b-baseline` (earliest 2026-11-23)

## Backlog

- 2026-11-10: Optional scheduled HTML report generation in `fyr auto` (source: `PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep` Follow-ups).
