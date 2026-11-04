# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

No phase active: item 11 of `PLAN-2026-11-02-1806-fyr-speed` is done (`runner docker` or `runner host (<reason>)` on the stamp line and each result line, `work.Check` and `fyr work --check`, `chooseRunner` wiring `Options.RunnerReason` in `internal/auto`, and `cmd/fyr/help_text.go`, `README.md`, `SDD.md`, `TDD.md`, `SPEC-fyr-work` and `GUIDE-testing` updated); the next session starts the first item of Next from the newest handoff's Kickoff.

Alongside it: item 1, the pilot build in `~/Workspace/booking-pilot`, run by the operator.

## Next

- Item 12 — single-binary Docker setup, Keychain token sync (`service="gemini"`, `account="antigravity"`), Colima `--vz-rosetta` `x86_64`+`arm64` `agy-linux` support, `~/.config/fyr/{sandbox,eval}/<project>` sandbox paths, and `fyr work config` (`workers=8`, `ram=8g`, `cpu=2`, `disk=10g`) (D7–D10).
- Item 14 — `--runner auto` as the default for `fyr work` (`docker` primary when preflight passes, else `host (<reason>)` secondary).
- Item 17 — reviewer pre-flight: list open bugs first, skip per-file `--refs` searches when empty.

## Backlog

- 2026-11-03 (item 10, BUG-504): `fyr eval`'s real-home watch fingerprints the whole `brain.db`, so another agy session's write fails every trial; compare per scope or per record instead (design question).
- 2026-11-03 (item 10): re-run `fyr eval work-tasks work-tasks-waves work-tasks-design --runs 3` while no other agy session works (1 of 9 by the verb, 9 of 9 on re-check, BUG-504).
- 2026-11-03 (item 10): the docker run of a task list, after item 12 builds `fyr-work:local` and the token on this Mac.
- 2026-11-03 (fyr check, REVIEW-2026-11-03-1143): pre-existing findings at `.agents/eval/GUIDE-eval.md:15`, `:24` (fixture paths) and `PRD.md:95` (R10).

- 2026-11-03 (item 9, ADR-501): an eval row for the `work` skill's duplicate-ID check (two workers delivering one record ID), and the `fyr eval work` run of the item 9 skill change.
- 2026-11-03 (item 4 baselines, TEST-2026-11-03-0320): preflight made a lightweight tag in 2 of 3 runs; a bug record and a preflight skill fix held to the preflight row.
- 2026-11-03 (item 4 baselines): the spec row is auto-denied one command in 2 of 3 runs; find the command before any spec-skill edit.
- 2026-11-02 (item 3, REVIEW-2026-11-02-1908): a darwin implementation of safety check e (leftover processes, O1), so eval registration runs on macOS.
- 2026-11-02 (item 3 review, finding 3): gitDirProblem has no size limit on `reftable/` tables for a reftable clone.
- 2026-11-01 (BUG-207): add the scratch-deletion prevention rule to `agents/verifier/agent.md` and `agents/reviewer/agent.md` with their behavioural runs.
