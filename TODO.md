# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

**Phase 5 — WS7a: Brain `unmigratedDocsDir` Fix, Symmetric `operator` Provenance Guard, `[STALE]` Display & Validity Curve Calibration** (`PLAN-2026-11-22-2007-plan-fyr-v9-10-0-full-surface-mitigation-dynamic` — Phases 1–4 landed; Phase 5 + Wave 3 ready in parallel):
- [ ] Restrict `unmigratedDocsDir` (`brain_session.go:46-56`) to refuse ONLY when `docs/` or `.agents/docs/` contains actual legacy file-era trail records (`PLAN-*`, `ADR-*`, `BUG-*`, `CHANGE-*`, `HANDOFF-*`) (`F-026`)
- [ ] Enforce symmetric `operator` provenance immutability in `MemoryAdd` (`memory.go:151`) and `MemoryDraftWithOptions("merge")` (`memory_draft.go:91`/`memory_draft_prior.go`), and emit `[STALE]` / JSON `"stale"` field in `MemoryShow*` (`memory_show.go`/`stdin_doc.go`) (`F-047`)
- [ ] Recalibrate `ComputeMemoryValidity` & `MemoryStalenessTag` (`validity.go`, `resume_memory.go`, `#13`, D3): keep `MinActiveValidity = 0.40`, `derived`/`default` base `min(0.80 + 0.05*min(ev,2), 0.90)`, decay floor `0.35`, `contraCap = 3`, content-hash staleness (`F-048`)
- [ ] Checkpoint — `F-026`, `F-047`, `F-048` pass `go test -race ./internal/brain`

## Next

- Wave 3 (Phases 6, 7, 9, 10, 11, parallel — all dependencies Phases 1–3 landed; disjoint from Phase 5): WS1b/WS6a vet & dynamic BM25 prefetch, WS2a/WS8a workflow allowlist/clamp/bash/watchdog, WS5a/b/c metrics truth, diagnostics & report KPIs (`PLAN-2026-11-22-2007-plan-fyr-v9-10-0-full-surface-mitigation-dynamic`)
- Waves 4–10 (Phases 8, 12–20): WS2b/WS7c gate/relay/isolate, watch `<25ms`, CLI ergonomics & 31/31 `--json`, `fyr check` 8/8, skill diet, living docs, 57-repro sweep, isolated `fyr eval` & review/security close (`PLAN-2026-11-22-2007-plan-fyr-v9-10-0-full-surface-mitigation-dynamic`)
- Phase 7 box 2 of `PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`: 7-day post-tag `fyr report --md` retro against `RETRO-2026-11-15-0102-graph-a-b-baseline` (earliest `2026-11-23`)

## Backlog

None.
