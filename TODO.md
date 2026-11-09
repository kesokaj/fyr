# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

## Next

## Backlog

- 2026-11-09: Fix BUG-525 (read_ledger clamp ignores read_file's 800-line cap; ByteOffset past the range records it all) via `/fyr-bug` (source: REVIEW-2026-11-09-2023-code-docs-review-plan-range-v8-1-0-head-phase-4).
- 2026-11-09: Fix BUG-526 (fixture.sh mock-* scenarios delete their working directory, `.git` included, unguarded) via `/fyr-bug` (source: same review).
- 2026-11-09: `TestSliceVsGrepTokenEfficiencyAndRecall` comment claims must-pass and must-fail states for `evalSymbolRecall` but checks one partial case; add the 1.0 and 0.0 cases or reword (source: same review, low).
- 2026-11-09: Optional live `GUIDE-eval.md` rows for the 3 mock scenarios (`scenario-ledger-mvp`, `scenario-worker-adr-fence`, `scenario-fleet-trajectory`), skipped from Phase 4 by the operator (source: PLAN-2026-11-09-1546-plan-end-to-end-feature-proof-matrix-3-mock-proj 4.1).
- 2026-11-09: `internal/auto/checks.go` `machineFindings` calls `Env.Usage` at `--level observe`, reporting `/usage unreadable` although G9 gates only above observe; unverified (source: HANDOFF-2026-11-09-2010-phase-3-mock-scenario-3-fleet agent notes).
