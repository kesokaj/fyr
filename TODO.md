# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Row 6 of `PLAN-2026-10-29-root-docs-and-fyr-serve` (Release 1; its § Order of work is the source of truth): `/preflight`, the agent's call (D37), after the behavioural runs the handoffs list (D12).

- [ ] The operator's `fyr eval` runs of the changed skills and agents, or their quoted waiver
- [ ] `/preflight vX.Y.Z`, the agent's call (D37)

## Next

- Row 7 of the same plan: `fyr serve` design (S1).
- Row 8 of the same plan: `fyr serve` read-only pages (S2).

## Backlog

- Thin `TDD.md` § T5's verb rows to one line and a spec link each, once every row's claims live in its `SPEC-fyr-*` record or `cmd/fyr/help_text.go` (added 2026-10-29, the row 4b review)
- Fix `GUIDE-testing` § 5's stale claim that the canary word is written in this repo's README and AGENTS.md (neither holds it) (added 2026-10-29, the row 5 report, item 3, operator: "go all approved")
- Give the catalog plan's D11 and D12 (only in git at `9001831^:.agents/docs/plans/2026-10-01-google-skills-catalog.md`) a home a record can point at (added 2026-10-29, the row 5 report, item 4, operator: "go all approved")
