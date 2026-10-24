---
owner: main
date: 2026-10-24
---
# fyr plan: cold read answered, the old plan closed, the requirements set proposed     (date: 2026-10-24)

## Summary
Phase 0 of plan `2026-10-24-fyr-agy-only.md`, its AI boxes up to the operator's approval. The cold-reader check returned "design needs revision" and "implementation not ready" with 7 blocking questions; each is answered in the plan by D10–D22, and none needed the operator. The worst finding, the hourly `sh-agent-update.timer`, is rebutted for Phases 1–5: the real home runs the v2.7.0 release binary, which refuses `--repo`, so nothing from `main` reaches it. Its crossing half became a Phase 6 who: user step. Phases 3 and 4 are split into 3a/3b and 4a/4b. Plan `2026-10-23-session-sized-work.md` is closed at Phase 2b (its D35), so one plan is open. The `REQUIREMENTS.md` replacement set is drafted in `.agents/scratch/fyr/requirements-edit.md` and awaits the operator; ADR-062 and the requirements commit wait on it.

## Type
task (plan phase, part)

## Files changed
from git diff --stat HEAD (uncommitted at writing)
- `.agents/docs/plans/2026-10-24-fyr-agy-only.md` — cold-reader box and old-plan box ticked; Scope's ADR list, Terms line, Interfaces (verbs, marker, env, migration, crossing, agents, requirements set), F2/F4/F5/F7 rewritten, F8 added, E2E 1 and 3 reworded, Phase 2 file list corrected, Phases 3a/3b/4a/4b, a Phase 6 timer step, Risks, D10–D22
- `.agents/docs/plans/2026-10-23-session-sized-work.md` — Phases 3–6 boxes `[-]` with reasons; D35 supersedes D10 and D30
- `.agents/docs/reviews/2026-10-24-review-cold-read-fyr-agy-only.md` — the cold read (reviewer) and its `## Outcome`
- `ROADMAP.md` — Now names the fyr plan alone
- `.agents/docs/changes/2026-10-24-2012-fyr-cold-read-and-old-plan-closed.md` — this entry

## Agents dispatched
reviewer (the cold-reader check).

## Verification
`sh-agent search --open --kind plan` lists one plan, the fyr plan. `journalctl --user -u sh-agent-update.service` shows the 20:04 run refused by the v2.7.0 binary (D11's evidence). No source, test or deployed file changed, so the test suite was not run; no test reads `ROADMAP.md`, `REQUIREMENTS.md` or the plans (grep of `*_test.go`).

## Docs
no docs changed — trail and roadmap only; no interface, command or deploy step changed yet.

## Requirements
unchanged; the replacement set is proposed, not applied.
