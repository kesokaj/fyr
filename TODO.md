# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Active plan: `PLAN-v9.6.0` (4 Waves, 9 Phases — `workers: yes`).

### Wave 1 (Done)
- [x] Phase 1 — `metrics.db` v11, concrete `f(3,28)->f(1,19)` model IDs, `14`-day decayed evidence & judge attribution (`fd28b47826b9`)
- [x] Phase 2 — `brain.db` v4 `repo_id`, `operator` provenance guard & merge hygiene (`c4a2412ef836`)
- [x] Phase 3a — Skills/rules/agents hygiene, `S1`–`S3`/`S5` diet, `18.2 MiB` PNG/`google-catalog` + 8 duplicate skills removal (`0db333f1d948`)

### Wave 2 (In Progress)
- [ ] Phase 3b — Unpack trimmed `skills/frontend-slate-ui/`, add `VENDORED.md` pins, and wire `vet_skills*` (`builder-p3b`)
- [ ] Phase 4 — Zero-codename Empirical-Bayes + UCB1 router (`internal/models`) & `fyr models --bench` (`builder-p4`)
- [ ] Phase 5 — Pure-stdlib `fyr state export` / `fyr state import` zip bundle (`internal/state`) (`builder-p5`)

## Next

- [ ] Phase 7 box 2 of `PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`: 7-day post-tag `fyr report --md` retro against `RETRO-2026-11-15-0102-graph-a-b-baseline` (earliest `2026-11-23`)

## Backlog

- [ ] Allow pure binary deletions (`D` status in `git diff --raw`) in `internal/gate/gate_checks.go:127` (`checkNumstat`) while continuing to reject binary adds/edits (`v9.6.1`, `PLAN-v9.6.0` `D21`, 2026-11-20)
