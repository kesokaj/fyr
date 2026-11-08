# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Active plan: `PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` (Second-Brain AST Graph, Tiered Slicer & Zero-Daemon Hook Engine)

### Phase 3 — Stateful Read-Deduplication Ledger & Bash Reader Interception (after: Phase 1)
- [ ] Implement `CheckAndRecordRead`, `RegisterCapsulePivots`, `CheckCompaction`, and `CheckPreWriteHash` in `internal/search/read_ledger.go` against the `read_ledger`, `session_meta`, and `symbols` tables in `.agents/scratch/search.db` — verify: unit, `go test -race ./internal/search/...`, who: AI — expect: 1st read stamps `sha256`, 2nd unchanged covering read returns `[UNCHANGED]` + symbol skeleton and consumes the stamp, 3rd retry is allowed, and `transcriptPath` shrinkage `> 20%` increments `generation_id`
- [ ] Implement `ParseBashFileRead(cmd string)` in `internal/search/read_ledger.go` to detect single-file `cat`, `head`, `tail`, `sed -n`, `nl`, and `bat` commands and route them through `CheckAndRecordRead` — verify: unit, `go test -race ./internal/search/...`, who: AI — expect: single-file `sed -n '1,120p' foo.go` and `cat foo.go` hit the read ledger while pipelines/redirects bypass it safely
- [ ] Checkpoint — expect: `read_ledger.go` enforces consume-on-hit deduplication, compaction resets, worktree path canonicalization, pre-write hash checks, and Bash reader parsing — evidence: `go test -race ./internal/search/...`, pending

## Next

- Phase 4 — Zero-Daemon Antigravity Hooks & Kickoff Capsule Injection (`PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` → T5, T6, T8, T18, T19)
- Phase 5 — Passive Co-Activity Memory, Staleness Flags & Read Telemetry (`PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` → T8, T15, T16)
- Phase 6 — Cross-Agent Citation Rules, Docs & Byte-Budget Verification (`PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` → T1, T2, T5, T8)

## Backlog
