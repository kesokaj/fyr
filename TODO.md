# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Workers Phase 8a of `PLAN-2026-10-29-workers` (a model per worker: the verb, its docs and ADR-080). Phase 8 is planned (D113–D131; the cold read `REVIEW-2026-10-30-1432-cold-read-workers-phase-8-a-model-per-worker` answered), split into 8a and 8b (the skills and their behavioural runs, after 8a). D115 and D116 wait for the operator's word (D122). Left in Phase 7: 7d, the Mac smoke (D112). Whether BUG-198 needs a guard is still the operator's question (D101). The plan runs before row 9 of `PLAN-2026-10-29-root-docs-and-fyr-serve` (workers D1, root-docs D50). The plan record wins on a conflict.

- [ ] ADR-080 stored before the code
- [ ] The `Model:` line and `--model`'s pattern
- [ ] Refusal 21, `internal/work/models.go`
- [ ] The worker's model and the contract
- [ ] The verb's help and the docs
- [ ] A real worker with a model on dave, two runs
- [ ] Checkpoint

## Next

- Root-docs rows 9 (S3, `fyr serve` actions), 10 (`fyr check` R → M → T), 11 (Release 2) and 12 (the smoke checks on dave).
- `PLAN-2026-10-30-ui-skills`: `frontend-ui-economist` and `frontend-ui-modern` replace `frontend`; Step 1's seven Open items wait for the operator (the zips in `.agents/scratch/ui-skills/`).
- Parallel dispatch (the operator, 2026-10-30, "Go with your recommendations"): first one agy run that puts two subagent dispatches in one turn, to see if they run at once; if they do, the two bullets go into the feature skill's lead step and the `explorer` description, never `rules/` (the rules block is at 21,032 of 22,000).

## Backlog

- Find why `fyr check` takes 3–4 minutes of CPU in this checkout (3m22s with the pre-row-8 binary, 99e4a0c), which `fyr serve`'s `/check` waits on (added 2026-10-29, row 8's manual walk and security review)
- Thin `TDD.md` § T5's verb rows to one line and a spec link each, once every row's claims live in its `SPEC-fyr-*` record or `cmd/fyr/help_text.go` (added 2026-10-29, the row 4b review)
- Fix `GUIDE-testing` § 5's stale claim that the canary word is written in this repo's README and AGENTS.md (neither holds it) (added 2026-10-29, the row 5 report, item 3, operator: "go all approved")
- Give the catalog plan's D11 and D12 (only in git at `9001831^:.agents/docs/plans/2026-10-01-google-skills-catalog.md`) a home a record can point at (added 2026-10-29, the row 5 report, item 4, operator: "go all approved")
