# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

No phase active: Item 17 of `PLAN-2026-11-02-1806-fyr-speed` is done (the reviewer lists every bug first, runs no `--refs` search when none, and one per directory holding a changed file otherwise; eval rows `reviewer-wide` and `reviewer-wide-bug`). The operator's order of 2026-11-05: the two open real-home-guard bugs first (BUG-507 graduated into ADR-505's guard, 2026-11-05), then the release, then the plan's remaining items as `/work` waves; the next session does the first item of Next from the newest handoff's Kickoff.

Alongside it: item 1, the pilot build in `~/Workspace/booking-pilot`, run by the operator on v7.0.0 before `/preflight`, never while an eval runs (BUG-504).

## Next

- BUG-504 — `fyr eval`'s real-home watch compares per scope or per record instead of fingerprinting the whole `brain.db`, so another agy session's write no longer fails every trial; its design is asked of the operator first, with an ADR.
- The release — the prep in `HANDOFF-2026-11-05-1011-fyr-speed-item-17-done` § Open at the post-fix HEAD (records regenerated or restored, the eval sweep, a testing record), then the operator's pilot (item 1) and `/preflight vX.Y.Z` in the operator's own turn.

## Backlog

- 2026-11-05 (BUG-507 verification, TEST-2026-11-05-1138): `GUIDE-testing` § 3c and `TDD.md:44` name `GUIDE-fyr-matrix` as the verb × place-kind spec, and no record of that ID exists in the store, so § 3c cannot run as written (the binary × place cells live in `cmd/fyr/place_matrix_test.go`); restore the record or point § 3c at the test.
- 2026-11-05 (BUG-507 class sweep): `fyr eval`'s suite fallback (`readRecordSuite`, `cmd/fyr/eval.go:241`) opens the real home's `brain.db` through `brain.ReadScope`, which migrates it, when a checkout has no tracked suite and `--home` names another home; ADR-505's guard keys on a missing `--home`, so it does not cover this; a read-only open or no fallback to the real home, a design question.
- 2026-11-05 (item 17 closing report, row 5): `GUIDE-testing` § 3l names `auto-*` eval rows that the tracked suite does not hold (only dave's old `GUIDE-eval` record held them); fix after the tag, with the § 4 fixture-path row below.
- 2026-11-05 (item 17 closing report, row 7): item 17's eval checks do not verify that `fyr record list --kind bug` runs first or that the reviewer runs one `--refs <dir>/` search per directory (a review citing `BUG-001` from the list alone passes), and no fixture changes a root file; a check could read the `--refs` lines from the trial's conversation databases, its scenario in a new file (`.agents/eval/fixture.sh` is 467 LOC).
- 2026-11-04 (item 17, proposal): the `security` agent's pre-flight still runs one `fyr search --refs` per changed file (`agents/security/agent.md:22`); the reviewer's item 17 change, with its own eval rows, would apply there too.
- 2026-11-04 (item 17): `GUIDE-testing` § 4 and Item 42's verify line name `--fixture ~/.config/fyr/dev/fyr-eval-fixture`, which does not exist on this Mac; every recorded `fyr eval` run used the default fixture, this checkout.
- 2026-11-04 (ADR-504a, pre-existing): a stale default image whose rebuild fails reads `holds fyr "vold", not this fyr's … (<nil> (auto-build failed: …))` (`internal/work/image_build.go:218`): the `<nil>` is the version read's error when the version read but differed; `--check` mirrors the run's words.
- 2026-11-04 (ADR-504a review, pre-existing): `fyr work --check` ignores `harness.ResolveSource`'s error and a cwd that is no git checkout (`cmd/fyr/verb_work.go:152-155`), which a run refuses (`:71-78`), so `--check` can name a runner for a run that refuses.
- 2026-11-04 (ADR-504a review R2-2, residual): `fyr auto`'s marked test runs `fyr-work:local` with the caller's whole environment (`internal/auto/verbs.go:79`), so a `DOCKER_DEFAULT_PLATFORM` naming another platform than the daemon's asks for one the image, now always built for the daemon's, does not have, and the daemon refuses it (fails closed); run it with `sandbox.DockerEnv`, with a test.
- 2026-11-04 (BUG-506 residual): a long `FAIL (<err>)` detail or eval-gate reason still comes before `branch <b> kept` on `fyr auto`'s 500-rune sweep line (`short`, `internal/auto/run.go:208`), so it can push the branch off the line.
- 2026-11-04 (item 14): the plan's measure, the same two-task run on both runners, not run (no linux `agy` at `~/.config/fyr/work/agy-linux`, and a development build always resolves to host); folded into Item 42.
- 2026-11-03 (item 10): re-run `fyr eval work-tasks work-tasks-waves work-tasks-design --runs 3` while no other agy session works (1 of 9 by the verb, 9 of 9 on re-check, BUG-504); item 14 changed all three rows' prompts and checks (2026-11-04: the runner log), so the re-run also covers that change before a release.
- 2026-11-03 (item 10): the docker run of a task list, after item 12 builds `fyr-work:local` and the token on this Mac.
- 2026-11-03 (fyr check, REVIEW-2026-11-03-1143): pre-existing findings at `.agents/eval/GUIDE-eval.md:15`, `:24` (fixture paths) and `PRD.md:95` (R10); a fixture path written `<clone>/…` is no path candidate (`internal/check/docs.go:143`), the form item 17's scenario rows use (2026-11-05).

- 2026-11-03 (item 9, ADR-501): an eval row for the `work` skill's duplicate-ID check (two workers delivering one record ID), and the `fyr eval work` run of the item 9 skill change.
- 2026-11-03 (item 4 baselines, TEST-2026-11-03-0320): preflight made a lightweight tag in 2 of 3 runs; a bug record and a preflight skill fix held to the preflight row.
- 2026-11-03 (item 4 baselines): the spec row is auto-denied one command in 2 of 3 runs; find the command before any spec-skill edit.
- 2026-11-02 (item 3, REVIEW-2026-11-02-1908): a darwin implementation of safety check e (leftover processes, O1), so eval registration runs on macOS.
- 2026-11-02 (item 3 review, finding 3): gitDirProblem has no size limit on `reftable/` tables for a reftable clone.
- 2026-11-01 (BUG-207): add the scratch-deletion prevention rule to `agents/verifier/agent.md` and `agents/reviewer/agent.md` with their behavioural runs.
