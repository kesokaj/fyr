# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Row 8 of `PLAN-2026-10-29-root-docs-and-fyr-serve` (S2, `fyr serve` read-only pages; its § Order of work is the source of truth). Row 7 designed it: `SPEC-fyr-serve`, ADR-075, `SDD.md` M16, `TDD.md` T16; the operator's answers are D39–D42. Until row 8 builds the verb, `fyr check` reports the spec's 14 names of it and its files (D43). Row 8's boxes are in the plan record.

## Next

- Row 9 of the same plan: `fyr serve` maintenance actions, `index` and `metrics ingest` (S3, D39).
- Row 10 of the same plan: `fyr check` verifies R → M → T (D40).
- Row 11 of the same plan: Release 2.

## Backlog

- Thin `TDD.md` § T5's verb rows to one line and a spec link each, once every row's claims live in its `SPEC-fyr-*` record or `cmd/fyr/help_text.go` (added 2026-10-29, the row 4b review)
- Fix `GUIDE-testing` § 5's stale claim that the canary word is written in this repo's README and AGENTS.md (neither holds it) (added 2026-10-29, the row 5 report, item 3, operator: "go all approved")
- Give the catalog plan's D11 and D12 (only in git at `9001831^:.agents/docs/plans/2026-10-01-google-skills-catalog.md`) a home a record can point at (added 2026-10-29, the row 5 report, item 4, operator: "go all approved")
