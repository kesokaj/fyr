# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Workers Phase 6 of `PLAN-2026-10-29-workers` ran on 2026-10-30 (D96–D98, `TEST-2026-10-30-0956-e2e-workers-phase-6-the-lead-loop-on-the-fixture`): the lead loop worked end to end, but the golden path failed its first squash on BUG-197 (`merge=union` duplicates sorted trail lines), and the lead wrote to its checkout mid-run (BUG-198). Both fixes are design questions for the operator; box 2 reruns after BUG-197's. The plan runs before row 9 of `PLAN-2026-10-29-root-docs-and-fyr-serve` (workers D1, root-docs D50). The plan record wins on a conflict.

- [x] The fixture is ready for a lead (D96: the sandbox `--home` sentence, `--repo`, the ingest read with `--source ~ --label`)
- [ ] The operator's `agy` lead on the fixture: the two question edges yes; the golden path no (BUG-197); reruns after BUG-197's fix, started by the agent in tmux
- [x] The verb-level edges: timeout (`no commits`, agy exit 0) and shared scope (refused)
- [x] The measure: 111,386 of 300,000 tokens (D97)
- [x] Whether the `work` skill gets a `GUIDE-eval` row: no (D98)
- [ ] Checkpoint: the E2E's `testing` record, change record, commit, push, handoff

## Next

- Workers Phase 7 (`--runner docker`, planned before it starts).
- Then root-docs rows 9 (S3, `fyr serve` actions), 10 (`fyr check` R → M → T), 11 (Release 2) and 12 (the smoke checks on dave).

## Backlog

- Find why `fyr check` takes 3–4 minutes of CPU in this checkout (3m22s with the pre-row-8 binary, 99e4a0c), which `fyr serve`'s `/check` waits on (added 2026-10-29, row 8's manual walk and security review)
- Thin `TDD.md` § T5's verb rows to one line and a spec link each, once every row's claims live in its `SPEC-fyr-*` record or `cmd/fyr/help_text.go` (added 2026-10-29, the row 4b review)
- Fix `GUIDE-testing` § 5's stale claim that the canary word is written in this repo's README and AGENTS.md (neither holds it) (added 2026-10-29, the row 5 report, item 3, operator: "go all approved")
- Give the catalog plan's D11 and D12 (only in git at `9001831^:.agents/docs/plans/2026-10-01-google-skills-catalog.md`) a home a record can point at (added 2026-10-29, the row 5 report, item 4, operator: "go all approved")
