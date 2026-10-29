# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Phase 2 of `PLAN-2026-10-29-workers` (`fyr work`: the lead session runs sandboxed `agy -p` workers): `internal/work`, one worker with no verb yet. Phase 1 (`internal/sandbox` extracted from `internal/eval`) is done at `d66d7cf`. The plan runs before row 9 of `PLAN-2026-10-29-root-docs-and-fyr-serve` (workers D1, root-docs D50). The plan record wins on a conflict.

- [ ] `agy --version` and `agy --help`: `--model`, `--mode`, `--print-timeout` still listed
- [ ] `internal/work`: Kickoff parse, name and `Files:` rules, refusals 1–4 and 7–10, steps 1–9 for one worker, scope check, drafts copy, lead-checkout check, result contract, `Config.SkipStores` (D45); tests first, with a fake agy
- [ ] Checkpoint

## Next

- Workers Phase 3: the `fyr work` verb, its ADR, the PRD R8 edits and the README map line in one commit; the first real worker.
- Workers Phase 4: several Kickoffs, `--max-workers`, `--kill`.
- Then, in order: workers Phase 5 (the `work` skill), Phase 6 (the E2E, a lead session you start), Phase 7 (`--runner docker`); then root-docs rows 9 (S3, `fyr serve` actions), 10 (`fyr check` R → M → T), 11 (Release 2) and 12 (the smoke checks on dave).

## Backlog

- Find why `fyr check` takes 3–4 minutes of CPU in this checkout (3m22s with the pre-row-8 binary, 99e4a0c), which `fyr serve`'s `/check` waits on (added 2026-10-29, row 8's manual walk and security review)
- Thin `TDD.md` § T5's verb rows to one line and a spec link each, once every row's claims live in its `SPEC-fyr-*` record or `cmd/fyr/help_text.go` (added 2026-10-29, the row 4b review)
- Fix `GUIDE-testing` § 5's stale claim that the canary word is written in this repo's README and AGENTS.md (neither holds it) (added 2026-10-29, the row 5 report, item 3, operator: "go all approved")
- Give the catalog plan's D11 and D12 (only in git at `9001831^:.agents/docs/plans/2026-10-01-google-skills-catalog.md`) a home a record can point at (added 2026-10-29, the row 5 report, item 4, operator: "go all approved")
