# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Active plan: `PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` (Second-Brain AST Graph, Tiered Slicer & Zero-Daemon Hook Engine)

### Phase 2 — Personalized PageRank, Tiered Slicer & Graph Queries (after: Phase 1)
- [ ] Implement Aider's 5-multiplier Personalized PageRank (`×10` in prompt, `×10` `>= 8`-char ident, `×0.1` unexported, `×0.1` defined in `> 5` files, `×50` active session file, dampened by `sqrt(num_refs)`) in `internal/search/rank.go` — verify: unit, `go test -race ./internal/search/...`, who: AI — expect: 20-iteration power method ranks target pivots above generic utility helpers in `< 5 ms`
- [ ] Implement `BuildContextSlice` (`--slice`, `--hops`, `--budget` with `chars / 4` token estimate) and `BuildFileSkeleton` (`--skeleton`) in `internal/search/slice.go` with Hop-0 full bodies, Hop-1 bodies `<= 40` lines or 5-line call-site snippets, and Hop-2 signatures only — verify: unit, `go test -race ./internal/search/...`, who: AI — expect: `--slice Reconcile --budget 1800` emits Hop 0/1/2 capsule strictly within token budget
- [ ] Implement `FindSymbolPath` (`--path`), `FindAffected` (`--affected`), and `RepoMapSummary` (`--map`) in `internal/search/graph_queries.go` and wire flags into `cmd/fyr/ctx_verbs.go`, `cmd/fyr/verb_search_memory.go`, and `cmd/fyr/help_text.go` (`<= 79` runes per line, `<= 10` `about` lines, backticked flag metavars) — verify: unit, `go test -race ./cmd/fyr/... .`, who: AI — expect: `fyr search --slice`, `--skeleton`, `--path`, `--affected`, and `--map` pass `TestHelpLinesFit`, `TestHelpFlagsAreTheFlagSet`, `TestHelpFormsAgree`, `TestHelpPageShape`, `flags_test.go`, and `recall_test.go` — docs: cmd/fyr/help_text.go
- [ ] Checkpoint — expect: `fyr search` supports `--slice`, `--skeleton`, `--path`, `--affected`, and `--map` with full CLI help parity and unit test coverage — evidence: `go test -race ./...`, pending

## Next

- Phase 3 — Stateful Read-Deduplication Ledger & Bash Reader Interception (`PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` → T8)
- Phase 4 — Zero-Daemon Antigravity Hooks & Kickoff Capsule Injection (`PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` → T5, T6, T8, T18, T19)
- Phase 5 — Passive Co-Activity Memory, Staleness Flags & Read Telemetry (`PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` → T8, T15, T16)

## Backlog
