---
owner: main
date: 2026-10-23
---
# Plan opened: session-sized work; EGM and session-sizing research records     (date: 2026-10-23)

## Summary
Two research records promoted from scratch (Google's Elephant-Goldfish Model against this harness; splitting work so a session never fills its context, with this box's metrics: 49 main sessions since 2026-09-01, median peak context 573K, 31 past 300K) and the feature plan they led to: an always-on session-sizing rule, a `## Kickoff` section in the handoff, phase sizing and a fourth cold-reader question, a peak-context metric, the EGM document standards and naive-reader lens, a `--drift` review lens, the reviewer-isolation decision, and (after the operator's redirection mid-session) a harness-owned auto-compact backstop that waits on a `REQUIREMENTS.md` edit. The plan's cold read (one report, 4 blocking, 10 non-blocking) is resolved in D11–D24; D25, D26 and D10 are open and wait on the operator.

## Type
plan (no source change)

## Files changed
- `.agents/docs/plans/2026-10-23-research-egm.md` — review of egm.dev / google/elephant-goldfish-model at d651fe86, 12 items
- `.agents/docs/plans/2026-10-23-research-session-sizing.md` — evidence and items for session-sized work
- `.agents/docs/plans/2026-10-23-session-sized-work.md` — the plan, six phases plus Phase 2b, each one session (D9)
- `.agents/docs/reviews/2026-10-23-review-session-sized-work-plan-cold-read.md` — the cold read (reviewer)
- `ROADMAP.md` — Now points at the plan
- this entry

## Agents dispatched
reviewer (cold read of the plan file only). No verifier (no source change); no changelog agent (this entry written from the diff).

## Verification
`go test ./internal/harness -run 'TestSkillListingBudget|TestRenderedContext' -v` read for the budgets the plan cites (19,274 of 19,500; 17,338 of 22,000; 14,929 of 15,000). Metrics read from a copy of the store in scratch, read-only. The web sources are named in each record.

## Docs
no how-it-works docs changed — plan and research only

## Requirements
D25 proposes an edit to § Non-goals (the `autoCompactWindow` key); not applied: awaiting the operator's approval in their own turn.
