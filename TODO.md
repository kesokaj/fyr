# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

**Wave 10 (Phase 20 — Final Close)** (`PLAN-2026-11-22-2007-plan-fyr-v9-10-0-full-surface-mitigation-dynamic` — Phases 1–19 landed & `57/57` verified; `19/20` complete):
- [ ] **Phase 20 (Isolated 11-row `fyr eval` Gate & Independent `reviewer` `--code --docs` + `security` Close):** Run isolated `.agents/scratch/bin/fyr eval --home "$HOME/.config/fyr/dev/fyr-eval-mitigation"` on all 11 changed rows (`feature`, `reviewer-cold`, `dreamer`, `sim-avoid-repeat`, `ab-pipeline-on`, `ab-pipeline-off`, `vet-first-gate`, `feature-routing`, `read-ledger-dedup`, `autonomy`, `handoff`), dispatch independent `reviewer` (`--code --docs`, diff only over `v9.10.0..HEAD`) and `security`, store `REVIEW-*` records, and write `.agents/scratch/v9-10-0-mitigation-eval-review.json` (`"all_pass": true`)

## Next

- Phase 7 box 2 of `PLAN-2026-11-14-2337-plan-fyr-v9-build-control-self-improvement-2026`: 7-day post-tag `fyr report --md` retro against `RETRO-2026-11-15-0102-graph-a-b-baseline` (earliest `2026-11-23`)

## Backlog

None.
