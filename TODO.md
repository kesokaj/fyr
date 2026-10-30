# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

No active phase. Row 10 of `PLAN-2026-10-29-root-docs-and-fyr-serve` is done (`fyr check` rule (d), D54). The `work` skill fix has landed (D136, D137), so row 11, Release 2, waits only on the operator's word (D55). Its behavioural runs, `work-kickoff-model` among them, stay the operator's (D12). The `PRD.md:114` strike has landed (ADR-081), and the Must/Should reshape is drafted and waits for the operator's approval (D55 (2)). `PLAN-2026-10-29-workers` is done except Phase 4's Mac box and Phase 7d, the Mac (D112, who: user). Whether BUG-198 needs a guard is still the operator's question (D101). D138's hardening Task and hardening-2 (D139, D140: the added records' date window, the worker Never line, step 8–9 residuals, the `work-squash-r2` eval row) have landed (`CHANGE-2026-10-30-2015-change-hardening-2-d139-d140-added-records-dated`); the PRD.md Task has landed with the three spec re-stamps (D141 item 2); the audit of the three open plans (D141 item 4) is done (`REVIEW-2026-10-30-2101-review-plan-audit-of-the-three-open-plans-agains`: 15 items, one ordered path); its items wait for the operator's answers; the `TZ` Task (D141 item 3a) is placed as workers Phase 9 (D143), before Release 2, its Step 1 is answered (D144: `$TZ`, else the `/etc/localtime` link name, else docker refused; `-e TZ` through `agyEnv` plus the image guard) its interfaces are stored, its cold read is answered (`REVIEW-2026-10-30-2211-cold-read-workers-phase-9-the-worker-s-zone`, D145), and Phase 9 is built and checkpointed (D146, D147): the lead's zone reaches the worker as `TZ`, the date check holds the lead's day only, and two real docker workers on dave passed (`TEST-2026-10-30-2236-testing-workers-phase-9-two-real-docker-workers`); step 7.3's sandbox question stays deferred. The hygiene Task (its items 5–9) is done: both plans' text matches their boxes (workers D142, root-docs D56), the deferrals are in Backlog, `GUIDE-testing` § 5's canary claim is fixed, and the catalog plan is stored as `PLAN-2026-10-01-google-skills-catalog`. The plan record wins on a conflict.

## Next

- Root-docs rows 11 (Release 2, on the operator's word) and 12 (the smoke checks on dave).
- `PLAN-2026-10-30-ui-skills`: `frontend-ui-economist` and `frontend-ui-modern` replace `frontend`; Step 1's seven Open items wait for the operator (the zips in `.agents/scratch/ui-skills/`).
- Parallel dispatch (the operator, 2026-10-30, "Go with your recommendations"): first one agy run that puts two subagent dispatches in one turn, to see if they run at once; if they do, the two bullets go into the feature skill's lead step and the `explorer` description, never `rules/` (the rules block is at 21,032 of 22,000).

## Backlog

- Find why `fyr check` takes 3–4 minutes of CPU in this checkout (3m22s with the pre-row-8 binary, 99e4a0c), which `fyr serve`'s `/check` waits on (added 2026-10-29, row 8's manual walk and security review)
- Thin `TDD.md` § T5's verb rows to one line and a spec link each, once every row's claims live in its `SPEC-fyr-*` record or `cmd/fyr/help_text.go` (added 2026-10-29, the row 4b review)
- Bind an added record's ID stamp to its `date` in `fyr work`'s scope check (added 2026-10-30, workers D141 item 3b)
- Date-check a record a worker names and edits, not only one it adds (added 2026-10-30, workers D141 item 3c)
- Step 7.3 of the `work` skill runs the worker's code outside the sandbox: a design question for when `--runner docker` becomes the default (added 2026-10-30, workers D138 item 5)
- Per-container `--memory`, `--memory-swap` and `--cpus` limits, and a run network with `enable_icc=false` in place of the default bridge, for `--runner docker`; put to the operator, unanswered (added 2026-10-30, workers D109; the plan audit's item 10)
- Worker session metrics: a `work=<name>` source in `fyr metrics`, if `/retro` needs it (added 2026-10-30, workers § Scope, follow-ups)
- agy's `--settings` (session-only settings) in place of a written `settings.json`, for `fyr eval` and `fyr work` (added 2026-10-30, workers § Scope, follow-ups; D20)
- One strict trail-line parser shared by `internal/brain` and `internal/work`, through a package with no SQLite driver (added 2026-10-30, workers § Scope, follow-ups; D65)
- A check that `TODO.md` Now mirrors the active plan's boxes (added 2026-10-30, root-docs D9's follow-up, never asked in row 7; D56)
- Watch: whether BUG-198 (the lead committed and reset its checkout during a `fyr work` run) recurs on the next lead loop; a guard is the operator's question (added 2026-10-30, workers D101; the plan audit's item 11)
