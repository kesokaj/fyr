# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

First, one Task: fix BUG-196 (the operator, 2026-10-30, workers D94; the newest handoff's Kickoff). Then:

Workers Phase 5b of `PLAN-2026-10-29-workers`: the workers decision, the worker template and the lead exception, in one commit. Phase 5a is done (the `work` skill; D93). The plan runs before row 9 of `PLAN-2026-10-29-root-docs-and-fyr-serve` (workers D1, root-docs D50). The plan record wins on a conflict.

- [ ] The feature skill: the `after:` and `files:` lines, the fifth cold-read question, the workers decision, the "ends here" exception, the gate rule's sentence, Step 3's cold-read gate in a session that may lead, `planner` and `lead` in the `by:` list
- [ ] The handoff skill: the optional `Files:` line, the worker-mode Kickoff, the worker-mode paragraph
- [ ] The lead exception: the `rules/session-sizing.md` clause, the ADR amending ADR-058 (1) and (4), `SDD.md`:146, `AGENTS.md` § Rules' dave sentence, `.gitattributes`
- [ ] Behavioural runs: `fyr eval` rows `feature` and `handoff`, n of N in a `testing` record
- [ ] Checkpoint: full test command, `reviewer` with `/review --docs`, `verifier` log, change record, ONE commit, push, the real-home install on dave, handoff

## Next

- Workers Phase 6 (the E2E, a planning session that becomes the lead), then Phase 7 (`--runner docker`).
- Then root-docs rows 9 (S3, `fyr serve` actions), 10 (`fyr check` R → M → T), 11 (Release 2) and 12 (the smoke checks on dave).

## Backlog

- Find why `fyr check` takes 3–4 minutes of CPU in this checkout (3m22s with the pre-row-8 binary, 99e4a0c), which `fyr serve`'s `/check` waits on (added 2026-10-29, row 8's manual walk and security review)
- Thin `TDD.md` § T5's verb rows to one line and a spec link each, once every row's claims live in its `SPEC-fyr-*` record or `cmd/fyr/help_text.go` (added 2026-10-29, the row 4b review)
- Fix `GUIDE-testing` § 5's stale claim that the canary word is written in this repo's README and AGENTS.md (neither holds it) (added 2026-10-29, the row 5 report, item 3, operator: "go all approved")
- Give the catalog plan's D11 and D12 (only in git at `9001831^:.agents/docs/plans/2026-10-01-google-skills-catalog.md`) a home a record can point at (added 2026-10-29, the row 5 report, item 4, operator: "go all approved")
