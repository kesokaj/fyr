# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

No phase active: Item 17 of `PLAN-2026-11-02-1806-fyr-speed` is done (the reviewer lists every bug first, runs no `--refs` search when none, and one per directory holding a changed file otherwise; eval rows `reviewer-wide` and `reviewer-wide-bug`); the next session does the first item of Next, Item 18, from the newest handoff's Kickoff.

Alongside it: item 1, the pilot build in `~/Workspace/booking-pilot`, run by the operator.

## Next

- Item 18 — no explorer on an empty repo: skip explorer dispatch on an empty scaffolded repo.
- Item 13 — Node in the worker image (its docker worker run needs an installed, versioned `fyr` and a linux `agy` at `~/.config/fyr/work/agy-linux`).
- Item 19 — cold-reader budget: bound the cold read (one took 21 min and 128K output tokens), and the budgeted reader still finds every seeded high-severity flaw.

## Backlog

- 2026-11-04 (item 17, proposal): the `security` agent's pre-flight still runs one `fyr search --refs` per changed file (`agents/security/agent.md:22`); the reviewer's item 17 change, with its own eval rows, would apply there too.
- 2026-11-04 (item 17): `GUIDE-testing` § 4 and Item 42's verify line name `--fixture ~/.config/fyr/dev/fyr-eval-fixture`, which does not exist on this Mac; every recorded `fyr eval` run used the default fixture, this checkout.
- 2026-11-04 (ADR-504a, pre-existing): a stale default image whose rebuild fails reads `holds fyr "vold", not this fyr's … (<nil> (auto-build failed: …))` (`internal/work/image_build.go:218`): the `<nil>` is the version read's error when the version read but differed; `--check` mirrors the run's words.
- 2026-11-04 (ADR-504a review, pre-existing): `fyr work --check` ignores `harness.ResolveSource`'s error and a cwd that is no git checkout (`cmd/fyr/verb_work.go:152-155`), which a run refuses (`:71-78`), so `--check` can name a runner for a run that refuses.
- 2026-11-04 (ADR-504a review R2-2, residual): `fyr auto`'s marked test runs `fyr-work:local` with the caller's whole environment (`internal/auto/verbs.go:79`), so a `DOCKER_DEFAULT_PLATFORM` naming another platform than the daemon's asks for one the image, now always built for the daemon's, does not have, and the daemon refuses it (fails closed); run it with `sandbox.DockerEnv`, with a test.
- 2026-11-04 (BUG-506 residual): a long `FAIL (<err>)` detail or eval-gate reason still comes before `branch <b> kept` on `fyr auto`'s 500-rune sweep line (`short`, `internal/auto/run.go:208`), so it can push the branch off the line.
- 2026-11-04 (item 14): the plan's measure, the same two-task run on both runners, not run (no linux `agy` at `~/.config/fyr/work/agy-linux`, and a development build always resolves to host); folded into Item 42.
- 2026-11-03 (item 10, BUG-504): `fyr eval`'s real-home watch fingerprints the whole `brain.db`, so another agy session's write fails every trial; compare per scope or per record instead (design question).
- 2026-11-03 (item 10): re-run `fyr eval work-tasks work-tasks-waves work-tasks-design --runs 3` while no other agy session works (1 of 9 by the verb, 9 of 9 on re-check, BUG-504); item 14 changed all three rows' prompts and checks (2026-11-04: the runner log), so the re-run also covers that change before a release.
- 2026-11-03 (item 10): the docker run of a task list, after item 12 builds `fyr-work:local` and the token on this Mac.
- 2026-11-03 (fyr check, REVIEW-2026-11-03-1143): pre-existing findings at `.agents/eval/GUIDE-eval.md:15`, `:24` (fixture paths) and `PRD.md:95` (R10); a fixture path written `<clone>/…` is no path candidate (`internal/check/docs.go:143`), the form item 17's scenario rows use (2026-11-05).

- 2026-11-03 (item 9, ADR-501): an eval row for the `work` skill's duplicate-ID check (two workers delivering one record ID), and the `fyr eval work` run of the item 9 skill change.
- 2026-11-03 (item 4 baselines, TEST-2026-11-03-0320): preflight made a lightweight tag in 2 of 3 runs; a bug record and a preflight skill fix held to the preflight row.
- 2026-11-03 (item 4 baselines): the spec row is auto-denied one command in 2 of 3 runs; find the command before any spec-skill edit.
- 2026-11-02 (item 3, REVIEW-2026-11-02-1908): a darwin implementation of safety check e (leftover processes, O1), so eval registration runs on macOS.
- 2026-11-02 (item 3 review, finding 3): gitDirProblem has no size limit on `reftable/` tables for a reftable clone.
- 2026-11-01 (BUG-207): add the scratch-deletion prevention rule to `agents/verifier/agent.md` and `agents/reviewer/agent.md` with their behavioural runs.
