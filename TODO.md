# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Workers Phase 7a of `PLAN-2026-10-29-workers` (`--runner docker`: the Mac's answers, the token risk check, the image). Phase 7 was planned on 2026-10-30 (D102), cold-read in two rounds (D104, D105), and split into 7a, 7b-1, 7b-2, 7c and 7d (7d, the Mac, deferred). Phase 8, a model per worker, is placed and unplanned (D103). Whether BUG-198 needs a guard is still the operator's question (D101). The plan runs before row 9 of `PLAN-2026-10-29-root-docs-and-fyr-serve` (workers D1, root-docs D50). The plan record wins on a conflict.

- [ ] A planning session first (dave's part done; the Mac's `docker info`, write probe and a linux-arm64 agy owed by the operator)
- [x] Risk check on dave: a real `agy -p` in the container (the token mounts read-write, D106)
- [x] `docker/work.Dockerfile`, its ignore file, `internal/sandbox/imagepath.go`, `docker/` in `AGENTS.md` § Layout
- [ ] Checkpoint

## Next

- Root-docs rows 9 (S3, `fyr serve` actions), 10 (`fyr check` R → M → T), 11 (Release 2) and 12 (the smoke checks on dave).

## Backlog

- Find why `fyr check` takes 3–4 minutes of CPU in this checkout (3m22s with the pre-row-8 binary, 99e4a0c), which `fyr serve`'s `/check` waits on (added 2026-10-29, row 8's manual walk and security review)
- Thin `TDD.md` § T5's verb rows to one line and a spec link each, once every row's claims live in its `SPEC-fyr-*` record or `cmd/fyr/help_text.go` (added 2026-10-29, the row 4b review)
- Fix `GUIDE-testing` § 5's stale claim that the canary word is written in this repo's README and AGENTS.md (neither holds it) (added 2026-10-29, the row 5 report, item 3, operator: "go all approved")
- Give the catalog plan's D11 and D12 (only in git at `9001831^:.agents/docs/plans/2026-10-01-google-skills-catalog.md`) a home a record can point at (added 2026-10-29, the row 5 report, item 4, operator: "go all approved")
