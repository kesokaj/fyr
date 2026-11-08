# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Active plan: `PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` (Second-Brain AST Graph, Tiered Slicer & Zero-Daemon Hook Engine)

### Phase 5 — Passive Co-Activity Memory, Staleness Flags & Read Telemetry (after: Phase 4)
- [ ] Implement `co_activity` recording (`co_read`, `co_modified`) on `Stop` hook in `internal/search/co_activity.go` and surface `co_change_hint` (`count >= 3`) + linked open `BUG-*`/`ADR-*` records (from `refs` and `Options.BrainRefs`) in `slice.go` and `graph_queries.go` — verify: unit, `go test -race ./internal/search/...`, who: AI — expect: files co-modified `>= 3` times appear as `co_change_hint` and linked `BUG-*`/`ADR-*` records surface in `--slice` and `--affected`
- [ ] Add file-hash staleness verification (`[STALE: <path> modified since <ID>]`) and a 1-line `Ready phases:` DAG summary on open `PLAN-*` records in `internal/brain/resume.go` and `internal/brain/search.go` — verify: unit, `go test -race ./internal/brain/...`, who: AI — expect: memories referencing a file modified after the record's timestamp are flagged `[STALE]` and `fyr resume` lists unblocked plan phases
- [ ] Hash normalized `Read`/`Write`/`Edit` target paths with `w.hash(rel)` prefixed `"r:"` (ranged) or `"f:"` (full) into `steps.cmd_hash` in `internal/metrics/agy_tools.go` (`agy.go`, `agy_conversation.go`) and report `RedundantReads` and `RangedReadPct` alongside `Search : Grep` in `internal/metrics/show_types.go`, `show_add.go`, `show_finish.go`, and `show_write.go` — verify: unit, `go test -race ./internal/metrics/...`, who: AI — expect: `fyr metrics show` displays redundant read count, ranged read percentage, and search-to-grep ratio without storing raw paths
- [ ] Checkpoint — expect: co-activity hints, `[STALE]` memory flags, ready-phase summaries, and `fyr metrics show` read-efficiency telemetry pass all unit tests — evidence: `go test -race ./...`, pending

## Next

- Phase 6 — Prompt Vetting, Intent Router, Skill Prefetcher & Turn Contract (`PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` → T5, T8)
- Phase 7 — Cross-Agent Citation Rules, Docs & Byte-Budget Verification (`PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` → T1, T2, T5, T8)

## Backlog
