# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Workers Phase 8b of `PLAN-2026-10-29-workers` (a model per worker: the skills and their behavioural runs). Phase 8a is done: `fyr work` reads a Kickoff's `Model:` line, checks every named model against `agy models` (refusal 21) and prints `model <ID>|default` on each result line (ADR-080). D115 and D116 kept (D132). Left in Phase 7: 7d, the Mac smoke (D112). Whether BUG-198 needs a guard is still the operator's question (D101). The plan runs before row 9 of `PLAN-2026-10-29-root-docs-and-fyr-serve` (workers D1, root-docs D50). The plan record wins on a conflict.

- [ ] The three skills (feature, handoff, work)
- [ ] A `GUIDE-eval` row `work-kickoff-model`
- [ ] The behavioural runs
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
