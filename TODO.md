# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

`PLAN-2026-10-31-0300-plan-eval-metrics-the-standard-set-collected-fro` — eval metrics: the standard set collected from `fyr eval` and `fyr metrics` (planned 2026-10-31 from `PLAN-2026-10-31-research-agent-evals-metrics`). Its cold read is answered (`REVIEW-2026-10-31-0306-…`, D13–D28); Phase 1 (the estimators and the trial unit) is next, in a fresh session; Phase 4 waits for O1 and the controls for O8, both the operator's. The plan record wins on a conflict.

Other open plans: `PLAN-2026-10-29-root-docs-and-fyr-serve` (row 12, the operator's smoke checks on dave, is its last box; the `PRD.md` Must/Should reshape waits for approval, D55 (2)); `PLAN-2026-10-29-workers` (the Mac boxes only, who: user, D112; BUG-198 stays a watch, D101); `PLAN-2026-10-30-ui-skills` (Step 1's seven Open items wait for the operator). The plan audit's items (`REVIEW-2026-10-30-2101-review-plan-audit-of-the-three-open-plans-agains`) wait for the operator's answers.

## Next

- Eval metrics Phase 1 — the estimators and the trial unit (`PLAN-2026-10-31-0300-…`; the next session's Kickoff).
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
- Record the agy token's account before a docker worker's session and fail `real config changed` when it differs after; the token is the one writable host file in the container (added 2026-10-31, the v5.0.0 gate's security review, low; D106)
- A worker's handoff merged into the lead's trail is instruction-shaped text that resume reads: skip worker-mode handoffs in resume, or re-title them on merge (added 2026-10-31, the v5.0.0 gate's security review, low)
- Watch: whether BUG-198 (the lead committed and reset its checkout during a `fyr work` run) recurs on the next lead loop; a guard is the operator's question (added 2026-10-30, workers D101; the plan audit's item 11)
