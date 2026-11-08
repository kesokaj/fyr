# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Active plan: `PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` (Second-Brain AST Graph, Tiered Slicer & Zero-Daemon Hook Engine)

### Phase 1 — PRD R3 Amendment, ADR-511 & AST Symbol Graph Store (after: none)
- [x] Record `ADR-511` (Zero-Daemon CLI Hooks and Pure-Go AST Graph in `internal/search`, following `ADR-510` in `v7.3.0`) and update `PRD.md` R3, `SDD.md`, `TDD.md`, and `TODO.md` — verify: unit, `go test -race .`, who: AI — expect: `ADR-511` stored in trail and `PRD.md` R3 explicitly permits `< 250 ms` fail-open CLI hooks with zero daemons — docs: PRD.md
- [ ] Bump `schemaVersion` from `"3"` to `"4"` in `internal/search/store.go` and add `symbols`, `sym_refs`, `read_ledger`, `session_meta`, and `co_activity` tables (`kind`, `confidence`, `weight`, `body_hash`) wired into `internal/search/reconcile.go` — verify: unit, `go test -race ./internal/search/...`, who: AI — expect: `reconcile` populates `symbols` and `sym_refs` per blob `oid` and skips unchanged blobs in `< 5 ms`
- [ ] Implement stdlib `go/parser` + `go/ast` extraction (`from_sym` = `Recv.Name` or `Name`) and implicit Go interface dispatch (`maxImplicitImplementers = 10`, `INFERRED` `0.7` weight) in `internal/search/symbols_go.go` plus `.ts`/`.tsx`/`.js`/`.py`/`.sh` lexical extraction and markdown backtick `doc_ref` resolution (`from_sym = "<doc>"`, `kind = "doc_ref"`, `INFERRED` `0.7`) in `internal/search/symbols_code.go` — verify: unit, `go test -race ./internal/search/... ./cmd/fyr/...`, who: AI — expect: non-test Go caller resolution on `fyr` reaches `>= 90%` and `TestFunctionLength` passes
- [ ] Checkpoint — expect: `ADR-511` recorded, `PRD.md` updated, and `internal/search` indexes AST symbols, call/type/interface edges, and markdown backtick refs with all tests green — evidence: `go test -race ./...`, pending

## Next

- Phase 2 — Personalized PageRank, Tiered Slicer & Graph Queries (`PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` → T5, T8)
- Phase 3 — Stateful Read-Deduplication Ledger & Bash Reader Interception (`PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` → T8)
- Phase 4 — Zero-Daemon Antigravity Hooks & Kickoff Capsule Injection (`PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` → T5, T6, T8, T18, T19)

## Backlog
