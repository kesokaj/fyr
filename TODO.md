# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

## Next

## Backlog

- 2026-11-09: `TestSliceVsGrepTokenEfficiencyAndRecall` comment claims must-pass and must-fail states for `evalSymbolRecall` but checks one partial case; add the 1.0 and 0.0 cases or reword (source: REVIEW-2026-11-09-2023-code-docs-review-plan-range-v8-1-0-head-phase-4, low).
- 2026-11-09: Optional live `GUIDE-eval.md` rows for the 3 mock scenarios (`scenario-ledger-mvp`, `scenario-worker-adr-fence`, `scenario-fleet-trajectory`), skipped from Phase 4 by the operator (source: PLAN-2026-11-09-1546-plan-end-to-end-feature-proof-matrix-3-mock-proj 4.1).
- 2026-11-09: `internal/auto/checks.go` `machineFindings` calls `Env.Usage` at `--level observe`, reporting `/usage unreadable` although G9 gates only above observe; unverified (source: HANDOFF-2026-11-09-2010-phase-3-mock-scenario-3-fleet agent notes).
- 2026-11-09: `internal/search/hook_clamp.go` counts raw line bytes (`len(line)+1`), whereas Antigravity `read_file` counts formatted `"%d: %s"` line prefixes toward its 46,080-byte cap (source: BUG-525 session probe).
- 2026-11-09: `internal/search/vet.go` `CheckADRConflict` matches single-word ADR tokens (`"search"`, `"agents"`) before `isKickoffPrompt`, emitting false-positive `VET_FIRST` on Kickoff prompts (source: BUG-525/BUG-526 session).
