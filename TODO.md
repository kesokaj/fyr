# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 6b — Deletion (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)
files: `internal/work/`, `internal/sandbox/`, `cmd/fyr/verb_work*`, `docker/`, `embed.go`, `internal/auto/rubric.go`, `skills/fyr-work/`, `skills/fyr-auto/`, `PRD.md` enforced-by lists (pre-approved in Phase 0)
- [ ] Confirm nothing imports the packages anymore (safety rule 3), then delete them. Remove `all:docker` from `embed.go`. Update `rubric.go` `deniedRoots` and `TestRubricMatchesSkill` — verify: `go build ./... && go test -race ./...`, `TestEmbedHoldsOnlyTrackedFiles`, `fyr check`
- [ ] Checkpoint — evidence: `git diff --stat` shows deletions only in the listed paths

## Next

- Phase 7 — Release `v9.0.0` (`PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`)

## Backlog

- 2026-11-10: Optional scheduled HTML report generation in `fyr auto` (source: `PLAN-2026-11-10-1540-plan-v8-5-0-airy-ux-second-brain-metrics-fyr-rep` Follow-ups).
