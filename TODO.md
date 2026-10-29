# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Workers Phase 3 of `PLAN-2026-10-29-workers`: the `fyr work` verb for one Kickoff, with its ADR, the PRD edits and the map in one commit. The re-plan after the operator's addendum is approved (D54–D69) and its cold read is answered (D70–D90). The plan runs before row 9 of `PLAN-2026-10-29-root-docs-and-fyr-serve` (workers D1, root-docs D50). The plan record wins on a conflict.

- [ ] `cmd/fyr/verb_work.go`: flags `--home --repo --timeout --model --keep`, one file, exit statuses, signals (D89)
- [ ] The path's ADR and its restatements (PRD R8, Non-goals, Known limits; the README map; AGENTS.md rules 1 and 4)
- [ ] `Config.SkipStores` by prefix, `state.json` fingerprinted again, the contract's 39 lines (D70, D85)
- [ ] `SPEC-fyr-work` accepted on the operator's word after reading D47 (who: user; not a Checkpoint condition, D69)
- [ ] The first real worker on `~/sandbox/fyr-work/fixture`
- [ ] Checkpoint: one commit for the verb, the ADR, the PRD, the map and the docs; push; the real-home install on dave; handoff

## Next

- Workers Phase 4: several Kickoffs, `--max-workers`, `--kill`.
- Workers Phase 5a (the `work` skill), then 5b (the feature skill's workers decision, the worker template, the session-sizing clause and its ADR).
- Then, in order: workers Phase 6 (the E2E, a planning session that becomes the lead), Phase 7 (`--runner docker`); then root-docs rows 9 (S3, `fyr serve` actions), 10 (`fyr check` R → M → T), 11 (Release 2) and 12 (the smoke checks on dave).

## Backlog

- Find why `fyr check` takes 3–4 minutes of CPU in this checkout (3m22s with the pre-row-8 binary, 99e4a0c), which `fyr serve`'s `/check` waits on (added 2026-10-29, row 8's manual walk and security review)
- Thin `TDD.md` § T5's verb rows to one line and a spec link each, once every row's claims live in its `SPEC-fyr-*` record or `cmd/fyr/help_text.go` (added 2026-10-29, the row 4b review)
- Fix `GUIDE-testing` § 5's stale claim that the canary word is written in this repo's README and AGENTS.md (neither holds it) (added 2026-10-29, the row 5 report, item 3, operator: "go all approved")
- Give the catalog plan's D11 and D12 (only in git at `9001831^:.agents/docs/plans/2026-10-01-google-skills-catalog.md`) a home a record can point at (added 2026-10-29, the row 5 report, item 4, operator: "go all approved")
