# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Row 10 of `PLAN-2026-10-29-root-docs-and-fyr-serve` (Phase 5: `fyr check` rule (d) verifies the R → M → T traces; built on the operator's yes, D40). `PLAN-2026-10-29-workers` is done but for Phase 7d, the Mac (D112, who: user); whether BUG-198 needs a guard is still the operator's question (D101); D135's two `work` skill findings wait for the operator. The plan record wins on a conflict.

- [ ] Rule (d): every Must and Should R# of `PRD.md` in `SDD.md`'s trace, every M# in `TDD.md`'s
- [ ] Checkpoint

## Next

- Root-docs rows 11 (Release 2) and 12 (the smoke checks on dave).
- `PLAN-2026-10-30-ui-skills`: `frontend-ui-economist` and `frontend-ui-modern` replace `frontend`; Step 1's seven Open items wait for the operator (the zips in `.agents/scratch/ui-skills/`).
- Parallel dispatch (the operator, 2026-10-30, "Go with your recommendations"): first one agy run that puts two subagent dispatches in one turn, to see if they run at once; if they do, the two bullets go into the feature skill's lead step and the `explorer` description, never `rules/` (the rules block is at 21,032 of 22,000).

## Backlog

- Find why `fyr check` takes 3–4 minutes of CPU in this checkout (3m22s with the pre-row-8 binary, 99e4a0c), which `fyr serve`'s `/check` waits on (added 2026-10-29, row 8's manual walk and security review)
- Thin `TDD.md` § T5's verb rows to one line and a spec link each, once every row's claims live in its `SPEC-fyr-*` record or `cmd/fyr/help_text.go` (added 2026-10-29, the row 4b review)
- Fix `GUIDE-testing` § 5's stale claim that the canary word is written in this repo's README and AGENTS.md (neither holds it) (added 2026-10-29, the row 5 report, item 3, operator: "go all approved")
- Give the catalog plan's D11 and D12 (only in git at `9001831^:.agents/docs/plans/2026-10-01-google-skills-catalog.md`) a home a record can point at (added 2026-10-29, the row 5 report, item 4, operator: "go all approved")
