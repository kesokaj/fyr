# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Active plan: `PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` (Second-Brain AST Graph, Tiered Slicer & Zero-Daemon Hook Engine)

### Phase 4 — Zero-Daemon Antigravity Hooks & Kickoff Capsule Injection (after: Phase 2, Phase 3)
- [ ] Implement `RunHook` (`pre-invocation`, `pre-tool-use`, `stop`) in `internal/search/hook.go` (wired to `fyr search --hook` in `cmd/fyr/`) with confidence-gated Turn-0 capsule injection (`>= 1` identifier `>= 6` chars and top PageRank `>= 3x` median), post-compaction memory + capsule re-priming via `BrainReprime` callback when `generation_id` increments (`beads` pattern), `on_capsule_injected` ledger pre-seeding, `> 15` dirty-file fast-path guard, `180K`-token knee warning, explicit store-degradation notice (`gh#5877`), and zero-token `systemMessage.metadata.user_markdown` banner — verify: unit, `go test -race ./internal/search/... ./cmd/fyr/...`, who: AI — expect: `pre-invocation`, `pre-tool-use`, and `stop` round-trip Antigravity hook JSON (`injectSteps`, `decision`) in `< 50 ms`, re-prime on compaction, and fail open (`exit 0`) with a visible degradation banner on store error
- [ ] Update `internal/harness/plan.go` (and `source_test.go`, `plan_test.go`, `install_test.go`, `cmd/fyr/status.go`, `cmd/fyr/zip_place.go`) to deploy `.gemini/config/hooks.json` invoking `$HOME/bin/fyr` (sandbox wrapper) else `$HOME/.local/bin/fyr` (installed host binary) — verify: unit, `go test -race ./internal/harness/... ./cmd/fyr/...`, who: AI — expect: `TestPlanFromFSSource`, `TestPlanAntigravityOnly`, and `cmd/fyr` status/install tests pass with `.gemini/config/hooks.json` planned
- [ ] Add structural `--slice` capsule formatting (`<= 1,500` tokens) and `read_ledger` pre-seeding helpers in `internal/work/kickoff.go` and `internal/auto/kickoff.go` (preserving `TestKickoffBuilder` when no search index is present) — verify: unit, `go test -race ./internal/work/... ./internal/auto/...`, who: AI — expect: worker and auto kickoffs include structural symbol capsules when a project search index exists and all existing `work`/`auto` tests pass
- [ ] Checkpoint — expect: `fyr search --hook` handles all three Antigravity lifecycle hooks, `fyr install` manages `hooks.json` across host and Docker sandboxes, and `work`/`auto` kickoffs support structural slices — evidence: `go test -race ./...`, pending

## Next

- Phase 5 — Passive Co-Activity Memory, Staleness Flags & Read Telemetry (`PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` → T8, T15, T16)
- Phase 6 — Cross-Agent Citation Rules, Docs & Byte-Budget Verification (`PLAN-2026-11-08-0938-plan-second-brain-ast-graph-tiered-slicer-zero-d` → T1, T2, T5, T8)

## Backlog
