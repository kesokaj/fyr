# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Phase 3 of `PLAN-2026-10-29-workers` (`fyr work`: the lead session runs sandboxed `agy -p` workers): the `fyr work` verb for one Kickoff, with its ADR, the PRD edits and the map in one commit. Phase 2 (`internal/work`, one worker with no verb) is done at `b3b0e1a`. The plan runs before row 9 of `PLAN-2026-10-29-root-docs-and-fyr-serve` (workers D1, root-docs D50). The plan record wins on a conflict.

- [ ] `cmd/fyr/verb_work.go`: flags, exit statuses (D34), eval's signal handling; exactly one file; `--repo` as eval's
- [ ] The path's ADR and its restatements (AGENTS.md rule 3), written by `documentation`: the ADR, `PRD.md` R8, Non-goals and Known limits (D27), README map's node and olive line, the rest of the list
- [ ] The first real worker on `~/sandbox/fyr-work/fixture`: `trustedWorkspaces` honoured under `work/`, branch fetched, PASS
- [ ] Checkpoint: ONE commit for the verb, the ADR, the PRD, the map and the docs

## Next

- Workers Phase 4: several Kickoffs, `--max-workers`, `--kill`.
- Workers Phase 5: the `work` skill, the feature skill's `after:`/`files:`, the handoff skill's `Files:` line, `.gitattributes`.
- Then, in order: workers Phase 6 (the E2E, a lead session you start), Phase 7 (`--runner docker`); then root-docs rows 9 (S3, `fyr serve` actions), 10 (`fyr check` R → M → T), 11 (Release 2) and 12 (the smoke checks on dave).

## Backlog

- Find why `fyr check` takes 3–4 minutes of CPU in this checkout (3m22s with the pre-row-8 binary, 99e4a0c), which `fyr serve`'s `/check` waits on (added 2026-10-29, row 8's manual walk and security review)
- Thin `TDD.md` § T5's verb rows to one line and a spec link each, once every row's claims live in its `SPEC-fyr-*` record or `cmd/fyr/help_text.go` (added 2026-10-29, the row 4b review)
- Fix `GUIDE-testing` § 5's stale claim that the canary word is written in this repo's README and AGENTS.md (neither holds it) (added 2026-10-29, the row 5 report, item 3, operator: "go all approved")
- Give the catalog plan's D11 and D12 (only in git at `9001831^:.agents/docs/plans/2026-10-01-google-skills-catalog.md`) a home a record can point at (added 2026-10-29, the row 5 report, item 4, operator: "go all approved")
