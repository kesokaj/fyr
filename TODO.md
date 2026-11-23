# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

**Wave 4 — Phases 8, 12, 16 (parallel, disjoint leases)** (`PLAN-2026-11-22-2007-plan-fyr-v9-10-0-full-surface-mitigation-dynamic` — Phases 1–7 and 9–11 landed):
- [ ] **Phase 8 (WS2b + WS7c — `F-010`, `F-030`, `F-031`, `F-032`, `F-033`, `F-034`):** Stage `0o400` `models.json` & scoped read-only `agy` in `internal/isolate/`; thread `RealHome` & fail-fast `ErrNoAuthToken`; add `--worker-model`/`--worker-session`, pool-aware `relay` quota floor, `orchestration_runs` emission (`metrics record-run`), `LandReport.Detail`, `--judge` on `_run_fyr_land`, `.agents/trail.jsonl` lease refusal, and dynamic caller-family lookup in `workflows/*.py`
- [ ] **Phase 12 (WS8b — `F-035`, `F-036`):** Open `search.db` read-only (`< 25ms`, `OpenCopy` fallback on busy/cantopen) in `internal/watch/inspect_meta.go`, add cross-platform active-session liveness filter (`metrics.db.sessions.ended_at` + `o.ProcLister()` + `max(o.IdleSec*2, 300)s`), align role caps, and detect OpenSSH `(yes/no)?` / `(yes/no/[fingerprint])?`
- [ ] **Phase 16 (WS6b + WS8e — `F-015`, `F-041`):** Fix `skills/docker/SKILL.md:71,79` (`:nonroot` + `imagetools inspect`) and `skills/terraform/SKILL.md:15` (two-stage bootstrap), and align `context/AGENTS.md:15,20` + `skills/fyr-{bug,handoff}/SKILL.md` with `PRD.md` R7 single-handoff close (`D10`)

## Next

- Wave 5 (Phases 14, 17, parallel after Wave 4): WS8c CLI ergonomics & fail-fast `fyr close` stdin, WS6c skill prompt-tax diet (`6 >20 KB` skills + `golang` -> progressive `references/` & 23-pair keyword dedup) (`PLAN-2026-11-22-2007-plan-fyr-v9-10-0-full-surface-mitigation-dynamic`)
- Waves 6–10 (Phases 15, 13, 18, 19, 20): 31/31 `--json` parity, `fyr check` 8/8 drift & `GUIDE-eval.md` regexes, living docs/specs, full `fyr verify` & 57-repro sweep, isolated `fyr eval` & independent `reviewer`/`security` close (`PLAN-2026-11-22-2007-plan-fyr-v9-10-0-full-surface-mitigation-dynamic`)
- Phase 7 box 2 of `PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`: 7-day post-tag `fyr report --md` retro against `RETRO-2026-11-15-0102-graph-a-b-baseline` (earliest `2026-11-23`)

## Backlog

None.
