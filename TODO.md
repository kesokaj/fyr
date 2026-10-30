# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Workers Phase 6 of `PLAN-2026-10-29-workers`: the E2E, a planning session on the fixture that becomes the lead (one session started by the operator). Phase 5b is done (the workers decision, the worker template and the lead exception; D95, ADR-077). The Task refreshing `SPEC-fyr-check`, `SPEC-fyr-eval` and `SPEC-fyr-serve` (workers D94) is done, with `SPEC-fyr-search` re-stamped too. The plan runs before row 9 of `PLAN-2026-10-29-root-docs-and-fyr-serve` (workers D1, root-docs D50). The plan record wins on a conflict.

- [ ] The fixture is ready for a lead: its `AGENTS.md` pins `--home ~/sandbox/fyr-work/home`, names no public flag and no storage format; `.gitattributes` only through the lead's step 1; `fyr metrics ingest` finds the agy sessions
- [ ] The operator starts `agy` on the fixture with the fixed `/feature` request and confirms Step 1 only; the session plans, cold-reads, decides `by: planner` (yes) and leads
- [ ] The verb-level edges by hand: a timeout and two Kickoffs sharing one file
- [ ] The measure: the lead's peak context, planning included, against 300,000 tokens
- [ ] Whether the `work` skill gets a `GUIDE-eval` row
- [ ] Checkpoint: the E2E's `testing` record, change record, commit, push, handoff

## Next

- Workers Phase 7 (`--runner docker`, planned before it starts).
- Then root-docs rows 9 (S3, `fyr serve` actions), 10 (`fyr check` R → M → T), 11 (Release 2) and 12 (the smoke checks on dave).

## Backlog

- Find why `fyr check` takes 3–4 minutes of CPU in this checkout (3m22s with the pre-row-8 binary, 99e4a0c), which `fyr serve`'s `/check` waits on (added 2026-10-29, row 8's manual walk and security review)
- Thin `TDD.md` § T5's verb rows to one line and a spec link each, once every row's claims live in its `SPEC-fyr-*` record or `cmd/fyr/help_text.go` (added 2026-10-29, the row 4b review)
- Fix `GUIDE-testing` § 5's stale claim that the canary word is written in this repo's README and AGENTS.md (neither holds it) (added 2026-10-29, the row 5 report, item 3, operator: "go all approved")
- Give the catalog plan's D11 and D12 (only in git at `9001831^:.agents/docs/plans/2026-10-01-google-skills-catalog.md`) a home a record can point at (added 2026-10-29, the row 5 report, item 4, operator: "go all approved")
