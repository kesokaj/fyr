# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

**Phase 4 — Wave 3b: Hotspot Monolith Decomposition, Complexity Reduction & `t.Parallel()`** (`PLAN-2026-11-27-craft-cleanup`, `ADR-537`):
- [ ] Slice `w3b-search-split`: extract `internal/readledger`, `internal/symgraph`, `internal/vet`, and `internal/hooks` out of `internal/search` (`114 -> <= 25` prod files/pkg), split `hook_pre_invocation.go`, refactor `summarizePackages`/`extractExprRefs` to `Cog <= 15`, scope `ftsTurnCache` per `Store`, rename milestone tests (`C2`, `A2`, `A6`, `C6`, `C8`, `C9`, `D10`) (`who: AI`)
- [ ] Slice `w3b-metrics-split`: extract `internal/metrics/report` and `internal/metrics/ingest` (`72 -> <= 35` prod files/pkg), refactor `populateAdherenceVerbs`/`joinRecords`/`(*Summary).verbs` to `Cog <= 15` / `Nest <= 3`, unexport internals, rename milestone tests (`C5`, `A5`, `C6`, `C8`, `D10`) (`who: AI`)
- [ ] Slice `w3b-sim-auto`: extract `internal/sim`, split `internal/brain/turn_records.go` SQL vs formatting, narrow `internal/auto` fan-out (`11 -> <= 6`), refactor `hasProposalEvidence` (`Cog <= 15`), dedup `auto`/`gate`/`dream` helpers, unexport internals (`C5`, `A2`, `A3`, `C7`, `C8`) (`who: AI`)
- [ ] Slice `w3b-isolate-parallel`: extract `internal/isolate/acp` (`52 -> <= 35` prod files/pkg), refactor `fingerprintWith` (`Cog <= 15` / `Nest <= 4`), align/share `splitShellSegments`, move global test seams onto `Options` structs and enable `t.Parallel()`, rename `v910_*_test.go` (`C5`, `C6`, `C7`, `C9`, `D4`, `D10`) (`who: AI`)
- [ ] Checkpoint (Phase 4 — Final): gate & land sequentially, update `cmd/fyr` callers + `t.Parallel()`, tighten all 4 fitness ratchets (`<= 35` prod files/pkg, `Cog <= 15`, `Nest <= 4`), update `areas.md`/`SDD.md`/`TDD.md`, run full verify + uncapped `golangci-lint`, run independent `reviewer` + `security` lenses, clear `TODO.md`, commit & push (`who: AI`)

## Next

- Workstream 2 — Automatic Antigravity Conversation Retention (`fyr conversations`, `ADR-538`)
- Release Preflight — `/fyr-preflight v9.14.0` once Workstream 1 + Workstream 2 verify green (followed by read-only `fyr conversations prune --dry-run` preview after install)

## Backlog

- 2026-11-23 — Re-run 7-day post-v9.11.0 telemetry check (`PreInvocation` timeout %, `n=0` `Search:Grep`, steps `>= 150k`, tokens/turn) 7 days after `v9.11.0` reaches real `$HOME` (source: `RETRO-2026-11-23-1840-post-v9-7-day-telemetry-retro-v9-0-0-2026-11-23`).
